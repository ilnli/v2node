package managed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wyx2685/v2node/conf"
)

func TestAgentCanEnrollAndRemainOnlineWithoutNodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	reported := make(chan map[string]any, 1)
	var enrolledKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/agent/enroll":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			enrolledKey, _ = body["agent_key"].(string)
			if body["enrollment_token"] != "pn_enroll_fixture" {
				t.Error("bad enrollment")
			}
			w.Write([]byte(`{"data":{"host_id":2}}`))
		case "/api/v2/agent/desired":
			if r.Header.Get("Authorization") != "Bearer "+enrolledKey {
				t.Error("wrong scoped credential")
			}
			w.Write([]byte(`{"data":{"host_id":2,"revision":"empty","enabled":true,"nodes":[],"poll_interval":1,"audit":{"enabled":false,"memory_mib":64,"disk_mib":1024}}}`))
		case "/api/v2/agent/report":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			reported <- body
			w.Write([]byte(`{"data":true}`))
			cancel()
		default:
			t.Error("unknown path", r.URL.Path)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	err := Run(ctx, &conf.AgentConfig{APIHost: server.URL, HostID: 2, EnrollmentToken: "pn_enroll_fixture", Directory: dir}, "fixture-version")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case report := <-reported:
		if report["applied_revision"] != "empty" || report["state"] != "idle" {
			t.Fatalf("no-config host was not ready for management: %+v", report)
		}
	default:
		t.Fatal("host did not report its state")
	}
	identity, err := readIdentity(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !identity.Enrolled || identity.Key != enrolledKey {
		t.Fatal("enrollment was not durable")
	}
	stat, _ := os.Stat(filepath.Join(dir, "identity.json"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal("credential file not private")
	}
}
