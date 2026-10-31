package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/geodata"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/dns"
	routingSession "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/proxy/freedom"
	"google.golang.org/protobuf/proto"
)

// Documentation-only address ranges stand in for CN; no public services are contacted.
func syntheticCN(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", directory)
	data, err := proto.Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{{Code: "CN", Cidr: []*geodata.CIDR{
		{Ip: []byte{192, 0, 2, 0}, Prefix: 24},
		{Ip: xnet.ParseAddress("2001:db8:1::").IP(), Prefix: 48},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "geoip.dat"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

type fixedDNS struct{ dns.Client }

func (*fixedDNS) LookupIP(domain string, _ dns.IPOption) ([]xnet.IP, uint32, error) {
	if domain == "cn.example" {
		return []xnet.IP{xnet.ParseAddress("192.0.2.8").IP()}, 60, nil
	}
	if domain == "cn6.example" {
		return []xnet.IP{xnet.ParseAddress("2001:db8:1::8").IP()}, 60, nil
	}
	return []xnet.IP{xnet.ParseAddress("198.51.100.8").IP()}, 60, nil
}

func TestCNBlockUsesDestinationAndPrecedesForwarding(t *testing.T) {
	syntheticCN(t)
	direct := `{"protocol":"freedom","tag":"custom-direct","settings":{}}`
	node := &panel.NodeInfo{Tag: "protected", Common: &panel.CommonNode{Routes: []panel.Route{
		{Action: "default_out", ActionValue: &direct},
		{Action: "block_ip", Match: []string{"geoip:cn"}},
	}}}
	_, outbounds, config, err := GetCustomConfig([]*panel.NodeInfo{node, {Tag: "unrestricted", Common: &panel.CommonNode{}}})
	if err != nil {
		t.Fatal(err)
	}
	r := new(router.Router)
	if err := r.Init(context.Background(), config, &fixedDNS{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, tc := range []struct {
		name, inbound, source, destination string
		network                            xnet.Network
		port                               xnet.Port
		blocked                            bool
	}{
		{"cn-tcp", "protected", "198.51.100.2", "192.0.2.8", xnet.Network_TCP, 443, true},
		{"cn-udp", "protected", "198.51.100.2", "192.0.2.8", xnet.Network_UDP, 443, true},
		{"cn-dns", "protected", "198.51.100.2", "192.0.2.8", xnet.Network_UDP, 53, true},
		{"cn-ipv6", "protected", "198.51.100.2", "2001:db8:1::8", xnet.Network_TCP, 443, true},
		{"cn-domain", "protected", "198.51.100.2", "cn.example", xnet.Network_TCP, 443, true},
		{"cn-domain6", "protected", "198.51.100.2", "cn6.example", xnet.Network_UDP, 443, true},
		{"cn-source-allowed", "protected", "192.0.2.9", "198.51.100.8", xnet.Network_TCP, 443, false},
		{"foreign-domain", "protected", "192.0.2.9", "foreign.example", xnet.Network_UDP, 443, false},
		{"other-node", "unrestricted", "198.51.100.2", "192.0.2.8", xnet.Network_TCP, 443, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: tc.inbound, Source: xnet.TCPDestination(xnet.ParseAddress(tc.source), 12345)})
			ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: xnet.Destination{Address: xnet.ParseAddress(tc.destination), Network: tc.network, Port: tc.port}}})
			result, err := r.PickRoute(routingSession.AsRoutingContext(ctx))
			blocked := err == nil && result.GetOutboundTag() == "block"
			if blocked != tc.blocked {
				t.Fatalf("blocked=%v, want %v (route=%v, err=%v)", blocked, tc.blocked, result, err)
			}
			if tc.inbound == "protected" && !tc.blocked {
				if err != nil {
					t.Fatal(err)
				}
				for _, outbound := range outbounds {
					if outbound.Tag != result.GetOutboundTag() {
						continue
					}
					settings, err := outbound.ProxySettings.GetInstance()
					if err != nil {
						t.Fatal(err)
					}
					if len(settings.(*freedom.Config).FinalRules) == 0 {
						t.Fatal("direct outbound lacks final destination protection")
					}
				}
			}
		})
	}
}

func TestCNBlockDefaultEgressIsScopedToNode(t *testing.T) {
	syntheticCN(t)
	_, outbounds, config, err := GetCustomConfig([]*panel.NodeInfo{
		{Tag: "protected", Common: &panel.CommonNode{Routes: []panel.Route{{Action: "block_ip", Match: []string{"geoip:cn"}}}}},
		{Tag: "other", Common: &panel.CommonNode{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := new(router.Router)
	if err := r.Init(context.Background(), config, &fixedDNS{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{Tag: "protected"})
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{Target: xnet.TCPDestination(xnet.ParseAddress("198.51.100.8"), 443)}})
	route, err := r.PickRoute(routingSession.AsRoutingContext(ctx))
	if err != nil {
		t.Fatal("protected node needs its own guarded default outbound:", err)
	}
	if route.GetOutboundTag() == "Default" {
		t.Fatal("protected node uses shared unguarded default")
	}
	found := false
	for _, outbound := range outbounds {
		if outbound.Tag != route.GetOutboundTag() && outbound.Tag != "Default" {
			continue
		}
		settings, err := outbound.ProxySettings.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		guarded := len(settings.(*freedom.Config).FinalRules) > 0
		if guarded != (outbound.Tag != "Default") {
			t.Fatalf("outbound %s guarded=%v", outbound.Tag, guarded)
		}
		found = found || guarded
	}
	if !found {
		t.Fatal("guarded outbound missing")
	}
}
