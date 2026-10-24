package db

import "context"

// QuotaBytes is the data quota of a plan in bytes: its limit (MB or GB) plus the data an early
// renewal rolled over. RADIUS enforcement, the renewal itself and the admin quota chip all use it.
func QuotaBytes(limit int64, unit string, carry int64) int64 {
	b := limit * 1048576
	if unit == "GB" {
		b *= 1024
	}
	return b + carry
}

// QuotaSeconds is the online-time quota of a plan in seconds: its limit (Mins or Hrs) plus the time
// already used when an early renewal granted a fresh allowance (time does not roll over).
func QuotaSeconds(limit int64, unit string, carry int64) int64 {
	t := limit * 60
	if unit == "Hrs" {
		t *= 60
	}
	return t + carry
}

// LoginUsage is the data (bytes) and online time (seconds) a customer used since a subscription
// started, over both RADIUS login names (username and pppoe_username). RADIUS authorize, renewal
// and the admin/portal quota views all use it so they always agree.
func LoginUsage(ctx context.Context, q *Queries, c Customer, since int64) (bytes, seconds int64, err error) {
	names := []string{c.Username}
	if c.PppoeUsername != "" && c.PppoeUsername != c.Username {
		names = append(names, c.PppoeUsername)
	}
	for _, n := range names {
		b, err := q.SumRadiusUsage(ctx, SumRadiusUsageParams{Username: n, StartedAt: since})
		if err != nil {
			return 0, 0, err
		}
		s, err := q.SumRadiusSessionTime(ctx, SumRadiusSessionTimeParams{Username: n, StartedAt: since})
		if err != nil {
			return 0, 0, err
		}
		bytes, seconds = bytes+b, seconds+s
	}
	return bytes, seconds, nil
}
