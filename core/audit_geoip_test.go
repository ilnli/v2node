package core

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

func TestAuditGeoIPCountries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", dir)
	cidr := func(value string) *geodata.CIDR {
		p := netip.MustParsePrefix(value)
		return &geodata.CIDR{Ip: p.Addr().AsSlice(), Prefix: uint32(p.Bits())}
	}
	data, err := proto.Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{
		{Code: "CN", Cidr: []*geodata.CIDR{cidr("192.0.2.0/24"), cidr("2001:db8:1::/48")}},
		{Code: "us", Cidr: []*geodata.CIDR{cidr("192.0.2.128/25"), cidr("2001:db8:1:1::/64")}},
		{Code: "PRIVATE", Cidr: []*geodata.CIDR{cidr("10.0.0.0/8")}},
		{Code: "1X", Cidr: []*geodata.CIDR{cidr("198.51.100.0/24")}},
		{Code: "ZZ", ReverseMatch: true, Cidr: []*geodata.CIDR{cidr("203.0.113.0/24")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), data, 0600); err != nil {
		t.Fatal(err)
	}
	index, err := loadAuditGeoIP()
	if err != nil {
		t.Fatal(err)
	}
	// The loaded index must keep working without reading the asset per connection.
	if err := os.Remove(filepath.Join(dir, "geoip.dat")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ ip, country string }{
		{"192.0.2.8", "CN"},
		{"::ffff:192.0.2.8", "CN"},
		{"192.0.2.200", "US"},
		{"2001:db8:1::8", "CN"},
		{"2001:db8:1:1::8", "US"},
		{"2001:db8:2::8", ""},
		{"10.1.2.3", ""},
		{"198.51.100.8", ""},
		{"203.0.113.8", ""},
		{"cn.example", ""},
		{"", ""},
	} {
		t.Run(tc.ip, func(t *testing.T) {
			if got := index.country(tc.ip); got != tc.country {
				t.Fatalf("country(%q)=%q, want %q", tc.ip, got, tc.country)
			}
		})
	}
}

func TestAuditGeoIPUnavailable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", dir)
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), []byte("invalid protobuf"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		index, err := loadAuditGeoIP()
		if err == nil {
			t.Fatal("missing or corrupt GeoIP must report its load error")
		}
		if got := index.country("192.0.2.8"); got != "" {
			t.Fatalf("unavailable GeoIP country=%q, want unknown", got)
		}
	}
}
