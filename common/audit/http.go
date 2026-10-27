package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var reportClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        4, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second,
		MaxResponseHeaderBytes: 8192, DisableCompression: true,
	},
}

type Endpoint struct {
	Target string
	url    string
	client *http.Client
}

func NewEndpoint(base string, nodeID int, token string) (*Endpoint, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || nodeID <= 0 || token == "" {
		return nil, errors.New("invalid audit panel URL, node ID or token")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/server/UniProxy/audit"
	u.RawPath = ""
	identity := sha256.Sum256([]byte(u.String() + "\n" + strconv.Itoa(nodeID)))
	query := u.Query()
	query.Set("node_type", "v2node")
	query.Set("node_id", strconv.Itoa(nodeID))
	query.Set("token", token)
	u.RawQuery = query.Encode()
	return &Endpoint{Target: hex.EncodeToString(identity[:]), url: u.String(), client: reportClient}, nil
}

// Send returns success only when the panel explicitly confirms every record in
// this exact batch. The panel must commit rows and deduplication atomically.
func (e *Endpoint) Send(ctx context.Context, b Batch) error {
	data, err := json.Marshal(b)
	if err != nil {
		return errors.New("encode audit request failed")
	}
	if len(data) > MaxBatchBytes {
		return errors.New("audit batch too large")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(data))
	if err != nil {
		return errors.New("create audit request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "proxynode-audit/1")
	resp, err := e.client.Do(req)
	// net/http errors include request URLs (and therefore node tokens). Never wrap them.
	if err != nil {
		return errors.New("audit transport failed or canceled")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("audit HTTP status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil || len(body) > 8192 {
		return errors.New("invalid or oversized audit response")
	}
	var ack struct {
		Data struct {
			ID       string `json:"batch_id"`
			Accepted int    `json:"accepted"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &ack); err != nil || ack.Data.ID != b.ID || ack.Data.Accepted != len(b.Records) {
		return errors.New("audit acknowledgment missing or mismatched")
	}
	return nil
}
