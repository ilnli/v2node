package node

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	panel "github.com/wyx2685/v2node/api/v2board"
)

func testCertificate(t *testing.T, domain string, expiry time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: expiry, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encodedKey}))
}

func TestRemoteCertificateUpdatesAndRejectsInvalidReplacement(t *testing.T) {
	dir := t.TempDir()
	cert, key := testCertificate(t, "node.example", time.Now().Add(90*24*time.Hour))
	info := &panel.CertInfo{CertMode: "remote", CertDomain: "node.example", CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem"), TlsCert: cert, TlsKey: key}
	c := &Controller{info: &panel.NodeInfo{Common: &panel.CommonNode{CertInfo: info}}}
	if err := c.requestCert(); err != nil {
		t.Fatal(err)
	}
	first, err := tls.LoadX509KeyPair(info.CertFile, info.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	nextCert, nextKey := testCertificate(t, "node.example", time.Now().Add(91*24*time.Hour))
	info.TlsCert, info.TlsKey = nextCert, nextKey
	if err := c.requestCert(); err != nil {
		t.Fatal(err)
	}
	second, err := tls.LoadX509KeyPair(info.CertFile, info.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Fatal("updated uploaded certificate was ignored")
	}
	for _, bad := range []struct{ name, cert, key string }{
		{"mismatched key", nextCert, key},
		{"invalid PEM", "broken", nextKey},
	} {
		t.Run(bad.name, func(t *testing.T) {
			info.TlsCert, info.TlsKey = bad.cert, bad.key
			if err := c.requestCert(); err == nil {
				t.Fatal("invalid certificate accepted")
			}
			current, err := tls.LoadX509KeyPair(info.CertFile, info.KeyFile)
			if err != nil {
				t.Fatal(err)
			}
			if string(current.Certificate[0]) != string(second.Certificate[0]) {
				t.Fatal("last valid certificate changed")
			}
		})
	}
	stat, err := os.Stat(info.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("private key permissions = %o", stat.Mode().Perm())
	}
}

func TestCertificateRejectsWrongDomainAndExpiredUpload(t *testing.T) {
	for _, tc := range []struct {
		name, domain string
		expiry       time.Time
	}{
		{"wrong domain", "other.example", time.Now().Add(90 * 24 * time.Hour)},
		{"expired", "node.example", time.Now().Add(-time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cert, key := testCertificate(t, tc.domain, tc.expiry)
			info := &panel.CertInfo{CertMode: "remote", CertDomain: "node.example", CertFile: filepath.Join(t.TempDir(), "pair.pem"), TlsCert: cert, TlsKey: key}
			info.KeyFile = info.CertFile
			c := &Controller{info: &panel.NodeInfo{Common: &panel.CommonNode{CertInfo: info}}}
			if err := c.requestCert(); err == nil {
				t.Fatal("invalid upload accepted")
			}
		})
	}
}

func TestSelfCertificateIsUsableAndPrivate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "self.pem"), filepath.Join(dir, "self.key")
	if err := generateSelfSslCertificate("node.example", certFile, keyFile); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("node.example"); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("private key permissions = %o", stat.Mode().Perm())
	}
}

func TestRemoteCombinedPEMDoesNotPublishAnEmbeddedWrongKey(t *testing.T) {
	cert, key := testCertificate(t, "node.example", time.Now().Add(90*24*time.Hour))
	_, wrongKey := testCertificate(t, "other.example", time.Now().Add(90*24*time.Hour))
	info := &panel.CertInfo{CertMode: "remote", CertDomain: "node.example", CertFile: filepath.Join(t.TempDir(), "pair.pem"), TlsCert: cert + wrongKey, TlsKey: key}
	info.KeyFile = info.CertFile
	if _, err := EnsureCertificate(info); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(info.CertFile, info.KeyFile); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyPublicationFailurePreservesPreviousCertificate(t *testing.T) {
	cert, key := testCertificate(t, "node.example", time.Now().Add(90*24*time.Hour))
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(certFile, []byte(cert), 0600); err != nil {
		t.Fatal(err)
	}
	replacement, replacementKey := testCertificate(t, "node.example", time.Now().Add(90*24*time.Hour))
	info := &panel.CertInfo{CertMode: "remote", CertDomain: "node.example", CertFile: certFile, KeyFile: t.TempDir(), TlsCert: replacement, TlsKey: replacementKey}
	if _, err := EnsureCertificate(info); err == nil {
		t.Fatal("directory accepted as key destination")
	}
	previous, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(previous, []byte(key)); err != nil {
		t.Fatalf("previous certificate replaced despite failed publication: %v", err)
	}
}

func TestRemoteChainUpdateKeepsLeafFingerprintAndSignalsReload(t *testing.T) {
	cert, key := testCertificate(t, "node.example", time.Now().Add(90*24*time.Hour))
	extra, _ := testCertificate(t, "issuer.example", time.Now().Add(90*24*time.Hour))
	info := &panel.CertInfo{CertMode: "remote", CertDomain: "node.example", CertFile: filepath.Join(t.TempDir(), "pair.pem"), TlsCert: cert, TlsKey: key}
	info.KeyFile = info.CertFile
	first, err := EnsureCertificate(info)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := EnsureCertificate(info)
	if err != nil || unchanged.Changed {
		t.Fatalf("unchanged upload triggered reload: %+v %v", unchanged, err)
	}
	info.TlsCert += extra
	second, err := EnsureCertificate(info)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Changed || first.Fingerprint != second.Fingerprint {
		t.Fatalf("chain update status: first=%+v second=%+v", first, second)
	}
	pair, err := tls.LoadX509KeyPair(info.CertFile, info.KeyFile)
	if err != nil || len(pair.Certificate) != 2 {
		t.Fatalf("uploaded chain was not applied: %v", err)
	}
}
