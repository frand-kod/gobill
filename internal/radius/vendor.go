package radius

// Speed and quota attributes per NAS type (nas.type).

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/vendors/mikrotik"
)

// NAS types. mikrotik is the default and the behaviour before nas.type existed.
const (
	NASMikrotik = "mikrotik"
	NASWISPr    = "wispr"    // WISPr speed + CoovaChilli quota: CoovaChilli on OpenWrt, WISPr controllers
	NASStandard = "standard" // RFC attributes only: the NAS applies its own speed and quota
)

// Vendor ids and attribute numbers, from CoovaChilli src/radius_wispr.h and src/radius_coovachilli.h.
const (
	vendorWISPr          = 14122
	wisprBandwidthMaxUp  = 7 // bits per second
	wisprBandwidthMaxDn  = 8
	vendorCoovaChilli    = 14559
	chilliMaxTotalOctets = 3  // low 32 bits; the user is disconnected at the limit
	chilliMaxTotalGiga   = 23 // high 32 bits
)

// setRate adds the speed limit. rate is rateLimit's "<up><K|M>/<down><K|M>[ burst]"; WISPr has no burst.
func setRate(p *radius.Packet, typ, rate string) {
	switch typ {
	case NASStandard:
	case NASWISPr:
		if up, down, ok := rateBPS(rate); ok {
			addVSA(p, vendorWISPr, wisprBandwidthMaxUp, up)
			addVSA(p, vendorWISPr, wisprBandwidthMaxDn, down)
		}
	default:
		mikrotik.MikrotikRateLimit_SetString(p, rate)
	}
}

// setTotal adds the data left in bytes.
func setTotal(p *radius.Packet, typ string, left uint64) {
	switch typ {
	case NASStandard:
	case NASWISPr:
		addVSA(p, vendorCoovaChilli, chilliMaxTotalOctets, uint32(left))
		addVSA(p, vendorCoovaChilli, chilliMaxTotalGiga, uint32(left>>32))
	default:
		mikrotik.MikrotikTotalLimit_Set(p, mikrotik.MikrotikTotalLimit(uint32(left)))
		mikrotik.MikrotikTotalLimitGigawords_Set(p, mikrotik.MikrotikTotalLimitGigawords(left>>32))
	}
}

// addVSA adds one Vendor-Specific attribute holding a single integer sub-attribute (RFC 2865 §5.26).
func addVSA(p *radius.Packet, vendor uint32, typ byte, v uint32) {
	b := make([]byte, 10)
	binary.BigEndian.PutUint32(b, vendor)
	b[4], b[5] = typ, 6
	binary.BigEndian.PutUint32(b[6:], v)
	p.Add(rfc2865.VendorSpecific_Type, radius.Attribute(b))
}

// rateBPS reads up and down in bits per second from a rateLimit value.
func rateBPS(rate string) (up, down uint32, ok bool) {
	first, _, _ := strings.Cut(strings.TrimSpace(rate), " ")
	u, d, found := strings.Cut(first, "/")
	if !found {
		return 0, 0, false
	}
	up, ok1 := bps(u)
	down, ok2 := bps(d)
	return up, down, ok1 && ok2
}

func bps(s string) (uint32, bool) {
	mul := uint64(1)
	switch {
	case strings.HasSuffix(s, "K"), strings.HasSuffix(s, "k"):
		mul = 1000
	case strings.HasSuffix(s, "M"):
		mul = 1000 * 1000
	case strings.HasSuffix(s, "G"):
		mul = 1000 * 1000 * 1000
	}
	if mul != 1 {
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	return uint32(min(n*mul, math.MaxUint32)), true
}
