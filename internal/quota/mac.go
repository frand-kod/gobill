package quota

import "strings"

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
