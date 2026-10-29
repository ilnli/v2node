package node

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/go-acme/lego/v4/registration"
	panel "github.com/wyx2685/v2node/api/v2board"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckCertRequiresRenewalForChangedDomain(t *testing.T) {
	cert, _ := testCertificate(t, "old.example", time.Now().Add(90*24*time.Hour))
	l := &Lego{config: &panel.CertInfo{CertDomain: "new.example"}}
	renew, err := l.CheckCert([]byte(cert))
	if err != nil {
		t.Fatal(err)
	}
	if !renew {
		t.Fatal("certificate for previous domain suppresses issuance")
	}
}

func TestCheckCertRenewalWindow(t *testing.T) {
	l := &Lego{config: &panel.CertInfo{CertDomain: "node.example"}}
	for _, days := range []int{20, 90} {
		cert, _ := testCertificate(t, "node.example", time.Now().Add(time.Duration(days)*24*time.Hour))
		renew, err := l.CheckCert([]byte(cert))
		if err != nil {
			t.Fatal(err)
		}
		if renew != (days < 30) {
			t.Fatalf("%d-day certificate: renewal=%v", days, renew)
		}
	}
	if _, err := l.CheckCert([]byte("broken")); err == nil {
		t.Fatal("invalid PEM error swallowed")
	}
}

func TestACMEAccountPersistsKeyAndRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts", "user.json")
	first, err := NewLegoUser(path, "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	first.Registration = &registration.Resource{URI: "https://acme.example.test/account/1"}
	if err := first.Save(path); err != nil {
		t.Fatal(err)
	}
	second, err := NewLegoUser(path, "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	firstKey, _ := EncodePrivate(first.key.(*ecdsa.PrivateKey))
	secondKey, _ := EncodePrivate(second.key.(*ecdsa.PrivateKey))
	if firstKey != secondKey || second.Registration.URI != first.Registration.URI {
		t.Fatal("account identity was not retained")
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("account permissions=%o", stat.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte(`{"Email":"admin@example.test","Key":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLegoUser(path, "admin@example.test"); err == nil {
		t.Fatal("corrupt account accepted")
	}
}

func TestACMEEnvironmentRestoredAfterCompleteRequestFailure(t *testing.T) {
	const existing = "PROXYNODE_TEST_DNS_EXISTING"
	const added = "PROXYNODE_TEST_DNS_ADDED"
	t.Setenv(existing, "original")
	if _, exists := os.LookupEnv(added); exists {
		t.Fatal("test environment already populated")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv(existing) != "temporary" || os.Getenv(added) != "secret-value" {
			t.Error("credentials not retained during ACME network operation")
		}
		close(entered)
		<-release
		http.Error(w, "secret-value", http.StatusBadRequest)
	}))
	defer server.Close()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEGO_CA_CERTIFICATES", caPath)
	config := &panel.CertInfo{CertMode: "http", CertDomain: "node.example", Email: "test@example.test", CertFile: filepath.Join(t.TempDir(), "pair.pem"), DNSEnv: map[string]string{existing: "temporary", added: "secret-value"}, ACMEDirectory: server.URL}
	config.KeyFile = config.CertFile
	l, err := NewLego(config)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(existing) != "original" {
		t.Fatal("construction changed process environment")
	}
	done := make(chan error, 1)
	go func() { done <- l.CreateCert() }()
	select {
	case <-entered:
	case err := <-done:
		close(release)
		t.Fatalf("ACME failed before request: %v", err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("ACME request did not reach test server")
	}
	if os.Getenv(existing) != "temporary" {
		t.Error("environment restored before operation finished")
	}
	close(release)
	if err := <-done; err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("ACME failure not safely propagated: %v", err)
	}
	if os.Getenv(existing) != "original" {
		t.Fatal("original environment not restored")
	}
	if _, exists := os.LookupEnv(added); exists {
		t.Fatal("temporary credential leaked")
	}
}

// Run with PEBBLE_BINARY=/path/to/pebble. Pebble validates real HTTP-01
// challenges on localhost:80; validation bypasses are deliberately disabled.
func TestPebbleIssueAndRenew(t *testing.T) {
	binary := os.Getenv("PEBBLE_BINARY")
	if binary == "" {
		t.Skip("set PEBBLE_BINARY to run real local ACME issuance and renewal")
	}
	dir := t.TempDir()
	cert, key := testCertificate(t, "localhost", time.Now().Add(90*24*time.Hour))
	certPath, keyPath := filepath.Join(dir, "localhost.pem"), filepath.Join(dir, "localhost.key")
	if err := os.WriteFile(certPath, []byte(cert), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEGO_CA_CERTIFICATES", certPath)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	configuration := map[string]any{"pebble": map[string]any{
		"listenAddress": fmt.Sprintf("127.0.0.1:%d", port), "managementListenAddress": "",
		"certificate": certPath, "privateKey": keyPath, "httpPort": 80, "tlsPort": 5001,
		"profiles": map[string]any{"default": map[string]any{"description": "renewal test", "validityPeriod": 3600}},
	}}
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "pebble.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "pebble.log")
	output, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	process := exec.Command(binary, "-config", configPath)
	process.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_VA_ALWAYS_VALID=0", "PEBBLE_WFE_NONCEREJECT=0", "PEBBLE_AUTHZREUSE=0")
	process.Stdout, process.Stderr = output, output
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Process.Kill()
		_ = process.Wait()
		if t.Failed() {
			logs, _ := os.ReadFile(logPath)
			t.Log(string(logs))
		}
	}()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(cert))
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	directory := fmt.Sprintf("https://localhost:%d/dir", port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err := client.Get(directory)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Pebble did not start: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	info := &panel.CertInfo{CertMode: "http", CertDomain: "localhost", Email: "test@example.test", ACMEDirectory: directory, AccountDirectory: filepath.Join(dir, "accounts"), CertFile: filepath.Join(dir, "managed", "pair.pem")}
	info.KeyFile = info.CertFile
	first, err := EnsureCertificate(info)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || first.Fingerprint == "" || time.Until(first.NotAfter) > 2*time.Hour {
		t.Fatalf("unexpected issued certificate: %+v", first)
	}
	accounts, err := filepath.Glob(filepath.Join(dir, "accounts", "*.json"))
	if err != nil || len(accounts) != 1 {
		t.Fatalf("persisted accounts=%v err=%v", accounts, err)
	}
	before, err := os.ReadFile(accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureCertificate(info)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Changed || first.Fingerprint == second.Fingerprint {
		t.Fatal("near-expiry certificate was not renewed")
	}
	after, err := os.ReadFile(accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("renewal replaced the persisted ACME account")
	}
	// A panel profile revision changes the PEM generation path, but must not
	// create another ACME account or lose its persisted registration.
	info.CertFile = filepath.Join(dir, "managed", "next-revision", "pair.pem")
	info.KeyFile = info.CertFile
	if _, err := EnsureCertificate(info); err != nil {
		t.Fatal(err)
	}
	accountAfterRevision, err := os.ReadFile(accounts[0])
	if err != nil || string(accountAfterRevision) != string(before) {
		t.Fatalf("profile revision replaced the ACME account: %v", err)
	}
	nestedAccounts, err := filepath.Glob(filepath.Join(dir, "managed", "next-revision", "accounts", "*.json"))
	if err != nil || len(nestedAccounts) != 0 {
		t.Fatalf("account leaked into disposable revision directory: %v %v", nestedAccounts, err)
	}

	pair, err := tls.LoadX509KeyPair(info.CertFile, info.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Fatal(err)
	}
}
