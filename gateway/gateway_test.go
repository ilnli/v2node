package gateway

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wyx2685/v2node/conf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func testTLS(t *testing.T) (string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gateway.test"}, DNSNames: []string{"gateway.test", "other.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tls.pem")
	if err := os.WriteFile(path, append(cert, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})...), 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(cert)
	return path, roots
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func backendPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return number
}

func gatewayClient(t *testing.T, port int, roots *x509.CertPool) *http.Client {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, ForceAttemptHTTP2: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func TestGatewayRoutesTLSAndReplacesForwardedHeaders(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s|%s|%s|%s|%s", r.Method, r.URL.RequestURI(), r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-Host"), r.Header.Get("Forwarded"), r.Header.Get("X-Real-IP"))
	}))
	defer backend.Close()
	cert, roots := testTLS(t)
	port := freePort(t)
	manager, err := Start([]conf.GatewayConfig{{Port: port, Host: "gateway.test", Path: "/tunnel", BackendPort: backendPort(t, backend), Transport: "xhttp", Certificate: cert}, {Port: port, Host: "other.test", Path: "/other", BackendPort: backendPort(t, backend), Transport: "ws", Certificate: cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client := gatewayClient(t, port, roots)
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://gateway.test:%d/tunnel/session?part=2", port), strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Forwarded-For", "evil")
	request.Header.Set("X-Forwarded-Proto", "http")
	request.Header.Set("X-Forwarded-Host", "spoof.test")
	request.Header.Set("Forwarded", "for=evil")
	request.Header.Set("X-Real-IP", "evil")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	expected := fmt.Sprintf("POST|/tunnel/session?part=2|127.0.0.1|https|gateway.test:%d||", port)
	if response.StatusCode != http.StatusOK || string(data) != expected || response.ProtoMajor != 2 {
		t.Fatalf("TLS route: status=%d protocol=%s body=%q", response.StatusCode, response.Proto, string(data))
	}
	for _, tc := range []struct {
		host, path string
		status     int
	}{{"gateway.test", "/tunnel-other", 404}, {"other.test", "/tunnel", 404}, {"other.test", "/other", 200}} {
		response, err := client.Get(fmt.Sprintf("https://%s:%d%s", tc.host, port, tc.path))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s%s status=%d", tc.host, tc.path, response.StatusCode)
		}
	}
}

func TestGatewayStreamsBeforeBackendCloses(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Write([]byte("last"))
	}))
	defer backend.Close()
	cert, roots := testTLS(t)
	port := freePort(t)
	manager, err := Start([]conf.GatewayConfig{{Port: port, Host: "gateway.test", Path: "/stream", BackendPort: backendPort(t, backend), Transport: "xhttp", Certificate: cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	response, err := gatewayClient(t, port, roots).Get(fmt.Sprintf("https://gateway.test:%d/stream", port))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, 5)
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	if string(first) != "first" {
		t.Fatalf("stream=%q", first)
	}
}

func TestGatewayWebSocketUpgradeBidirectional(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer socket.Close()
		kind, data, err := socket.ReadMessage()
		if err != nil {
			return
		}
		_ = socket.WriteMessage(kind, append([]byte("echo:"), data...))
	}))
	defer backend.Close()
	cert, roots := testTLS(t)
	port := freePort(t)
	manager, err := Start([]conf.GatewayConfig{{Port: port, Host: "gateway.test", Path: "/ws", BackendPort: backendPort(t, backend), Transport: "ws", Certificate: cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "gateway.test"}, HandshakeTimeout: 5 * time.Second}
	socket, _, err := dialer.Dial(fmt.Sprintf("wss://127.0.0.1:%d/ws", port), http.Header{"Host": []string{"gateway.test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := socket.WriteMessage(websocket.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	kind, data, err := socket.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.BinaryMessage || string(data) != "echo:hello" {
		t.Fatalf("upgraded echo kind=%d body=%q", kind, data)
	}
}

func TestGatewayGRPCUsesH2CBackend(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	healthpb.RegisterHealthServer(grpcServer, health.NewServer())
	go grpcServer.Serve(backend)
	defer grpcServer.Stop()
	cert, roots := testTLS(t)
	port := freePort(t)
	manager, err := Start([]conf.GatewayConfig{{Port: port, Host: "gateway.test", Path: "/grpc.health.v1.Health", BackendPort: backend.Addr().(*net.TCPAddr).Port, Transport: "grpc", Certificate: cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := grpc.NewClient(fmt.Sprintf("passthrough:///gateway.test:%d", port), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: roots, ServerName: "gateway.test"})), grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := healthpb.NewHealthClient(client).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("gRPC response=%v", status)
	}
}

func TestGatewayRejectsAmbiguousRoutesAndRollsBackPorts(t *testing.T) {
	cert, _ := testTLS(t)
	base := conf.GatewayConfig{Port: freePort(t), Host: "gateway.test", Path: "/a", BackendPort: freePort(t), Transport: "ws", Certificate: cert}
	for _, path := range []string{"/a", "/a/child", "/"} {
		duplicate := base
		duplicate.Path = path
		if err := Validate([]conf.GatewayConfig{base, duplicate}); err == nil {
			t.Fatalf("ambiguous route %q accepted", path)
		}
	}
	for _, transport := range []string{"tcp", "quic", "hysteria2", "reality"} {
		invalid := base
		invalid.Transport = transport
		if err := Validate([]conf.GatewayConfig{invalid}); err == nil {
			t.Fatalf("unsupported transport %q accepted", transport)
		}
	}
	invalid := base
	invalid.Host = "missing.test"
	if err := Validate([]conf.GatewayConfig{invalid}); err == nil {
		t.Fatal("certificate hostname mismatch accepted")
	}
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	conflict := base
	conflict.Port = occupied.Addr().(*net.TCPAddr).Port
	if manager, err := Start([]conf.GatewayConfig{base, conflict}); err == nil {
		manager.Close()
		t.Fatal("occupied public port accepted")
	}
	reclaimed, err := net.Listen("tcp", fmt.Sprintf(":%d", base.Port))
	if err != nil {
		t.Fatalf("failed startup leaked earlier listener: %v", err)
	}
	reclaimed.Close()
}

func TestGatewayStreamsHTTP1ResponseWhileRequestIsOpen(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Error(err)
			return
		}
		first := make([]byte, 5)
		if _, err := io.ReadFull(r.Body, first); err != nil {
			return
		}
		w.Write(first)
		w.(http.Flusher).Flush()
		rest, _ := io.ReadAll(r.Body)
		w.Write(rest)
	}))
	defer backend.Close()
	cert, roots := testTLS(t)
	port := freePort(t)
	manager, err := Start([]conf.GatewayConfig{{Port: port, Host: "gateway.test", Path: "/stream", BackendPort: backendPort(t, backend), Transport: "xhttp", Certificate: cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	connection, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &tls.Config{RootCAs: roots, ServerName: "gateway.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprint(connection, "POST /stream HTTP/1.1\r\nHost: gateway.test\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nfirst\r\n")
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("response blocked until request completed: %v", err)
	}
	defer response.Body.Close()
	first := make([]byte, 5)
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	if string(first) != "first" {
		t.Fatalf("first streamed frame=%q", first)
	}
	fmt.Fprint(connection, "4\r\nlast\r\n0\r\n\r\n")
	last, err := io.ReadAll(response.Body)
	if err != nil || string(last) != "last" {
		t.Fatalf("last streamed frame=%q err=%v", last, err)
	}
}

func TestGatewayReleasesClosedUpgradeConnections(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		fmt.Fprint(connection, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		io.Copy(connection, connection)
	}))
	defer backend.Close()
	cert, roots := testTLS(t)
	port := freePort(t)
	manager, err := Start([]conf.GatewayConfig{{Port: port, Host: "gateway.test", Path: "/upgrade", BackendPort: backendPort(t, backend), Transport: "httpupgrade", Certificate: cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	connection, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &tls.Config{RootCAs: roots, ServerName: "gateway.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprint(connection, "GET /upgrade HTTP/1.1\r\nHost: gateway.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	buffer := bufio.NewReader(connection)
	response, err := http.ReadResponse(buffer, &http.Request{Method: http.MethodGet})
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: response=%v err=%v", response, err)
	}
	if _, err := connection.Write([]byte("echo")); err != nil {
		t.Fatal(err)
	}
	echoed := make([]byte, 4)
	if _, err := io.ReadFull(buffer, echoed); err != nil || string(echoed) != "echo" {
		t.Fatalf("HTTP upgrade echo=%q err=%v", echoed, err)
	}
	connection.Close()
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		remaining := len(manager.connections)
		manager.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("gateway retains closed upgrade connections")
		}
		time.Sleep(time.Millisecond)
	}
}
