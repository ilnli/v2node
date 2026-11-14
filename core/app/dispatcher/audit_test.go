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
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
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
	child := auditOutbound(ctx, auditProxyHandler{}, target, "")
	session.ReportEgress(child, session.EgressEvent{Destination: net.TCPDestination(net.ParseAddress("203.0.113.8"), 1080), Outcome: "allowed"})
	finishAudit(ctx)
	if len(records) != 1 || records[0].Outcome != "unknown" || records[0].DestinationIP != "" {
		t.Fatal(records)
	}
}

type auditRoute struct {
	routing.Context
	tag string
}

func (auditRoute) GetOutboundTag() string         { return "test-outbound" }
func (r auditRoute) GetRuleTag() string           { return r.tag }
func (auditRoute) GetOutboundGroupTags() []string { return nil }

type auditRouter struct {
	routing.Router
	tag string
}

func (r auditRouter) PickRoute(ctx routing.Context) (routing.Route, error) {
	return auditRoute{Context: ctx, tag: r.tag}, nil
}

type auditHandler struct {
	outbound.Handler
	kind   string
	events []session.EgressEvent
}

func (h auditHandler) ProxySettings() *serial.TypedMessage {
	return &serial.TypedMessage{Type: h.kind}
}
func (auditHandler) Tag() string { return "test-outbound" }
func (h auditHandler) Dispatch(ctx context.Context, _ *transport.Link) {
	for _, event := range h.events {
		session.ReportEgress(ctx, event)
	}
}

type auditOutboundManager struct {
	outbound.Manager
	handler outbound.Handler
}

func (m auditOutboundManager) GetHandler(string) outbound.Handler { return m.handler }

func TestAuditSelectedRouteAndFinalEgressReasons(t *testing.T) {
	target := net.TCPDestination(net.ParseAddress("blocked.example"), 443)
	ip := net.TCPDestination(net.ParseAddress("192.0.2.8"), 443)
	for _, tc := range []struct {
		name, tag, kind string
		events          []session.EgressEvent
		want            []string
	}{
		{"matched rule", "audit-block/bm9kZQ/0/IP 拦截规则 #1：geoip:cn", "xray.proxy.blackhole.Config", nil, []string{"IP 拦截规则 #1：geoip:cn"}},
		{"untagged blackhole", "", "xray.proxy.blackhole.Config", nil, []string{"黑洞出口拦截（未提供路由规则）"}},
		{"final egress", "forwarding-rule", "xray.proxy.freedom.Config", []session.EgressEvent{
			{Destination: ip, Requested: target, Outcome: "allowed"},
			{Destination: ip, Requested: target, Outcome: "blocked"},
			{Destination: ip, Requested: target, Outcome: "failed"},
		}, []string{"", "最终出口 IP/端口安全检查拦截", ""}},
		{"unobserved", "", "xray.proxy.freedom.Config", nil, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var records []audit.Record
			d := &DefaultDispatcher{
				Audit:  func(_, _ string, r audit.Record) { records = append(records, r) },
				router: auditRouter{tag: tc.tag},
				ohm:    auditOutboundManager{handler: auditHandler{kind: tc.kind, events: tc.events}},
			}
			ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: &protocol.MemoryUser{Email: "test-user"}})
			ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: target}})
			ctx = d.withAudit(ctx, target, "")
			d.routedDispatch(ctx, &transport.Link{}, target)
			if len(records) != len(tc.want) {
				t.Fatalf("audit record count %d, want %d", len(records), len(tc.want))
			}
			for i, reason := range tc.want {
				if records[i].BlockReason != reason {
					t.Fatalf("audit %d reason=%q, want %q", i, records[i].BlockReason, reason)
				}
			}
			if tc.kind == "xray.proxy.blackhole.Config" && records[0].DestinationIP != "" {
				t.Fatal("route reason must not invent an actual IP for an unresolved domain")
			}
		})
	}
}
