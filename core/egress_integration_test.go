package core

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/common/audit"
	"github.com/wyx2685/v2node/common/format"
	"github.com/wyx2685/v2node/conf"
	"github.com/wyx2685/v2node/limiter"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	xcore "github.com/xtls/xray-core/core"
)

// Real loopback traffic exercises dispatch -> routing -> freedom -> audit.
// 127.0.0.2 stands in for a blocked country; synthetic geodata is covered by
// TestCNBlockUsesDestinationAndPrecedesForwarding.
func TestDestinationBlockAndActualEgressAudit(t *testing.T) {
	direct := `{"protocol":"direct","tag":"Default","settings":{"domainStrategy":"UseIPv4","finalRules":[{"action":"allow","ip":["127.0.0.0/8"]}]}}`
	info := &panel.NodeInfo{Tag: "audit-egress", Common: &panel.CommonNode{Routes: []panel.Route{
		{Action: "default_out", ActionValue: &direct}, {Action: "block_ip", Match: []string{"127.0.0.2/32"}},
	}}}
	limiter.Init()
	user := panel.UserInfo{Id: 7, Uuid: "test-user"}
	limiter.AddLimiter("vless", info.Tag, []panel.UserInfo{user}, nil)
	defer limiter.DeleteLimiter(info.Tag)
	instance := New(conf.New())
	if err := instance.Start([]*panel.NodeInfo{info}); err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	events := make(chan audit.Record, 16)
	instance.dispatcher.Audit = func(_, _ string, r audit.Record) { events <- r }
	newContext := func() context.Context {
		return session.ContextWithInbound(context.Background(), &session.Inbound{
			Tag: info.Tag, Name: "vless", User: &protocol.MemoryUser{Email: format.UserTag(info.Tag, user.Uuid)},
			Source: xnet.TCPDestination(xnet.ParseAddress("192.0.2.9"), 12345),
		})
	}
	next := func() audit.Record {
		select {
		case r := <-events:
			return r
		case <-time.After(3 * time.Second):
			t.Fatal("missing egress audit")
			return audit.Record{}
		}
	}
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, err := echo.Accept()
		if err == nil {
			defer c.Close()
			io.Copy(c, c)
		}
	}()
	port := xnet.Port(echo.Addr().(*net.TCPAddr).Port)
	conn, err := xcore.Dial(newContext(), instance.Server, xnet.TCPDestination(xnet.ParseAddress("localhost"), port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("echo")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil || string(response) != "echo" {
		t.Fatalf("echo %q: %v", response, err)
	}
	record := next()
	if record.Destination != "localhost" || record.Domain != "localhost" || record.DestinationIP != "127.0.0.1" || record.Outcome != "allowed" {
		t.Fatal(record)
	}
	conn.Close()
	denied, err := xcore.Dial(newContext(), instance.Server, xnet.TCPDestination(xnet.ParseAddress("127.0.0.2"), port))
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Close()
	denied.SetDeadline(time.Now().Add(3 * time.Second))
	denied.Write([]byte("blocked"))
	if _, err := denied.Read(make([]byte, 1)); err == nil {
		t.Fatal("blocked TCP returned data")
	}
	record = next()
	if record.DestinationIP != "127.0.0.2" || record.Outcome != "blocked" {
		t.Fatal(record)
	}

	allowedUDP, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer allowedUDP.Close()
	udpPort := allowedUDP.LocalAddr().(*net.UDPAddr).Port
	blockedUDP, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.2", strconv.Itoa(udpPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer blockedUDP.Close()
	target := xnet.UDPDestination(xnet.ParseAddress("127.0.0.1"), xnet.Port(udpPort))
	link, err := instance.dispatcher.Dispatch(newContext(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer common.Interrupt(link.Reader)
	defer common.Close(link.Writer)
	for _, ip := range []string{"127.0.0.1", "127.0.0.2", "127.0.0.1"} {
		b := buf.New()
		b.Write([]byte("packet"))
		dest := xnet.UDPDestination(xnet.ParseAddress(ip), xnet.Port(udpPort))
		b.UDP = &dest
		if err := link.Writer.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
			t.Fatal(err)
		}
	}
	allowedUDP.SetReadDeadline(time.Now().Add(3 * time.Second))
	for range 2 {
		if n, _, err := allowedUDP.ReadFrom(make([]byte, 64)); err != nil || n != 6 {
			t.Fatalf("allowed UDP n=%d err=%v", n, err)
		}
	}
	blockedUDP.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err := blockedUDP.ReadFrom(make([]byte, 64)); err == nil {
		t.Fatal("later UDP target bypassed deny")
	}
	first, second := next(), next()
	if first.Outcome != "allowed" || first.DestinationIP != "127.0.0.1" || second.Outcome != "blocked" || second.DestinationIP != "127.0.0.2" {
		t.Fatalf("UDP audits: %+v %+v", first, second)
	}
	select {
	case r := <-events:
		t.Fatalf("duplicate packet audit: %+v", r)
	default:
	}
}
