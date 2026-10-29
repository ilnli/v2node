package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wyx2685/v2node/conf"
)

func TestManagedNodeKeyUsesHeaderAndEmptyUsersAreAnUpdate(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer pn_host_test-secret" || r.URL.Query().Get("token") != "" {
			t.Error("managed credential must be sent only in the Authorization header")
		}
		if r.URL.Query().Get("node_id") != "7" {
			t.Error("missing node scope")
		}
		if requests == 2 {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"users":[]}`))
	}))
	defer server.Close()
	client, err := New(&conf.NodeConfig{APIHost: server.URL, NodeID: 7, Key: "pn_host_test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	users, err := client.GetUserList(context.Background())
	if err != nil || users == nil || len(users) != 0 {
		t.Fatalf("empty full snapshot must remove old users: users=%v err=%v", users, err)
	}
	users, err = client.GetUserList(context.Background())
	if err != nil || users != nil {
		t.Fatalf("304 must mean no update: %v %v", users, err)
	}
}

func TestNodeConfigRejectsFailedHTTPBeforeParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		// A syntactically valid body must not turn revoked authentication into configuration.
		w.Write([]byte(`{"protocol":"vless","server_port":443,"base_config":{"pull_interval":60,"push_interval":60}}`))
	}))
	defer server.Close()
	client, _ := New(&conf.NodeConfig{APIHost: server.URL, NodeID: 7, Key: "pn_host_test-secret"})
	if _, err := client.GetNodeInfo(context.Background()); err == nil {
		t.Fatal("accepted a denied node configuration")
	}
}
