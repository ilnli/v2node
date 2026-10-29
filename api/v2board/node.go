package panel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"encoding/json"
)

// Security type
const (
	None    = 0
	Tls     = 1
	Reality = 2
)

type NodeInfo struct {
	Id           int
	Type         string
	Security     int
	PushInterval time.Duration
	PullInterval time.Duration
	Tag          string
	Common       *CommonNode
}

type CommonNode struct {
	Protocol   string      `json:"protocol"`
	ListenIP   string      `json:"listen_ip"`
	ServerPort int         `json:"server_port"`
	Routes     []Route     `json:"routes"`
	BaseConfig *BaseConfig `json:"base_config"`
	//vless vmess trojan
	Tls                  int         `json:"tls"`
	TlsSettings          TlsSettings `json:"tls_settings"`
	CertInfo             *CertInfo
	Network              string          `json:"network"`
	NetworkSettings      json.RawMessage `json:"network_settings"`
	TrustedXForwardedFor []string        `json:"trusted_x_forwarded_for"`
	Encryption           string          `json:"encryption"`
	EncryptionSettings   EncSettings     `json:"encryption_settings"`
	ServerName           string          `json:"server_name"`
	Flow                 string          `json:"flow"`
	//shadowsocks
	Cipher    string `json:"cipher"`
	ServerKey string `json:"server_key"`
	//tuic
	CongestionControl string `json:"congestion_control"`
	ZeroRTTHandshake  bool   `json:"zero_rtt_handshake"`
	//anytls
	PaddingScheme []string `json:"padding_scheme,omitempty"`
	//hysteria hysteria2
	UpMbps                  int    `json:"up_mbps"`
	DownMbps                int    `json:"down_mbps"`
	Obfs                    string `json:"obfs"`
	ObfsPassword            string `json:"obfs_password"`
	Ignore_Client_Bandwidth bool   `json:"ignore_client_bandwidth"`
}

type Route struct {
	Id          int      `json:"id"`
	Match       []string `json:"match"`
	Action      string   `json:"action"`
	ActionValue *string  `json:"action_value"`
}

type BaseConfig struct {
	PushInterval           any `json:"push_interval"`
	PullInterval           any `json:"pull_interval"`
	DeviceOnlineMinTraffic int `json:"device_online_min_traffic"`
	NodeReportMinTraffic   int `json:"node_report_min_traffic"`
}

type TlsSettings struct {
	Email            string            `json:"email"`
	ACMEDirectory    string            `json:"acme_directory"`
	DNSEnvMap        map[string]string `json:"dns_env_map"`
	ALPN             []string          `json:"alpn,omitempty"`
	ServerName       string            `json:"server_name"`
	ServerNames      []string          `json:"server_names"`
	Dest             string            `json:"dest"`
	ServerPort       string            `json:"server_port"`
	ShortId          string            `json:"short_id"`
	ShortIds         []string          `json:"short_ids"`
	PrivateKey       string            `json:"private_key"`
	Mldsa65Seed      string            `json:"mldsa65Seed"`
	Xver             uint64            `json:"xver,string"`
	CertMode         string            `json:"cert_mode"`
	CertFile         string            `json:"cert_file"`
	KeyFile          string            `json:"key_file"`
	TlsCert          string            `json:"tls_cert"`
	TlsKey           string            `json:"tls_key"`
	Provider         string            `json:"provider"`
	DNSEnv           string            `json:"dns_env"`
	RejectUnknownSni string            `json:"reject_unknown_sni"`
}

type CertInfo struct {
	AccountDirectory string
	ACMEDirectory    string
	CertMode         string
	CertFile         string
	KeyFile          string
	TlsCert          string
	TlsKey           string
	Email            string
	CertDomain       string
	DNSEnv           map[string]string
	Provider         string
	RejectUnknownSni bool
}

type EncSettings struct {
	Mode          string `json:"mode"`
	Ticket        string `json:"ticket"`
	ServerPadding string `json:"server_padding"`
	PrivateKey    string `json:"private_key"`
}

func (c *Client) GetNodeInfo(ctx context.Context) (node *NodeInfo, err error) {
	const path = "/api/v2/server/config"
	r, err := c.client.
		R().
		SetContext(ctx).
		SetHeader("If-None-Match", c.nodeEtag).
		ForceContentType("application/json").
		Get(path)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("received nil response")
	}

	if r.StatusCode() == 304 {
		return nil, nil
	}
	if r.StatusCode() != 200 {
		return nil, fmt.Errorf("node configuration HTTP status %d", r.StatusCode())
	}
	hash := sha256.Sum256(r.Body())
	newBodyHash := hex.EncodeToString(hash[:])
	if c.responseBodyHash == newBodyHash {
		return nil, nil
	}
	node, err = ParseNodeInfo(r.Body(), c.NodeId, c.APIHost)
	if err != nil {
		return nil, err
	}
	c.responseBodyHash = newBodyHash
	c.nodeEtag = r.Header().Get("ETag")
	return node, nil
}

// ParseNodeInfo is shared by legacy polling and an authenticated managed manifest.
func ParseNodeInfo(data []byte, nodeID int, apiHost string) (node *NodeInfo, err error) {
	node = &NodeInfo{
		Id: nodeID,
	}
	// parse protocol params
	cm := &CommonNode{}
	err = json.Unmarshal(data, cm)
	if err != nil {
		return nil, fmt.Errorf("decode node params error: %s", err)
	}
	switch cm.Protocol {
	case "vmess", "trojan", "hysteria2", "tuic", "anytls", "vless":
		node.Type = cm.Protocol
		node.Security = cm.Tls
	case "shadowsocks":
		node.Type = cm.Protocol
		node.Security = 0
	default:
		return nil, fmt.Errorf("unsupport protocol: %s", cm.Protocol)
	}
	node.Tag = fmt.Sprintf("[%s]-%s:%d", apiHost, node.Type, node.Id)
	cf := cm.TlsSettings.CertFile
	kf := cm.TlsSettings.KeyFile
	if cf == "" {
		cf = filepath.Join("/etc/v2node/", cm.Protocol+strconv.Itoa(nodeID)+".cer")
	}
	if kf == "" {
		kf = filepath.Join("/etc/v2node/", cm.Protocol+strconv.Itoa(nodeID)+".key")
	}
	cm.CertInfo = &CertInfo{
		CertMode:         cm.TlsSettings.CertMode,
		CertFile:         cf,
		KeyFile:          kf,
		Email:            cm.TlsSettings.Email,
		ACMEDirectory:    cm.TlsSettings.ACMEDirectory,
		CertDomain:       cm.TlsSettings.PrimaryServerName(),
		DNSEnv:           make(map[string]string),
		Provider:         cm.TlsSettings.Provider,
		RejectUnknownSni: cm.TlsSettings.RejectUnknownSni == "1",
	}
	if cm.CertInfo.CertMode == "dns" && cm.TlsSettings.DNSEnv != "" {
		envs := strings.Split(cm.TlsSettings.DNSEnv, ",")
		for _, env := range envs {
			kv := strings.SplitN(env, "=", 2)
			if len(kv) == 2 {
				cm.CertInfo.DNSEnv[kv[0]] = kv[1]
			}
		}
	}
	for key, value := range cm.TlsSettings.DNSEnvMap {
		cm.CertInfo.DNSEnv[key] = value
	}
	if cm.CertInfo.CertMode == "remote" {
		cm.CertInfo.TlsCert = cm.TlsSettings.TlsCert
		cm.CertInfo.TlsKey = cm.TlsSettings.TlsKey
	}
	if cm.ServerPort < 1 || cm.ServerPort > 65535 {
		return nil, fmt.Errorf("invalid node port")
	}
	if cm.BaseConfig == nil {
		cm.BaseConfig = &BaseConfig{}
	}
	// set interval
	node.PushInterval = intervalToTime(cm.BaseConfig.PushInterval)
	node.PullInterval = intervalToTime(cm.BaseConfig.PullInterval)

	node.Common = cm

	return node, nil
}

func intervalToTime(i interface{}) time.Duration {
	var seconds int
	switch value := i.(type) {
	case int:
		seconds = value
	case float64:
		seconds = int(value)
	case string:
		seconds, _ = strconv.Atoi(value)
	}
	if seconds <= 0 || seconds > 86400 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

func (t TlsSettings) EffectiveServerNames() []string {
	if len(t.ServerNames) > 0 {
		return t.ServerNames
	}
	if t.ServerName == "" {
		return nil
	}
	return []string{t.ServerName}
}

func (t TlsSettings) EffectiveShortIds() []string {
	if len(t.ShortIds) > 0 {
		return t.ShortIds
	}
	if t.ShortId == "" {
		return nil
	}
	return []string{t.ShortId}
}

func (t TlsSettings) PrimaryServerName() string {
	serverNames := t.EffectiveServerNames()
	if len(serverNames) == 0 {
		return ""
	}
	return serverNames[0]
}
