// Package gateway terminates TLS for panel-assigned HTTP transports and forwards
// each host/path route to its fixed loopback Xray listener.
package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wyx2685/v2node/conf"
	"golang.org/x/net/http2"
)

type Manager struct {
	servers     []*http.Server
	listeners   []net.Listener
	transports  []interface{ CloseIdleConnections() }
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	closeOnce   sync.Once
	closeErr    error
}

type listenerConfig struct {
	port         int
	routes       []conf.GatewayConfig
	certificates map[string]*tls.Certificate
}

// Validate checks route ambiguity, transport support and certificate material
// without claiming ports or starting background work.
func Validate(configs []conf.GatewayConfig) error {
	_, err := prepare(configs)
	return err
}

func prepare(configs []conf.GatewayConfig) ([]*listenerConfig, error) {
	byPort := map[int]*listenerConfig{}
	var listeners []*listenerConfig
	cache := map[string]*tls.Certificate{}
	for _, config := range configs {
		if config.Port < 1 || config.Port > 65535 || config.BackendPort < 1 || config.BackendPort > 65535 {
			return nil, fmt.Errorf("gateway ports must be between 1 and 65535")
		}
		config.Host = normalizeHost(config.Host)
		if config.Host == "" || strings.ContainsAny(config.Host, " /\\?#@*\t\r\n") || (strings.Contains(config.Host, ":") && net.ParseIP(config.Host) == nil) {
			return nil, fmt.Errorf("invalid gateway host")
		}
		if !strings.HasPrefix(config.Path, "/") || strings.ContainsAny(config.Path, "?#\\%") || path.Clean(config.Path) != strings.TrimSuffix(config.Path, "/") && config.Path != "/" {
			return nil, fmt.Errorf("gateway path must be an absolute canonical URL path")
		}
		config.Path = strings.TrimSuffix(config.Path, "/")
		if config.Path == "" {
			config.Path = "/"
		}
		switch config.Transport {
		case "ws", "httpupgrade", "grpc", "xhttp":
		default:
			return nil, fmt.Errorf("unsupported gateway transport %q", config.Transport)
		}
		certificate := cache[config.Certificate]
		if certificate == nil {
			data, err := os.ReadFile(config.Certificate)
			if err != nil {
				return nil, fmt.Errorf("read gateway certificate: %w", err)
			}
			pair, err := tls.X509KeyPair(data, data)
			if err != nil {
				return nil, fmt.Errorf("gateway certificate/key pair: %w", err)
			}
			pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				return nil, err
			}
			certificate = &pair
			cache[config.Certificate] = certificate
		}
		if err := certificate.Leaf.VerifyHostname(config.Host); err != nil {
			return nil, fmt.Errorf("gateway host %s: %w", config.Host, err)
		}
		now := time.Now()
		if now.Before(certificate.Leaf.NotBefore) || !now.Before(certificate.Leaf.NotAfter) {
			return nil, fmt.Errorf("gateway certificate for %s is outside its validity period", config.Host)
		}
		group := byPort[config.Port]
		if group == nil {
			group = &listenerConfig{port: config.Port, certificates: map[string]*tls.Certificate{}}
			byPort[config.Port] = group
			listeners = append(listeners, group)
		}
		if previous := group.certificates[config.Host]; previous != nil && !sameChain(previous.Certificate, certificate.Certificate) {
			return nil, fmt.Errorf("gateway host %s has conflicting certificates", config.Host)
		}
		group.certificates[config.Host] = certificate
		for _, existing := range group.routes {
			if existing.Host == config.Host && (pathMatches(existing.Path, config.Path) || pathMatches(config.Path, existing.Path)) {
				return nil, fmt.Errorf("gateway routes overlap for %s:%d (%s and %s)", config.Host, config.Port, existing.Path, config.Path)
			}
		}
		group.routes = append(group.routes, config)
	}
	for _, config := range configs {
		if byPort[config.BackendPort] != nil {
			return nil, fmt.Errorf("gateway backend port %d overlaps a public listener", config.BackendPort)
		}
	}
	return listeners, nil
}

func sameChain(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func requestHost(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	return normalizeHost(strings.Trim(host, "[]"))
}

func pathMatches(prefix, value string) bool {
	return prefix == "/" || value == prefix || strings.HasPrefix(value, prefix+"/")
}

// Start claims every listener before serving any route. A bind/configuration
// failure releases all earlier listeners, allowing the caller to retain or
// restart its last known good generation.
func Start(configs []conf.GatewayConfig) (*Manager, error) {
	groups, err := prepare(configs)
	if err != nil {
		return nil, err
	}
	manager := &Manager{connections: map[net.Conn]struct{}{}}
	httpTransport := http.DefaultTransport.(*http.Transport).Clone()
	httpTransport.Proxy = nil
	httpTransport.ForceAttemptHTTP2 = false
	httpTransport.DisableCompression = true
	h2Transport := &http2.Transport{AllowHTTP: true, DisableCompression: true, DialTLSContext: func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
	}}
	manager.transports = append(manager.transports, httpTransport, h2Transport)
	for _, group := range groups {
		handler := buildHandler(group, httpTransport, h2Transport)
		server := &http.Server{
			Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
			TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				if certificate := group.certificates[normalizeHost(hello.ServerName)]; certificate != nil {
					return certificate, nil
				}
				if hello.ServerName == "" && len(group.certificates) == 1 {
					for _, certificate := range group.certificates {
						return certificate, nil
					}
				}
				return nil, fmt.Errorf("unconfigured gateway TLS hostname")
			}},
		}
		if err := http2.ConfigureServer(server, &http2.Server{}); err != nil {
			manager.Close()
			return nil, fmt.Errorf("configure gateway HTTP/2: %w", err)
		}
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(group.port))
		if err != nil {
			manager.Close()
			return nil, fmt.Errorf("listen on gateway port %d: %w", group.port, err)
		}
		manager.listeners = append(manager.listeners, &trackedListener{Listener: listener, manager: manager})
		manager.servers = append(manager.servers, server)
	}
	for i, server := range manager.servers {
		listener := manager.listeners[i]
		go func() {
			if err := server.ServeTLS(listener, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				log.WithError(err).Error("Gateway listener stopped")
			}
		}()
	}
	return manager, nil
}

type proxyRoute struct {
	host, path string
	proxy      *httputil.ReverseProxy
}

func buildHandler(group *listenerConfig, httpTransport, h2Transport http.RoundTripper) http.Handler {
	routes := make([]proxyRoute, 0, len(group.routes))
	for _, config := range group.routes {
		target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(config.BackendPort))}
		transport := httpTransport
		if config.Transport == "grpc" {
			transport = h2Transport
		}
		proxy := &httputil.ReverseProxy{
			Transport: transport, FlushInterval: -1,
			Rewrite: func(request *httputil.ProxyRequest) {
				request.SetURL(target)
				request.Out.Host = request.In.Host
				for name := range request.Out.Header {
					if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") || strings.EqualFold(name, "Forwarded") || strings.EqualFold(name, "X-Real-IP") {
						request.Out.Header.Del(name)
					}
				}
				request.SetXForwarded()
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				log.WithError(err).WithField("backend", target.Host).Warn("Gateway backend request failed")
				http.Error(w, "gateway backend unavailable", http.StatusBadGateway)
			},
		}
		routes = append(routes, proxyRoute{config.Host, config.Path, proxy})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := requestHost(r.Host)
		for _, route := range routes {
			if route.host == host && pathMatches(route.path, r.URL.Path) {
				// HTTP/1 otherwise drains an open request body before writing the
				// response, deadlocking bidirectional XHTTP streaming. HTTP/2 is
				// already full duplex and may report ErrNotSupported here.
				_ = http.NewResponseController(w).EnableFullDuplex()
				route.proxy.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
}

// Track the underlying sockets rather than ConnState: hijacked connections
// never emit StateClosed, and retaining them would leak one entry per upgrade.
type trackedListener struct {
	net.Listener
	manager *Manager
}

func (l *trackedListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tracked := &trackedConnection{Conn: connection, manager: l.manager}
	l.manager.mu.Lock()
	l.manager.connections[tracked] = struct{}{}
	l.manager.mu.Unlock()
	return tracked, nil
}

type trackedConnection struct {
	net.Conn
	manager *Manager
}

func (c *trackedConnection) Close() error {
	err := c.Conn.Close()
	c.manager.mu.Lock()
	delete(c.manager.connections, c)
	c.manager.mu.Unlock()
	return err
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		for _, server := range m.servers {
			m.closeErr = errors.Join(m.closeErr, server.Close())
		}
		for _, listener := range m.listeners {
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				m.closeErr = errors.Join(m.closeErr, err)
			}
		}
		m.mu.Lock()
		connections := make([]net.Conn, 0, len(m.connections))
		for connection := range m.connections {
			connections = append(connections, connection)
		}
		m.mu.Unlock()
		for _, connection := range connections {
			_ = connection.Close()
		}
		for _, transport := range m.transports {
			transport.CloseIdleConnections()
		}
	})
	return m.closeErr
}
