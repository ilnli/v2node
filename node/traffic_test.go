package node

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/common/trafficqueue"
	"github.com/wyx2685/v2node/conf"
	"github.com/wyx2685/v2node/core"
	"github.com/wyx2685/v2node/limiter"
)

func TestManagedShutdownPreservesFailedAndFinalTraffic(t *testing.T) {
	const userUUID = "2d795b6f-5e30-4b5d-b2eb-274c48f68168"
	var reject atomic.Bool
	reject.Store(true)
	var lock sync.Mutex
	ledger := map[string]map[int][]int64{}
	panelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/server/UniProxy/user":
			fmt.Fprintf(w, `{"users":[{"id":42,"uuid":%q}]}`, userUUID)
		case "/api/v1/server/UniProxy/alivelist":
			w.Write([]byte(`{"alive":{}}`))
		case "/api/v2/server/config":
			w.WriteHeader(http.StatusNotModified)
		case "/api/v1/server/UniProxy/push":
			var payload map[int][]int64
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			id := r.Header.Get("X-Traffic-Batch-ID")
			if id == "" {
				t.Error("missing durable batch ID")
				w.WriteHeader(400)
				return
			}
			lock.Lock()
			if previous, ok := ledger[id]; ok && !reflect.DeepEqual(previous, payload) {
				t.Error("replayed batch changed payload")
			}
			ledger[id] = payload
			lock.Unlock()
			if reject.Load() {
				w.WriteHeader(503)
				w.Write([]byte(`{"message":"ack lost"}`))
				return
			}
			w.Write([]byte(`{"data":true}`))
		default:
			w.Write([]byte(`{"data":true}`))
		}
	}))
	defer panelServer.Close()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		connection, err := echo.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		io.Copy(connection, connection)
	}()
	portReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portReservation.Addr().(*net.TCPAddr).Port
	portReservation.Close()
	retries := 0
	cfg := conf.NodeConfig{APIHost: panelServer.URL, Key: "pn_host_test", NodeID: 7, RetryCount: &retries, TrafficDirectory: t.TempDir()}
	client, err := panel.New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	info := &panel.NodeInfo{Id: 7, Type: "vless", Tag: "traffic-shutdown", PullInterval: time.Hour, PushInterval: time.Hour, Common: &panel.CommonNode{Protocol: "vless", ListenIP: "127.0.0.1", ServerPort: port, Network: "tcp", NetworkSettings: json.RawMessage(`{}`)}}
	testOutbound := `{"tag":"test-loopback","protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}}`
	info.Common.Routes = []panel.Route{{Action: "default_out", ActionValue: &testOutbound}}
	limiter.Init()
	coreConfig := conf.New()
	coreConfig.NodeConfigs = []conf.NodeConfig{cfg}
	x := core.New(coreConfig)
	x.ReloadCh = make(chan struct{}, 1)
	if err := x.Start([]*panel.NodeInfo{info}); err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	controller := NewController(client, &cfg, info)
	defer controller.Close()
	if err := controller.Start(x); err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(5 * time.Second))
	uuid, err := hex.DecodeString(strings.ReplaceAll(userUUID, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	targetPort := echo.Addr().(*net.TCPAddr).Port
	header := append([]byte{0}, uuid...)
	header = append(header, 0, 1, byte(targetPort>>8), byte(targetPort), 1, 127, 0, 0, 1)
	first := bytes.Repeat([]byte("traffic-"), 1024)
	if _, err := connection.Write(append(header, first...)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2+len(first))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response[:2], []byte{0, 0}) || !bytes.Equal(response[2:], first) {
		t.Fatal("real VLESS tunnel did not echo payload")
	}
	if _, err := controller.flushTraffic(context.Background(), 0, false); err == nil {
		t.Fatal("failed traffic acknowledgement accepted")
	}
	final := []byte("small-final")
	if _, err := connection.Write(final); err != nil {
		t.Fatal(err)
	}
	echoed := make([]byte, len(final))
	if _, err := io.ReadFull(connection, echoed); err != nil || !bytes.Equal(echoed, final) {
		t.Fatalf("final traffic echo: %v", err)
	}
	if _, err := controller.flushTraffic(context.Background(), 1000, false); err == nil {
		t.Fatal("pending failed batch unexpectedly drained")
	}
	if err := controller.Close(); err == nil {
		t.Fatal("shutdown did not surface unavailable acknowledgement")
	}
	connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := connection.Read(make([]byte, 1)); err == nil {
		t.Fatal("shutdown left an active data link")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("shutdown did not close active data link")
	}
	select {
	case <-echoDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown retained upstream data connection")
	}
	reject.Store(false)
	restarted, err := trafficqueue.Open(cfg.TrafficDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Drain(context.Background(), func(ctx context.Context, rows []panel.UserTraffic, id string) error {
		return client.ReportUserTraffic(ctx, rows, id)
	}); err != nil {
		t.Fatal(err)
	}
	lock.Lock()
	defer lock.Unlock()
	var upload, download int64
	for _, payload := range ledger {
		upload += payload[42][0]
		download += payload[42][1]
	}
	expected := int64(len(first) + len(final))
	if len(ledger) != 2 || upload != expected || download != expected {
		t.Fatalf("restart accounting: batches=%d upload=%d download=%d expected=%d", len(ledger), upload, download, expected)
	}
}
