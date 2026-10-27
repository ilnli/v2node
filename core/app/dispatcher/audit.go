package dispatcher

import (
	"context"
	"time"

	"github.com/wyx2685/v2node/common/audit"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
)

// recordAudit runs after existing sniffing, before routing; it records an attempt,
// not a confirmed connection. The callback only enqueues bounded metadata.
func (d *DefaultDispatcher) recordAudit(ctx context.Context, original net.Destination, domain string, when time.Time) {
	if d.Audit == nil {
		return
	}
	inbound := session.InboundFromContext(ctx)
	if inbound == nil || inbound.User == nil || inbound.User.Email == "" {
		return
	}
	if domain == "" && original.Address.Family().IsDomain() {
		domain = original.Address.String()
	}
	network := "tcp"
	switch original.Network {
	case net.Network_TCP:
	case net.Network_UDP:
		network = "udp"
	default:
		return
	}
	d.Audit(inbound.Tag, inbound.User.Email, audit.Record{
		Timestamp: when.UnixMilli(), Destination: original.Address.String(), Port: uint16(original.Port), Network: network, Domain: domain,
	})
}
