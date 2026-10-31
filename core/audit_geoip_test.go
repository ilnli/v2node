package core

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/encoding/protowire"
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

func TestAuditGeoIPWireFields(t *testing.T) {
	field := func(num protowire.Number, value []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, num, protowire.BytesType), value)
	}
	cidr, err := proto.Marshal(&geodata.CIDR{Ip: []byte{192, 0, 2, 0}, Prefix: 24})
	if err != nil {
		t.Fatal(err)
	}
	// Protobuf fields need not be ordered. Unknown fields must not hide a
	// later country code or reverse flag, and duplicate prefixes stay last-wins.
	entry := append(field(2, cidr), field(1, []byte("CN"))...)
	entry = append(entry, field(19, []byte("future metadata"))...)
	entry = append(entry, field(1, []byte("US"))...)
	data := append(field(9, []byte("future list field")), field(1, entry)...)
	for _, tc := range []struct {
		name    string
		data    []byte
		country string
		wantErr bool
	}{
		{"reordered fields", data, "US", false},
		{"last country wins", append(field(1, append(field(1, []byte("CN")), field(2, cidr)...)), data...), "US", false},
		{"reverse flag at end", field(1, append(append([]byte{}, entry...), 0x18, 0x01)), "", false},
		{"truncated entry", data[:len(data)-1], "", true},
		{"malformed CIDR", field(1, append(field(1, []byte("CN")), field(2, []byte{0x0a, 0x04, 0xc0})...)), "", true},
		{"malformed excluded CIDR", field(1, append(field(1, []byte("PRIVATE")), field(2, []byte{0x0a, 0x04, 0xc0})...)), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XRAY_LOCATION_ASSET", dir)
			if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			index, err := loadAuditGeoIP()
			if (err != nil) != tc.wantErr {
				t.Fatalf("load error = %v, want error = %v", err, tc.wantErr)
			}
			if got := index.country("192.0.2.8"); got != tc.country {
				t.Fatalf("country = %q, want %q", got, tc.country)
			}
		})
	}
}
