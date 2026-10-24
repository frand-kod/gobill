package db

// QuotaBytes is the data quota of a plan in bytes: its limit (MB or GB) plus the unused bytes an
// early renewal carried over. RADIUS enforcement, the renewal itself and the admin quota chip all use it.
func QuotaBytes(limit int64, unit string, carry int64) int64 {
	b := limit * 1048576
	if unit == "GB" {
		b *= 1024
	}
	return b + carry
}
