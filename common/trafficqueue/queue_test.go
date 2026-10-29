package trafficqueue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/conf"
)

func TestPendingTrafficSurvivesRestartAndRetriesSameAcknowledgedBatch(t *testing.T) {
	accepted := map[string]map[int][]int64{}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		id := r.Header.Get("X-Traffic-Batch-ID")
		if id == "" {
			t.Error("missing batch ID")
			w.WriteHeader(400)
			return
		}
		var payload map[int][]int64
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if previous, ok := accepted[id]; ok && !reflect.DeepEqual(previous, payload) {
			t.Error("retry changed batch payload")
			w.WriteHeader(409)
			return
		}
		accepted[id] = payload
		// Simulate the durable server commit completing, followed by a lost ACK.
		if requests == 1 {
			http.Error(w, "response lost after commit", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":true}`))
	}))
	defer server.Close()
	retries := 0
	client, err := panel.New(&conf.NodeConfig{APIHost: server.URL, NodeID: 7, Key: "pn_host_test", RetryCount: &retries})
	if err != nil {
		t.Fatal(err)
	}
	send := func(ctx context.Context, traffic []panel.UserTraffic, id string) error {
		return client.ReportUserTraffic(ctx, traffic, id)
	}
	directory := t.TempDir()
	queue, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Append([]panel.UserTraffic{{UID: 42, Upload: 123, Download: 456}}); err != nil {
		t.Fatal(err)
	}
	if err := queue.Drain(context.Background(), send); err == nil {
		t.Fatal("unacknowledged batch removed")
	}
	restarted, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Drain(context.Background(), send); err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 1 || requests != 2 {
		t.Fatalf("retry double-billed: unique=%d requests=%d", len(accepted), requests)
	}
	for _, payload := range accepted {
		if !reflect.DeepEqual(payload, map[int][]int64{42: {123, 456}}) {
			t.Fatalf("persisted traffic=%v", payload)
		}
	}
	empty, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := empty.Drain(context.Background(), send); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatal("acknowledged traffic replayed after restart")
	}
	stat, err := os.Stat(filepath.Join(directory, "pending.json"))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("queue privacy: stat=%v err=%v", stat, err)
	}
}

func TestFalseAcknowledgementRetainsBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":false}`))
	}))
	defer server.Close()
	client, err := panel.New(&conf.NodeConfig{APIHost: server.URL, NodeID: 7, Key: "pn_host_test"})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	queue, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Append([]panel.UserTraffic{{UID: 42, Upload: 3}}); err != nil {
		t.Fatal(err)
	}
	if err := queue.Drain(context.Background(), func(ctx context.Context, rows []panel.UserTraffic, id string) error {
		return client.ReportUserTraffic(ctx, rows, id)
	}); err == nil {
		t.Fatal("false acknowledgement accepted")
	}
	loaded, err := Open(directory)
	if err != nil || len(loaded.batches) != 1 {
		t.Fatalf("failed ACK lost batch: %v", err)
	}
}

func TestStorageFailureRetainsSnapshotForRetry(t *testing.T) {
	var received map[int][]int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":true}`))
	}))
	defer server.Close()
	client, err := panel.New(&conf.NodeConfig{APIHost: server.URL, NodeID: 7, Key: "pn_host_test"})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	queue, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(directory, "pending.json")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := queue.Append([]panel.UserTraffic{{UID: 42, Upload: 11, Download: 22}}); err == nil {
		t.Fatal("failed disk write accepted")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := queue.Drain(context.Background(), func(ctx context.Context, rows []panel.UserTraffic, id string) error {
		return client.ReportUserTraffic(ctx, rows, id)
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(received, map[int][]int64{42: {11, 22}}) {
		t.Fatalf("snapshot lost after storage failure: %v", received)
	}
}
