package dispatcher

import (
	"context"
	"sync"
	"testing"

	"github.com/wyx2685/v2node/common/audit"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/outbound"
)

func TestAuditActualIPPreservesDomainAndDeduplicates(t *testing.T) {
	var mu sync.Mutex
	var records []audit.Record
	d := &DefaultDispatcher{Audit: func(tag, email string, r audit.Record) {
		if tag != "node-3" || email != "user-tag" {
			t.Error("incorrect identity")
		}
		mu.Lock()
		defer mu.Unlock()
		records = append(records, r)
	}}
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "node-3", User: &protocol.MemoryUser{Email: "user-tag"}})
	original := net.TCPDestination(net.ParseAddress("example.com"), 443)
	ctx = d.withAudit(ctx, original, "example.com")
	if len(records) != 0 {
		t.Fatal("must wait for actual egress")
	}
	event := session.EgressEvent{Destination: net.TCPDestination(net.ParseAddress("203.0.113.8"), 443), Requested: original, Outcome: "allowed"}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); session.ReportEgress(ctx, event) }()
	}
	wg.Wait()
	finishAudit(ctx)
	if len(records) != 1 {
		t.Fatalf("want one actual connection, got %d", len(records))
	}
	r := records[0]
	if r.Destination != "example.com" || r.Domain != "example.com" || r.DestinationIP != "203.0.113.8" || r.Outcome != "allowed" || r.Port != 443 || r.Network != "tcp" {
		t.Fatal(r)
	}
}

func TestAuditBlockedUDPAndUnknownFallback(t *testing.T) {
	var records []audit.Record
	d := &DefaultDispatcher{Audit: func(_, _ string, r audit.Record) { records = append(records, r) }}
	base := session.ContextWithInbound(context.Background(), &session.Inbound{User: &protocol.MemoryUser{Email: "user"}})
	original := net.UDPDestination(net.ParseAddress("dns.example"), 53)
	ctx := d.withAudit(base, original, "")
	requested := net.UDPDestination(net.ParseAddress("blocked.example"), 443)
	blocked := net.UDPDestination(net.ParseAddress("192.0.2.8"), 443)
	session.ReportEgress(ctx, session.EgressEvent{Destination: blocked, Requested: requested, Outcome: "blocked"})
	finishAudit(ctx)
	r := records[0]
	if r.Destination != "blocked.example" || r.Domain != "blocked.example" || r.DestinationIP != "192.0.2.8" || r.Port != 443 || r.Outcome != "blocked" {
		t.Fatal(r)
	}
	ctx = d.withAudit(base, original, "")
	finishAudit(ctx)
	if len(records) != 2 || records[1].Outcome != "unknown" || records[1].DestinationIP != "" {
		t.Fatal(records)
	}
	ctx = d.withAudit(context.Background(), original, "")
	session.ReportEgress(ctx, session.EgressEvent{Destination: blocked, Outcome: "allowed"})
	finishAudit(ctx)
	if len(records) != 2 {
		t.Fatal("unauthenticated audit")
	}
}

// A proxy's own dialer may itself use freedom; its peer is not the user's target.
type auditProxyHandler struct{ outbound.Handler }

func (auditProxyHandler) ProxySettings() *serial.TypedMessage {
	return &serial.TypedMessage{Type: "xray.proxy.socks.ClientConfig"}
}
func TestAuditDoesNotAttributeProxyPeerToRequestedDomain(t *testing.T) {
	var records []audit.Record
	d := &DefaultDispatcher{Audit: func(_, _ string, r audit.Record) { records = append(records, r) }}
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: &protocol.MemoryUser{Email: "user"}})
	target := net.TCPDestination(net.ParseAddress("request.example"), 443)
	ctx = d.withAudit(ctx, target, "")
	child := auditOutbound(ctx, auditProxyHandler{}, target)
	session.ReportEgress(child, session.EgressEvent{Destination: net.TCPDestination(net.ParseAddress("203.0.113.8"), 1080), Outcome: "allowed"})
	finishAudit(ctx)
	if len(records) != 1 || records[0].Outcome != "unknown" || records[0].DestinationIP != "" {
		t.Fatal(records)
	}
}
