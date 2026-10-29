package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wyx2685/v2node/conf"
)

func TestTrafficAndOnlineReportsRejectHTTPFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not accepted", http.StatusForbidden) }))
	defer server.Close()
	retries := 0
	client, err := New(&conf.NodeConfig{APIHost: server.URL, NodeID: 7, Key: "pn_host_test", RetryCount: &retries})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ReportUserTraffic(context.Background(), []UserTraffic{{UID: 42, Upload: 10, Download: 20}}); err == nil {
		t.Fatal("rejected traffic reported as accepted")
	}
	if err := client.ReportNodeOnlineUsers(context.Background(), &map[int][]string{42: {"127.0.0.1"}}); err == nil {
		t.Fatal("rejected online report reported as accepted")
	}
}
