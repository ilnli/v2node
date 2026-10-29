package managed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/conf"
	"github.com/wyx2685/v2node/core"
	"github.com/wyx2685/v2node/gateway"
	"github.com/wyx2685/v2node/node"
)

type Identity struct {
	APIHost  string `json:"api_host"`
	HostID   int    `json:"host_id"`
	Key      string `json:"key"`
	Enrolled bool   `json:"enrolled"`
}

type Prepared struct {
	ConfigPath         string
	ReadyPath          string
	Certificates       []map[string]any
	Nodes              []map[string]any
	CertificateChanged bool
}

func safeID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// Prepare validates the complete desired listener set before changing the running worker.
func Prepare(manifest Manifest, identity Identity, directory string) (*Prepared, error) {
	if manifest.HostID != identity.HostID || manifest.Revision == "" {
		return nil, errors.New("manifest host/revision mismatch")
	}
	generation := filepath.Join(directory, "generations", safeID(manifest.Revision))
	result := &Prepared{ConfigPath: filepath.Join(generation, "config.json"), ReadyPath: filepath.Join(generation, "ready.json"), Nodes: []map[string]any{}, Certificates: []map[string]any{}}
	config := conf.New()
	config.ReadyFile = result.ReadyPath
	config.Audit.Enabled = manifest.Audit.Enabled
	config.Audit.Directory = filepath.Join(directory, "audit")
	if manifest.Audit.MemoryMiB != 0 {
		config.Audit.MemoryMiB = manifest.Audit.MemoryMiB
	}
	if manifest.Audit.DiskMiB != 0 {
		config.Audit.DiskMiB = manifest.Audit.DiskMiB
	}
	if config.Audit.MemoryMiB < 1 || config.Audit.MemoryMiB > 128 || config.Audit.DiskMiB < 1 || config.Audit.DiskMiB > 1024 {
		return nil, errors.New("invalid shared audit budget")
	}
	type candidate struct {
		spec Node
		info *panel.NodeInfo
		wire map[string]any
	}
	candidates := []candidate{}
	ports := map[string]int{}
	ids := map[int]bool{}
	reserve := func(network string, port, id int) error {
		key := fmt.Sprintf("%s/%d", network, port)
		if old, ok := ports[key]; ok {
			return fmt.Errorf("nodes %d and %d conflict on %s", old, id, key)
		}
		ports[key] = id
		return nil
	}
	if manifest.Enabled {
		for _, spec := range manifest.Nodes {
			if !spec.Enabled {
				continue
			}
			if spec.ID < 1 || ids[spec.ID] {
				return nil, errors.New("invalid or repeated node ID")
			}
			ids[spec.ID] = true
			info, err := panel.ParseNodeInfo(spec.Config, spec.ID, identity.APIHost)
			if err != nil {
				return nil, fmt.Errorf("node %d: %w", spec.ID, err)
			}
			if info.Security == panel.Tls && spec.Certificate == nil {
				return nil, fmt.Errorf("node %d requires a certificate profile", spec.ID)
			}
			if (info.Type == "trojan" || info.Type == "hysteria2" || info.Type == "tuic" || info.Type == "anytls") && info.Security != panel.Tls {
				return nil, fmt.Errorf("node %d requires TLS", spec.ID)
			}
			var wire map[string]any
			if err = json.Unmarshal(spec.Config, &wire); err != nil {
				return nil, fmt.Errorf("invalid node %d config", spec.ID)
			}
			if wire["tls_settings"] == nil {
				wire["tls_settings"] = map[string]any{}
			}
			gateway := spec.Gateway
			if gateway != nil && gateway.Enabled {
				if info.Security != panel.Tls || spec.Certificate == nil || info.Common.Flow != "" {
					return nil, errors.New("HTTP gateway requires TLS and no Vision flow")
				}
				switch info.Common.Network {
				case "ws", "grpc", "httpupgrade", "xhttp", "splithttp":
				default:
					return nil, errors.New("transport cannot use the HTTP gateway")
				}
				if gateway.Port < 1 || gateway.Port > 65535 || gateway.BackendPort < 1024 || gateway.BackendPort > 65535 || gateway.Host == "" || !strings.HasPrefix(gateway.Path, "/") {
					return nil, errors.New("invalid gateway listener")
				}
				// A shared public TLS listener is allowed; internal listeners must be unique.
				public := fmt.Sprintf("tcp/%d", gateway.Port)
				if old, ok := ports[public]; ok && old != -1 {
					return nil, errors.New("gateway conflicts with a direct listener")
				}
				ports[public] = -1
				if err := reserve("tcp", gateway.BackendPort, spec.ID); err != nil {
					return nil, err
				}
				wire["listen_ip"] = "127.0.0.1"
				wire["server_port"] = gateway.BackendPort
				wire["tls"] = 0
			} else {
				networks := []string{"tcp"}
				if info.Type == "hysteria2" || info.Type == "tuic" {
					networks = []string{"udp"}
				}
				if info.Type == "shadowsocks" {
					networks = []string{"tcp", "udp"}
				}
				for _, network := range networks {
					if err := reserve(network, info.Common.ServerPort, spec.ID); err != nil {
						return nil, err
					}
				}
			}
			candidates = append(candidates, candidate{spec, info, wire})
		}
	}
	certs := map[int]string{}
	infos := []*panel.NodeInfo{}
	for _, candidate := range candidates {
		spec, wire := candidate.spec, candidate.wire
		if cert := spec.Certificate; cert != nil {
			if cert.ID < 1 || cert.Revision == "" {
				return nil, errors.New("invalid certificate profile identity")
			}
			pem := certs[cert.ID]
			if pem == "" {
				pem = filepath.Join(directory, "certificates", fmt.Sprint(cert.ID), safeID(cert.Revision), "bundle.pem")
				status, err := node.EnsureCertificate(&panel.CertInfo{CertMode: cert.Mode, AccountDirectory: filepath.Join(directory, "certificates", fmt.Sprint(cert.ID), "accounts"), CertFile: pem, KeyFile: pem, CertDomain: cert.Domain, Email: cert.Email, Provider: cert.Provider, DNSEnv: cert.DNSEnv, TlsCert: cert.PEM, TlsKey: cert.PrivateKey, ACMEDirectory: cert.ACMEDirectory})
				if err != nil {
					return nil, fmt.Errorf("certificate %d: %w", cert.ID, err)
				}
				result.CertificateChanged = result.CertificateChanged || status.Changed
				result.Certificates = append(result.Certificates, map[string]any{"id": cert.ID, "state": "ready", "not_after": status.NotAfter.Unix(), "fingerprint": status.Fingerprint, "revision": cert.Revision})
				certs[cert.ID] = pem
			}
			tls := wire["tls_settings"].(map[string]any)
			tls["cert_mode"] = "file"
			tls["cert_file"] = pem
			tls["key_file"] = pem
			tls["server_name"] = cert.Domain
			if gateway := spec.Gateway; gateway != nil && gateway.Enabled {
				config.Gateways = append(config.Gateways, conf.GatewayConfig{Port: gateway.Port, Host: gateway.Host, Path: gateway.Path, BackendPort: gateway.BackendPort, Transport: candidate.info.Common.Network, Certificate: pem})
			}
		}
		data, err := json.Marshal(wire)
		if err != nil {
			return nil, err
		}
		info, err := panel.ParseNodeInfo(data, spec.ID, identity.APIHost)
		if err != nil {
			return nil, err
		}
		if err := core.ValidateNode(info); err != nil {
			return nil, fmt.Errorf("node %d: %w", spec.ID, err)
		}
		infos = append(infos, info)
		path := filepath.Join(generation, fmt.Sprintf("node-%d.json", spec.ID))
		if err := writePrivate(path, data); err != nil {
			return nil, err
		}
		config.NodeConfigs = append(config.NodeConfigs, conf.NodeConfig{APIHost: identity.APIHost, NodeID: spec.ID, Key: identity.Key, Timeout: 20, ConfigFile: path, TrafficDirectory: filepath.Join(directory, "traffic", fmt.Sprint(spec.ID))})
		result.Nodes = append(result.Nodes, map[string]any{"node_id": spec.ID, "state": "running", "revision": manifest.Revision, "port": candidate.info.Common.ServerPort})
	}
	if _, _, _, err := core.GetCustomConfig(infos); err != nil {
		return nil, fmt.Errorf("invalid node routing: %w", err)
	}
	if err := gateway.Validate(config.Gateways); err != nil {
		return nil, fmt.Errorf("invalid gateway: %w", err)
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writePrivate(result.ConfigPath, data); err != nil {
		return nil, err
	}
	return result, nil
}
