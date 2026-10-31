package dispatcher

import (
	"context"
	"sync"
	"time"

	"github.com/wyx2685/v2node/common/audit"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/outbound"
)

type auditContextKey struct{}
type auditSession struct {
	sync.Mutex
	seen     [64]string
	next     int
	recorded bool
}

// withAudit observes real egress events after sniffing. A small ring suppresses
// repeated UDP packets without retaining an unbounded per-connection target map.
func (d *DefaultDispatcher) withAudit(ctx context.Context, original net.Destination, domain string) context.Context {
	if d.Audit == nil {
		return ctx
	}
	inbound := session.InboundFromContext(ctx)
	if inbound == nil || inbound.User == nil || inbound.User.Email == "" {
		return ctx
	}
	if domain == "" && original.Address.Family().IsDomain() {
		domain = original.Address.String()
	}
	state := &auditSession{}
	ctx = context.WithValue(ctx, auditContextKey{}, state)
	return session.ContextWithEgressObserver(ctx, func(event session.EgressEvent) {
		target := original
		eventDomain := domain
		// XUDP can carry multiple requested destinations over one dispatched link.
		if event.Requested.IsValid() && original.Network == net.Network_UDP {
			target = event.Requested
			if target.Address.Family().IsDomain() {
				eventDomain = target.Address.String()
			} else if target != original {
				eventDomain = ""
			}
		}
		network := "tcp"
		if target.Network == net.Network_UDP {
			network = "udp"
		} else if target.Network != net.Network_TCP {
			return
		}
		ip := ""
		if event.Destination.IsValid() && event.Destination.Address.Family().IsIP() {
			ip = event.Destination.Address.String()
		}
		key := target.String() + "|" + ip + "|" + event.Outcome
		state.Lock()
		for _, previous := range state.seen {
			if previous == key {
				state.Unlock()
				return
			}
		}
		state.seen[state.next] = key
		state.next = (state.next + 1) % len(state.seen)
		state.recorded = true
		state.Unlock()
		d.Audit(inbound.Tag, inbound.User.Email, audit.Record{
			Timestamp: time.Now().UnixMilli(), Destination: target.Address.String(), Port: uint16(target.Port), Network: network, Domain: eventDomain,
			DestinationIP: ip, Outcome: event.Outcome,
		})
	})
}

// finishAudit preserves attempts from outbounds that do not expose final egress
// metadata. Unknown means no claim about connection success or final IP.
func finishAudit(ctx context.Context) {
	state, _ := ctx.Value(auditContextKey{}).(*auditSession)
	if state == nil {
		return
	}
	state.Lock()
	recorded := state.recorded
	state.Unlock()
	if !recorded {
		session.ReportEgress(ctx, session.EgressEvent{Outcome: "unknown"})
	}
}

func auditOutbound(ctx context.Context, handler outbound.Handler, destination net.Destination) context.Context {
	if ctx.Value(auditContextKey{}) == nil {
		return ctx
	}
	settings := handler.ProxySettings()
	if settings == nil {
		return ctx
	}
	switch settings.Type {
	case "xray.proxy.blackhole.Config":
		// A domain denied before dialing has no actual connection IP. Do not
		// misrepresent one of its DNS candidates as an established connection.
		session.ReportEgress(ctx, session.EgressEvent{Destination: destination, Requested: destination, Outcome: "blocked"})
	case "xray.proxy.freedom.Config":
		// Direct egress reports from the actual dial / packet write boundary.
	default:
		session.ReportEgress(ctx, session.EgressEvent{Outcome: "unknown"})
		return session.ContextWithEgressObserver(ctx, nil)
	}
	return ctx
}
