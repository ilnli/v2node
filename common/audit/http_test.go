package audit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedHostAuditKeyIsNotAQueryParameter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pn_host_test" || r.URL.Query().Has("token") {
			t.Error("audit must use the scoped host bearer without putting it in the URL")
		}
		w.Write([]byte(`{"data":{"batch_id":"test-batch","accepted":1}}`))
	}))
	defer server.Close()
	endpoint, err := NewEndpoint(server.URL, 3, "pn_host_test")
	if err != nil {
		t.Fatal(err)
	}
	if err := endpoint.Send(context.Background(), sampleBatch("test-batch")); err != nil {
		t.Fatal(err)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPAuditAuthenticationAndAcknowledgment(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"success", `{"data":{"batch_id":"test-batch","accepted":1}}`, 200, true},
		{"wrong-id", `{"data":{"batch_id":"other","accepted":1}}`, 200, false},
		{"partial", `{"data":{"batch_id":"test-batch","accepted":0}}`, 200, false},
		{"legacy", `{"data":true}`, 200, false},
		{"missing", `{}`, 200, false},
		{"server-error", `{"data":{"batch_id":"test-batch","accepted":1}}`, 500, false},
		{"too-large", strings.Repeat(" ", 8193), 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint, err := NewEndpoint("https://panel.example/prefix", 3, "test-secret")
			if err != nil {
				t.Fatal(err)
			}
			endpoint.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/prefix/api/v1/server/UniProxy/audit" || r.URL.Query().Get("token") != "test-secret" || r.URL.Query().Get("node_id") != "3" || r.URL.Query().Get("node_type") != "v2node" {
					t.Fatal("incorrect endpoint or auth", r.URL.Path)
				}
				var b Batch
				if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
					t.Fatal(err)
				}
				if b.ID != "test-batch" || b.Records[0].UserID != 7 {
					t.Fatal("wrong report")
				}
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			err = endpoint.Send(context.Background(), sampleBatch("test-batch"))
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected response acceptance: %v", err)
			}
		})
	}
}
