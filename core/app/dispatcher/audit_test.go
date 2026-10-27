package dispatcher

import (
	"context"
	"testing"
	"time"

	"github.com/wyx2685/v2node/common/audit"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
)

func TestAuditPreservesOriginalDestinationAndSniffedDomain(t *testing.T) {
	var records []audit.Record
	d := &DefaultDispatcher{Audit: func(tag, email string, r audit.Record) {
		if tag != "node-3" || email != "user-tag" {
			t.Fatal("incorrect identity")
		}
		records = append(records, r)
	}}
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "node-3", User: &protocol.MemoryUser{Email: "user-tag"}})
	when := time.UnixMilli(1700000000123)
	d.recordAudit(ctx, net.TCPDestination(net.ParseAddress("203.0.113.8"), 443), "example.com", when)
	d.recordAudit(ctx, net.UDPDestination(net.ParseAddress("dns.example"), 53), "", when)
	d.recordAudit(context.Background(), net.TCPDestination(net.ParseAddress("203.0.113.8"), 443), "example.com", when)
	if len(records) != 2 {
		t.Fatalf("expected authenticated records only: %d", len(records))
	}
	if records[0].Destination != "203.0.113.8" || records[0].Domain != "example.com" || records[0].Timestamp != 1700000000123 || records[0].Network != "tcp" || records[0].Port != 443 {
		t.Fatal(records[0])
	}
	if records[1].Destination != "dns.example" || records[1].Domain != "dns.example" || records[1].Network != "udp" {
		t.Fatal(records[1])
	}
}
