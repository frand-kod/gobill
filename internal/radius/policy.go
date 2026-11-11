package radius

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

// Reject messages of the NAS-Port-Type check.
const (
	msgPortHotspot = "Port type mismatch: a Hotspot plan cannot log in on a Virtual (VPN/PPPoE) port"
	msgPortPPPoE   = "Port type mismatch: a PPPoE plan cannot log in on a Wireless-802.11 (hotspot) port"
)

// portMismatch checks the plan type against NAS-Port-Type, only when the NAS sent one: a Hotspot plan
// is refused on a Virtual port (PPP over a tunnel), a PPPoE plan on a Wireless-802.11 port (hotspot).
// Ethernet and anything else pass: MikroTik uses Ethernet for both services.
func portMismatch(planType, portType string) string {
	switch {
	case planType == "Hotspot" && portType == "Virtual":
		return msgPortHotspot
	case planType == "PPPoE" && portType == "Wireless-802.11":
		return msgPortPPPoE
	}
	return ""
}

// msgOutsideWindow starts the reject message of a login outside the plan's login window.
const msgOutsideWindow = "Login not allowed at this time"

// msgIsolated rejects an isolated customer on a NAS without the MikroTik isolir address list.
const msgIsolated = "Account isolated, please pay to reconnect"

// Reject messages of the per-customer binding.
const (
	msgBindMAC  = "Account locked to another device (MAC address does not match)"
	msgBindPort = "Account locked to another port (NAS-Port-Id does not match)"
)

// NormMAC reads a MAC address in any usual notation (AA:BB:.., AA-BB-.., aabb.ccdd.eeff) and returns
// AA:BB:CC:DD:EE:FF, or "" when it is not a MAC.
func NormMAC(s string) string {
	var h []byte
	for _, c := range []byte(strings.ToUpper(s)) {
		switch {
		case c >= '0' && c <= '9', c >= 'A' && c <= 'F':
			h = append(h, c)
		case c == ':' || c == '-' || c == '.':
		default:
			return ""
		}
	}
	if len(h) != 12 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.Write(h[i : i+2])
	}
	return b.String()
}

// bindingMismatch checks the customer's lock to a MAC and/or NAS-Port-Id. A locked value the NAS did
// not send counts as a mismatch: the lock cannot be verified, so it fails closed.
func bindingMismatch(mac, port string, rq AuthRequest) string {
	if mac != "" && NormMAC(rq.MAC) != NormMAC(mac) || mac != "" && NormMAC(rq.MAC) == "" {
		return msgBindMAC
	}
	if port != "" && !strings.EqualFold(strings.TrimSpace(rq.PortID), port) {
		return msgBindPort
	}
	return ""
}

// LimitSeconds converts a plan time limit (Mins or Hrs) to seconds.
func LimitSeconds(n int64, unit string) int64 { return db.QuotaSeconds(n, unit, 0) }

// LimitBytes converts a plan data limit (MB or GB) to bytes.
func LimitBytes(n int64, unit string) int64 {
	return db.QuotaBytes(n, unit, 0)
}

// rateLimit is the Mikrotik-Rate-Limit value of a plan's bandwidth, "" without one.
func rateLimit(up, down sql.NullInt64, upUnit, downUnit, burst sql.NullString) string {
	if !up.Valid || !down.Valid {
		return ""
	}
	r := fmt.Sprintf("%d%s/%d%s", up.Int64, unit(upUnit.String), down.Int64, unit(downUnit.String))
	if b := strings.TrimSpace(burst.String); b != "" {
		r += " " + b
	}
	return r
}

func unit(u string) string {
	if u == "Kbps" {
		return "K"
	}
	return "M"
}
