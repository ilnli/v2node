package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordBlockReasonSurvivesCloneAndSpool(t *testing.T) {
	const input = `{"user_id":1,"node_id":2,"timestamp_ms":1,"destination":"blocked.example","port":443,"network":"tcp","outcome":"blocked","block_reason":"IP 拦截规则：geoip:cn"}`
	var record Record
	if err := json.Unmarshal([]byte(input), &record); err != nil {
		t.Fatal(err)
	}
	copy, ok := record.Clone()
	if !ok {
		t.Fatal("valid block reason rejected")
	}
	dir := t.TempDir()
	options := Options{Directory: dir, MemoryBytes: 1, DiskBytes: 8192}
	q, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Add(Batch{ID: "reason", Target: "node", Records: []Record{copy}}) {
		t.Fatal("record rejected by spool")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	batch, ok := q.Peek()
	if !ok || len(batch.Records) != 1 {
		t.Fatal("record lost after reopening spool")
	}
	data, err := json.Marshal(batch.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["block_reason"] != "IP 拦截规则：geoip:cn" {
		t.Fatalf("reason lost after clone/spool: %s", data)
	}
}

func TestRecordBlockReasonByteLimit(t *testing.T) {
	for _, reason := range []string{strings.Repeat("x", 512), strings.Repeat("界", 171)} {
		data, err := json.Marshal(map[string]any{"user_id": 1, "node_id": 2, "timestamp_ms": 1,
			"destination": "blocked.example", "port": 443, "network": "tcp", "outcome": "blocked", "block_reason": reason})
		if err != nil {
			t.Fatal(err)
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if _, ok := record.Clone(); ok != (len(reason) <= 512) {
			t.Fatalf("reason with %d bytes accepted=%v", len(reason), ok)
		}
	}
	legacy := Record{UserID: 1, NodeID: 2, Timestamp: 1, Destination: "old.example", Port: 443, Network: "tcp"}
	data, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(data), "block_reason") {
		t.Fatalf("legacy serialization changed: %s (%v)", data, err)
	}
}

func TestRecordOptionalEgressDetails(t *testing.T) {
	base := Record{UserID: 1, NodeID: 2, Timestamp: 1, Destination: "example.com", Port: 443, Network: "tcp", Protocol: "vless"}
	if _, ok := base.Clone(); !ok {
		t.Fatal("legacy record rejected")
	}
	base.DestinationIP = "::ffff:192.0.2.8"
	base.CountryCode = "CN"
	base.Outcome = "blocked"
	copy, ok := base.Clone()
	if !ok || copy.DestinationIP != "192.0.2.8" || copy.CountryCode != "CN" || copy.Outcome != "blocked" {
		t.Fatal(copy)
	}
	for _, invalid := range []Record{
		{DestinationIP: "example.com"}, {DestinationIP: "fe80::1%eth0"}, {CountryCode: "CHN"}, {CountryCode: "cn"}, {Outcome: "success"},
	} {
		r := base
		if invalid.DestinationIP != "" {
			r.DestinationIP = invalid.DestinationIP
		}
		if invalid.CountryCode != "" {
			r.CountryCode = invalid.CountryCode
		}
		if invalid.Outcome != "" {
			r.Outcome = invalid.Outcome
		}
		if _, ok := r.Clone(); ok {
			t.Fatalf("invalid details accepted: %+v", invalid)
		}
	}
}
