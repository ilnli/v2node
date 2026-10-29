package node

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/conf"
	"github.com/wyx2685/v2node/core"
	"github.com/wyx2685/v2node/limiter"
)

func TestEmptyNodeStartsAndRemovedUsersAreApplied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/server/UniProxy/user":
			w.Write([]byte(`{"users":[]}`))
		case "/api/v1/server/UniProxy/alivelist":
			w.Write([]byte(`{"alive":{}}`))
		case "/api/v2/server/config":
			w.WriteHeader(http.StatusNotModified)
		default:
			w.Write([]byte(`{"data":true}`))
		}
	}))
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	cfg := conf.NodeConfig{APIHost: server.URL, Key: "pn_host_test", NodeID: 7}
	client, _ := panel.New(&cfg)
	info := &panel.NodeInfo{Id: 7, Type: "vless", Tag: "test-empty-node", PullInterval: time.Hour, PushInterval: time.Hour,
		Common: &panel.CommonNode{Protocol: "vless", ListenIP: "127.0.0.1", ServerPort: port, Network: "tcp", NetworkSettings: json.RawMessage(`{}`)}}
	limiter.Init()
	config := conf.New()
	config.NodeConfigs = []conf.NodeConfig{cfg}
	x := core.New(config)
	x.ReloadCh = make(chan struct{}, 1)
	if err := x.Start([]*panel.NodeInfo{info}); err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	c := NewController(client, &cfg, info)
	defer c.Close()
	if err := c.Start(x); err != nil {
		t.Fatalf("new node without subscribers must still run: %v", err)
	}
	user := panel.UserInfo{Id: 42, Uuid: "2d795b6f-5e30-4b5d-b2eb-274c48f68168"}
	if _, err := x.AddUsers(&core.AddUsersParams{Tag: info.Tag, Users: []panel.UserInfo{user}, NodeInfo: info}); err != nil {
		t.Fatal(err)
	}
	c.userList = []panel.UserInfo{user}
	if err := c.nodeInfoMonitor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.userList) != 0 {
		t.Fatal("empty panel snapshot retained a removed user's access")
	}
}
