package node

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns"
	"github.com/go-acme/lego/v4/registration"
	panel "github.com/wyx2685/v2node/api/v2board"
)

type Lego struct {
	client *lego.Client
	config *panel.CertInfo
}

// NewLego has no network or environment side effects. The complete ACME
// operation, including provider creation and cleanup, runs under one lock.
func NewLego(config *panel.CertInfo) (*Lego, error) {
	if config == nil || config.CertDomain == "" {
		return nil, fmt.Errorf("ACME certificate domain is required")
	}
	if config.CertMode != "http" && config.CertMode != "dns" {
		return nil, fmt.Errorf("unsupported ACME mode: %s", config.CertMode)
	}
	copy := *config
	copy.CertFile = certificatePath(config.CertFile, config)
	copy.KeyFile = certificatePath(config.KeyFile, config)
	return &Lego{config: &copy}, nil
}

func checkPath(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0700)
}

func (l *Lego) SetProvider() error {
	switch l.config.CertMode {
	case "http":
		return l.client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", "80"))
	case "dns":
		provider, err := dns.NewDNSChallengeProviderByName(l.config.Provider)
		if err != nil {
			return fmt.Errorf("create DNS provider: %w", err)
		}
		return l.client.Challenge.SetDNS01Provider(provider)
	default:
		return fmt.Errorf("unsupported ACME mode: %s", l.config.CertMode)
	}
}

func (l *Lego) CreateCert() error {
	certificateMu.Lock()
	defer certificateMu.Unlock()
	return l.obtain()
}

func (l *Lego) RenewCert() error {
	certificateMu.Lock()
	defer certificateMu.Unlock()
	current, err := readCertificate(l.config)
	if err == nil && time.Until(current.NotAfter) > certificateRenewBefore {
		return nil
	}
	return l.obtain()
}

// obtain is called only with certificateMu held, because DNS providers may read
// credentials lazily during Present/CleanUp, long after their constructors.
func (l *Lego) obtain() error {
	err := withDNSEnvironment(l.config.DNSEnv, func() error {
		directory := l.config.ACMEDirectory
		if directory == "" {
			directory = lego.LEDirectoryProduction
		}
		accountID := sha256.Sum256([]byte(directory + "\x00" + l.config.Email))
		accountDirectory := l.config.AccountDirectory
		if accountDirectory == "" {
			accountDirectory = filepath.Join(filepath.Dir(l.config.CertFile), "accounts")
		}
		accountPath := filepath.Join(accountDirectory, fmt.Sprintf("%x.json", accountID))
		user, err := NewLegoUser(accountPath, l.config.Email)
		if err != nil {
			return fmt.Errorf("load ACME account: %w", err)
		}
		config := lego.NewConfig(user)
		config.CADirURL = directory
		config.Certificate.KeyType = certcrypto.RSA2048
		config.HTTPClient.Timeout = 30 * time.Second
		client, err := lego.NewClient(config)
		if err != nil {
			return fmt.Errorf("create ACME client: %w", err)
		}
		l.client = client
		if err := l.SetProvider(); err != nil {
			return err
		}
		if user.Registration == nil {
			user.Registration, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
			if err != nil {
				return fmt.Errorf("register ACME account: %w", err)
			}
			if err := user.Save(accountPath); err != nil {
				return fmt.Errorf("save ACME account: %w", err)
			}
		}
		resource, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: []string{l.config.CertDomain}, Bundle: true})
		if err != nil {
			return fmt.Errorf("obtain certificate: %w", err)
		}
		return l.writeCert(resource)
	})
	if err == nil {
		return nil
	}
	// Provider errors can contain request parameters. Status reports must never
	// echo supplied credentials back to the panel's general node-status UI.
	message := err.Error()
	for _, value := range l.config.DNSEnv {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	return fmt.Errorf("ACME: %s", message)
}

func withDNSEnvironment(values map[string]string, run func() error) error {
	type value struct {
		text   string
		exists bool
	}
	old := make(map[string]value, len(values))
	defer func() {
		for name, previous := range old {
			if previous.exists {
				_ = os.Setenv(name, previous.text)
			} else {
				_ = os.Unsetenv(name)
			}
		}
	}()
	for name, text := range values {
		previous, exists := os.LookupEnv(name)
		old[name] = value{previous, exists}
		if err := os.Setenv(name, text); err != nil {
			return fmt.Errorf("set DNS environment variable %q: %w", name, err)
		}
	}
	return run()
}

func (l *Lego) CheckCert(data []byte) (bool, error) {
	cert, err := certcrypto.ParsePEMCertificate(data)
	if err != nil {
		return false, err
	}
	if err := cert.VerifyHostname(l.config.CertDomain); err != nil {
		return true, nil
	}
	return time.Until(cert.NotAfter) <= certificateRenewBefore || time.Now().Before(cert.NotBefore), nil
}

func (l *Lego) writeCert(resource *certificate.Resource) error {
	if resource == nil {
		return fmt.Errorf("ACME returned no certificate")
	}
	return publishCertificate(l.config, resource.Certificate, resource.PrivateKey)
}

type User struct {
	Email        string                 `json:"Email"`
	Registration *registration.Resource `json:"Registration"`
	key          crypto.PrivateKey
	KeyEncoded   string `json:"Key"`
}

func (u *User) GetEmail() string                        { return u.Email }
func (u *User) GetRegistration() *registration.Resource { return u.Registration }
func (u *User) GetPrivateKey() crypto.PrivateKey        { return u.key }

// NewLegoUser loads or durably creates an account key. Registration belongs to
// the configured directory and is performed by obtain using the same client.
func NewLegoUser(path, email string) (*User, error) {
	user := &User{Email: email}
	if err := user.Load(path); err == nil {
		if user.Email != email {
			return nil, fmt.Errorf("ACME account email does not match")
		}
		return user, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	user.key = key
	if err := user.Save(path); err != nil {
		return nil, err
	}
	return user, nil
}

func EncodePrivate(key *ecdsa.PrivateKey) (string, error) {
	encoded, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encoded})), nil
}

func (u *User) Save(path string) error {
	key, ok := u.key.(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("unsupported ACME account key")
	}
	encoded, err := EncodePrivate(key)
	if err != nil {
		return err
	}
	copy := *u
	copy.KeyEncoded = encoded
	data, err := json.Marshal(&copy)
	if err != nil {
		return err
	}
	return atomicPrivateFile(path, data)
}

func (u *User) DecodePrivate(encoded string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, fmt.Errorf("invalid ACME account private key PEM")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func (u *User) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read ACME account: %w", err)
	}
	if err := json.Unmarshal(data, u); err != nil {
		return fmt.Errorf("decode ACME account: %w", err)
	}
	u.key, err = u.DecodePrivate(u.KeyEncoded)
	u.KeyEncoded = ""
	return err
}
