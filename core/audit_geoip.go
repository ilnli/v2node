package core

import (
	"io"
	"net/netip"
	"strings"

	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/platform/filesystem"
	"google.golang.org/protobuf/proto"
)

// auditGeoIP is immutable after loading and shared by all audit callbacks.
// Lookup checks only populated prefix lengths, never every network in the file.
type auditGeoIP struct {
	countries map[netip.Prefix]string
	v4Lengths []int
	v6Lengths []int
}

func loadAuditGeoIP() (*auditGeoIP, error) {
	file, _, err := filesystem.OpenAsset("geoip.dat")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var list geodata.GeoIPList
	if err := proto.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	index := &auditGeoIP{countries: make(map[netip.Prefix]string)}
	var v4Lengths [33]bool
	var v6Lengths [129]bool
	for _, entry := range list.Entry {
		code := strings.ToUpper(entry.GetCode())
		if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' || entry.GetReverseMatch() {
			continue
		}
		for _, cidr := range entry.GetCidr() {
			addr, ok := netip.AddrFromSlice(cidr.GetIp())
			if !ok || cidr.GetPrefix() > uint32(addr.BitLen()) {
				continue
			}
			bits := int(cidr.GetPrefix())
			prefix := netip.PrefixFrom(addr, bits).Masked()
			index.countries[prefix] = code
			if addr.Is4() {
				v4Lengths[bits] = true
			} else {
				v6Lengths[bits] = true
			}
		}
	}
	for bits := 128; bits >= 0; bits-- {
		if bits <= 32 && v4Lengths[bits] {
			index.v4Lengths = append(index.v4Lengths, bits)
		}
		if v6Lengths[bits] {
			index.v6Lengths = append(index.v6Lengths, bits)
		}
	}
	return index, nil
}

func (g *auditGeoIP) country(ip string) string {
	if g == nil {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	addr = addr.Unmap()
	lengths := g.v6Lengths
	if addr.Is4() {
		lengths = g.v4Lengths
	}
	for _, bits := range lengths {
		if country := g.countries[netip.PrefixFrom(addr, bits).Masked()]; country != "" {
			return country
		}
	}
	return ""
}
