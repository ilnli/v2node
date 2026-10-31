package core

import (
	"io"
	"net/netip"
	"strings"

	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/platform/filesystem"
	"google.golang.org/protobuf/encoding/protowire"
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
	file, size, err := filesystem.OpenAsset("geoip.dat")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data := make([]byte, size)
	if _, err := io.ReadFull(file, data); err != nil {
		return nil, err
	}
	index := &auditGeoIP{countries: make(map[netip.Prefix]string)}
	var v4Lengths [33]bool
	var v6Lengths [129]bool
	// Decode one country at a time. Keeping the entire protobuf object graph
	// alongside the lookup index can exhaust memory on small VPS hosts.
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		data = data[n:]
		if num != 1 || typ != protowire.BytesType {
			n = protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			data = data[n:]
			continue
		}
		body, n := protowire.ConsumeBytes(data)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		data = data[n:]
		// Decode only the country header first; even a single large country's
		// []*CIDR graph is expensive. CIDRs are consumed individually below.
		var header []byte
		for fields := body; len(fields) > 0; {
			num, typ, n := protowire.ConsumeField(fields)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			if (num == 1 && typ == protowire.BytesType) || (num == 3 && typ == protowire.VarintType) {
				header = append(header, fields[:n]...)
			}
			fields = fields[n:]
		}
		var entry geodata.GeoIP
		if err := proto.Unmarshal(header, &entry); err != nil {
			return nil, err
		}
		code := strings.ToUpper(entry.GetCode())
		isCountry := len(code) == 2 && code[0] >= 'A' && code[0] <= 'Z' && code[1] >= 'A' && code[1] <= 'Z' && !entry.GetReverseMatch()
		var cidr geodata.CIDR
		for len(body) > 0 {
			// The header scan already validated these field boundaries.
			num, typ, n := protowire.ConsumeTag(body)
			body = body[n:]
			if num != 2 || typ != protowire.BytesType {
				body = body[protowire.ConsumeFieldValue(num, typ, body):]
				continue
			}
			raw, n := protowire.ConsumeBytes(body)
			body = body[n:]
			if err := proto.Unmarshal(raw, &cidr); err != nil {
				return nil, err
			}
			if !isCountry {
				continue
			}
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
