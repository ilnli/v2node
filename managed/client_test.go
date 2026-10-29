package managed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentEnrollmentAndManifestUseDistinctCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/agent/enroll":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["agent_key"] != "pn_host_fixture" || body["enrollment_token"] != "pn_enroll_fixture" {
				t.Error("missing enrollment binding")
			}
			w.Write([]byte(`{"data":{"host_id":9}}`))
		case "/api/v2/agent/desired":
			if r.Header.Get("Authorization") != "Bearer pn_host_fixture" || r.URL.RawQuery != "" {
				t.Error("host credential missing or leaked to query")
			}
			w.Write([]byte(`{"data":{"host_id":9,"revision":"revision-1","enabled":true,"poll_interval":15,"nodes":[],"audit":{"enabled":false,"memory_mib":64,"disk_mib":1024}}}`))
		default:
			t.Error("unexpected path", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, 9, "pn_host_fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Enroll(context.Background(), "pn_enroll_fixture", "test-version"); err != nil {
		t.Fatal(err)
	}
	manifest, err := client.Desired(context.Background())
	if err != nil || manifest.HostID != 9 || manifest.Revision != "revision-1" {
		t.Fatalf("manifest: %+v %v", manifest, err)
	}
}

func TestAgentRejectsRedirectsAndRedactsTransportErrors(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed credential redirect") }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	client, _ := NewClient(server.URL, 1, "pn_host_secret-value")
	_, err := client.Desired(context.Background())
	if err == nil {
		t.Fatal("accepted redirect")
	}
	server.Close()
	_, err = client.Desired(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("unsafe error: %v", err)
	}
}
