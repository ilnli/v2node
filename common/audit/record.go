// Package audit collects connection metadata without retaining payloads or credentials.
package audit

import (
	"crypto/rand"
	"encoding/hex"
	"net/netip"
	"strings"
)

const (
	MaxBatchBytes            = 256 << 10
	MaxRecords               = 64
	MaxEntries               = 65536
	DefaultMemoryBytes int64 = 64 << 20
	MaxMemoryBytes     int64 = 128 << 20 // Remaining 128 MiB reserved for ingestion, metadata and working buffers.
	MaxDiskBytes       int64 = 1 << 30
)

type Record struct {
	UserID        int    `json:"user_id"`
	NodeID        int    `json:"node_id"`
	Timestamp     int64  `json:"timestamp_ms"`
	Destination   string `json:"destination"`
	Port          uint16 `json:"port"`
	Network       string `json:"network"`
	Domain        string `json:"domain,omitempty"`
	Protocol      string `json:"protocol"`
	DestinationIP string `json:"destination_ip,omitempty"`
	CountryCode   string `json:"country_code,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
}

// Clone rejects oversize input before copying so packet buffers cannot be retained.
func (r Record) Clone() (Record, bool) {
	if r.UserID <= 0 || r.NodeID <= 0 || r.Timestamp <= 0 || len(r.Destination) == 0 || len(r.Destination) > 253 || len(r.Domain) > 253 || len(r.Protocol) > 16 || (r.Network != "tcp" && r.Network != "udp") {
		return Record{}, false
	}
	if r.DestinationIP != "" {
		ip, err := netip.ParseAddr(r.DestinationIP)
		if err != nil || ip.Zone() != "" {
			return Record{}, false
		}
		r.DestinationIP = ip.Unmap().String()
	}
	if r.CountryCode != "" && (len(r.CountryCode) != 2 || r.CountryCode[0] < 'A' || r.CountryCode[0] > 'Z' || r.CountryCode[1] < 'A' || r.CountryCode[1] > 'Z') {
		return Record{}, false
	}
	switch r.Outcome {
	case "", "allowed", "blocked", "failed", "unknown":
	default:
		return Record{}, false
	}
	r.CountryCode = strings.Clone(r.CountryCode)
	r.Outcome = strings.Clone(r.Outcome)
	r.Destination = strings.Clone(r.Destination)
	r.Domain = strings.Clone(r.Domain)
	r.Protocol = strings.Clone(r.Protocol)
	return r, true
}

type Batch struct {
	ID      string   `json:"batch_id"`
	Target  string   `json:"target"` // Opaque panel/node identity, never a token or URL.
	Records []Record `json:"records"`
}

func newBatch(target string, records []Record) (Batch, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Batch{}, err
	}
	return Batch{ID: hex.EncodeToString(id[:]), Target: target, Records: records}, nil
}
