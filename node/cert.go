package node

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	panel "github.com/wyx2685/v2node/api/v2board"
)

const certificateRenewBefore = 30 * 24 * time.Hour

// CertificateStatus describes the material actually available to listeners.
type CertificateStatus struct {
	Domain      string    `json:"domain"`
	NotAfter    time.Time `json:"not_after"`
	Fingerprint string    `json:"fingerprint"`
	Changed     bool      `json:"changed"`
}

// Serializing certificate work also prevents two profiles from temporarily
// supplying different credentials to DNS providers that read process environment.
var certificateMu sync.Mutex

func (c *Controller) renewCertTask(_ context.Context) error {
	status, err := EnsureCertificate(c.info.Common.CertInfo)
	if err != nil {
		return fmt.Errorf("renew certificate: %w", err)
	}
	if status.Changed && c.server != nil && c.server.ReloadCh != nil {
		select {
		case c.server.ReloadCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (c *Controller) requestCert() error {
	_, err := EnsureCertificate(c.info.Common.CertInfo)
	return err
}

// EnsureCertificate validates or provisions a certificate and reports its expiry.
// Managed callers should use one path for CertFile and KeyFile: a combined PEM
// is replaced in one atomic rename. Distinct legacy paths remain supported.
func EnsureCertificate(info *panel.CertInfo) (CertificateStatus, error) {
	certificateMu.Lock()
	defer certificateMu.Unlock()
	return ensureCertificate(info)
}

func ensureCertificate(info *panel.CertInfo) (CertificateStatus, error) {
	if info == nil {
		return CertificateStatus{}, fmt.Errorf("missing certificate configuration")
	}
	if info.CertMode == "" || info.CertMode == "none" {
		return CertificateStatus{}, nil
	}
	config := *info
	config.CertFile = certificatePath(info.CertFile, info)
	config.KeyFile = certificatePath(info.KeyFile, info)
	if config.CertFile == "" || config.KeyFile == "" {
		return CertificateStatus{}, fmt.Errorf("certificate and key paths are required")
	}
	before, existingErr := readCertificate(&config)
	switch config.CertMode {
	case "file":
		return before, existingErr
	case "remote":
		_, err := inspectCertificate([]byte(config.TlsCert), []byte(config.TlsKey), config.CertDomain)
		if err != nil {
			return CertificateStatus{}, fmt.Errorf("uploaded certificate: %w", err)
		}
		if existingErr == nil {
			cert, certErr := os.ReadFile(config.CertFile)
			key, keyErr := os.ReadFile(config.KeyFile)
			if certErr == nil && keyErr == nil {
				current, currentErr := certificateBundle(cert, key)
				desired, desiredErr := certificateBundle([]byte(config.TlsCert), []byte(config.TlsKey))
				if currentErr == nil && desiredErr == nil && bytes.Equal(current, desired) {
					return before, nil
				}
			}
		}
		if err := publishCertificate(&config, []byte(config.TlsCert), []byte(config.TlsKey)); err != nil {
			return CertificateStatus{}, err
		}
	case "http", "dns":
		if config.CertDomain == "" {
			return CertificateStatus{}, fmt.Errorf("ACME certificate domain is required")
		}
		if existingErr == nil && time.Until(before.NotAfter) > certificateRenewBefore {
			return before, nil
		}
		l, err := NewLego(&config)
		if err != nil {
			return CertificateStatus{}, err
		}
		if err := l.obtain(); err != nil {
			return CertificateStatus{}, err
		}
	case "self":
		if existingErr == nil && time.Until(before.NotAfter) > certificateRenewBefore {
			return before, nil
		}
		if err := generateSelfSslCertificate(config.CertDomain, config.CertFile, config.KeyFile); err != nil {
			return CertificateStatus{}, err
		}
	default:
		return CertificateStatus{}, fmt.Errorf("unsupported certificate mode: %s", config.CertMode)
	}
	after, err := readCertificate(&config)
	if err != nil {
		return CertificateStatus{}, err
	}
	after.Changed = true
	return after, nil
}

func certificatePath(path string, config *panel.CertInfo) string {
	return strings.NewReplacer("{domain}", config.CertDomain, "{email}", config.Email).Replace(path)
}

func readCertificate(config *panel.CertInfo) (CertificateStatus, error) {
	cert, err := os.ReadFile(config.CertFile)
	if err != nil {
		return CertificateStatus{}, fmt.Errorf("read certificate: %w", err)
	}
	key, err := os.ReadFile(config.KeyFile)
	if err != nil {
		return CertificateStatus{}, fmt.Errorf("read private key: %w", err)
	}
	return inspectCertificate(cert, key, config.CertDomain)
}

func inspectCertificate(certPEM, keyPEM []byte, domain string) (CertificateStatus, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return CertificateStatus{}, fmt.Errorf("invalid certificate/key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return CertificateStatus{}, err
	}
	if domain != "" {
		if err := leaf.VerifyHostname(domain); err != nil {
			return CertificateStatus{}, fmt.Errorf("certificate domain: %w", err)
		}
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return CertificateStatus{}, fmt.Errorf("certificate is outside its validity period (%s to %s)", leaf.NotBefore.UTC().Format(time.RFC3339), leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	return CertificateStatus{Domain: domain, NotAfter: leaf.NotAfter, Fingerprint: hex.EncodeToString(fingerprint[:])}, nil
}

func publishCertificate(config *panel.CertInfo, cert, key []byte) error {
	if _, err := inspectCertificate(cert, key, config.CertDomain); err != nil {
		return err
	}
	bundle, err := certificateBundle(cert, key)
	if err != nil {
		return err
	}
	certPath, err := filepath.Abs(config.CertFile)
	if err != nil {
		return err
	}
	keyPath, err := filepath.Abs(config.KeyFile)
	if err != nil {
		return err
	}
	if certPath != keyPath {
		target, linkErr := os.Readlink(keyPath)
		if linkErr != nil || target != certPath {
			if err := checkPath(keyPath); err != nil {
				return err
			}
			temporary, err := os.CreateTemp(filepath.Dir(keyPath), ".certificate-link-*")
			if err != nil {
				return err
			}
			name := temporary.Name()
			if err := temporary.Close(); err != nil {
				os.Remove(name)
				return err
			}
			if err := os.Remove(name); err != nil {
				return err
			}
			defer os.Remove(name)
			if err := os.Symlink(certPath, name); err != nil {
				return err
			}
			// During migration from two legacy files, first put the OLD matching key
			// alongside its certificate. Repointing the key alias then preserves the
			// old pair even if publishing the new bundle fails.
			oldCert, certErr := os.ReadFile(certPath)
			oldKey, keyErr := os.ReadFile(keyPath)
			if certErr == nil && keyErr == nil {
				if oldBundle, err := certificateBundle(oldCert, oldKey); err == nil {
					if err := atomicPrivateFile(certPath, oldBundle); err != nil {
						return err
					}
				}
			}
			if err := os.Rename(name, keyPath); err != nil {
				return err
			}
		}
	}
	// Both legacy aliases and managed paths now read a single atomically
	// replaced file; independent certificate/key writes cannot tear the pair.
	if err := atomicPrivateFile(certPath, bundle); err != nil {
		return fmt.Errorf("publish certificate: %w", err)
	}
	return nil
}

func certificateBundle(cert, key []byte) ([]byte, error) {
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	// Normalize the PEM blocks so a pasted combined certificate containing an
	// unrelated embedded key cannot precede the validated private key.
	var bundle []byte
	for _, der := range pair.Certificate {
		bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		return nil, err
	}
	return append(bundle, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})...), nil
}

func atomicPrivateFile(path string, data []byte) error {
	if err := checkPath(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".certificate-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func generateSelfSslCertificate(domain, certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: domain},
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		NotBefore:   time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0),
	}
	if ip := net.ParseIP(domain); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{domain}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return err
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return publishCertificate(&panel.CertInfo{CertDomain: domain, CertFile: certPath, KeyFile: keyPath},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}))
}
