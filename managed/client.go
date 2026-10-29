// Package managed implements the panel-owned host control plane.
package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

type Certificate struct {
	ID            int               `json:"id"`
	Revision      string            `json:"revision"`
	Mode          string            `json:"mode"`
	Domain        string            `json:"domain"`
	Email         string            `json:"email"`
	Provider      string            `json:"provider"`
	DNSEnv        map[string]string `json:"dns_env"`
	PEM           string            `json:"certificate"`
	PrivateKey    string            `json:"private_key"`
	ACMEDirectory string            `json:"acme_directory"`
}

type Gateway struct {
	Enabled     bool   `json:"enabled"`
	Port        int    `json:"port"`
	Host        string `json:"host"`
	Path        string `json:"path"`
	BackendPort int    `json:"backend_port"`
}

type Node struct {
	ID          int             `json:"node_id"`
	Enabled     bool            `json:"enabled"`
	Config      json.RawMessage `json:"config"`
	Certificate *Certificate    `json:"certificate"`
	Gateway     *Gateway        `json:"gateway"`
}

type Command struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Version string `json:"version"`
}

type Manifest struct {
	HostID       int    `json:"host_id"`
	Revision     string `json:"revision"`
	PollInterval int    `json:"poll_interval"`
	Enabled      bool   `json:"enabled"`
	Nodes        []Node `json:"nodes"`
	Audit        struct {
		Enabled   bool  `json:"enabled"`
		MemoryMiB int64 `json:"memory_mib"`
		DiskMiB   int64 `json:"disk_mib"`
	} `json:"audit"`
	Command *Command `json:"command"`
}

type Client struct {
	base   string
	key    string
	hostID int
	http   *http.Client
}

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("panel HTTP status %d", e.Status) }

func NewClient(base string, hostID int, key string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || hostID < 1 || !strings.HasPrefix(key, "pn_host_") {
		return nil, errors.New("invalid managed host identity")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))) {
		return nil, errors.New("managed panel URL requires HTTPS (HTTP is allowed only on loopback)")
	}
	return &Client{base: strings.TrimRight(base, "/"), key: key, hostID: hostID, http: &http.Client{
		Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) call(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return errors.New("cannot encode agent request")
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return errors.New("cannot create agent request")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("panel unavailable or request canceled")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 {
		return errors.New("invalid or oversized panel response")
	}
	if output == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || len(envelope.Data) == 0 || json.Unmarshal(envelope.Data, output) != nil {
		return errors.New("invalid panel response")
	}
	return nil
}

func (c *Client) Enroll(ctx context.Context, token, version string) error {
	return c.call(ctx, http.MethodPost, "/api/v2/agent/enroll", map[string]any{
		"host_id": c.hostID, "enrollment_token": token, "agent_key": c.key,
		"facts": map[string]any{"agent_version": version, "os": runtime.GOOS, "arch": runtime.GOARCH,
			"protocols": []string{"vless", "vmess", "trojan", "shadowsocks", "hysteria2", "tuic", "anytls"}},
	}, nil)
}

func (c *Client) Desired(ctx context.Context) (Manifest, error) {
	var result Manifest
	err := c.call(ctx, http.MethodGet, "/api/v2/agent/desired", nil, &result)
	if err == nil && (result.HostID != c.hostID || result.Revision == "") {
		err = errors.New("manifest does not match host identity")
	}
	return result, err
}

func (c *Client) Report(ctx context.Context, status any) error {
	return c.call(ctx, http.MethodPost, "/api/v2/agent/report", status, nil)
}
