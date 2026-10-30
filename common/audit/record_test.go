package audit

import "testing"

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
