package billing

import (
	"testing"
	"time"
)

var jkt = func() *time.Location {
	l, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		panic(err)
	}
	return l
}()

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, jkt)
	if err != nil {
		panic(err)
	}
	return t
}

// Expected values for Months/Period were produced by running the PHP code (php 8, Asia/Jakarta).
func TestNewExpiry(t *testing.T) {
	tests := []struct {
		name  string
		from  string
		value int
		unit  Unit
		opts  Options
		want  string
	}{
		{"mins fresh (PHP :190)", "2025-01-01 10:00:00", 30, Mins, Options{}, "2025-01-01 10:30:00"},
		{"mins extend crosses midnight (PHP :219)", "2025-01-01 23:50:00", 30, Mins, Options{Extend: true}, "2025-01-02 00:20:00"},
		{"hrs fresh (PHP :186)", "2025-01-01 10:00:00", 5, Hrs, Options{}, "2025-01-01 15:00:00"},
		{"hrs extend (PHP :214)", "2025-01-01 22:00:00", 5, Hrs, Options{Extend: true}, "2025-01-02 03:00:00"},
		{"days fresh keeps time (PHP :182)", "2025-01-01 10:15:20", 7, Days, Options{}, "2025-01-08 10:15:20"},
		{"days extend uses expiry+its time (PHP :210)", "2025-02-25 12:00:00", 7, Days, Options{Extend: true}, "2025-03-04 12:00:00"},
		{"days leap year", "2024-02-28 08:00:00", 2, Days, Options{}, "2024-03-01 08:00:00"},
		{"months normal (PHP :145)", "2025-01-15 09:00:00", 1, Months, Options{}, "2025-02-15 09:00:00"},
		{"months Jan31 overflow -> Mar3 (PHP :145)", "2025-01-31 09:00:00", 1, Months, Options{}, "2025-03-03 09:00:00"},
		{"months Jan31 leap -> Mar2 (PHP :145)", "2024-01-31 09:00:00", 1, Months, Options{}, "2024-03-02 09:00:00"},
		{"months Jan30 -> Mar2 (PHP :145)", "2025-01-30 09:00:00", 1, Months, Options{}, "2025-03-02 09:00:00"},
		{"months Mar31 -> May1 (PHP :145)", "2025-03-31 09:00:00", 1, Months, Options{}, "2025-05-01 09:00:00"},
		{"months leap Feb29 +12 -> Mar1 (PHP :145)", "2024-02-29 09:00:00", 12, Months, Options{}, "2025-03-01 09:00:00"},
		{"months extend year wrap (PHP :202)", "2025-12-31 09:00:00", 2, Months, Options{Extend: true}, "2026-03-03 09:00:00"},
		{"period billing day 1 from Jan31 (PHP :146)", "2025-01-31 14:00:00", 1, Period, Options{BillingDay: 1}, "2025-03-01 23:59:59"},
		{"period billing day 31 from Jan31 (PHP :146)", "2025-01-31 14:00:00", 1, Period, Options{BillingDay: 31}, "2025-03-03 23:59:59"},
		{"period default day 20 (PHP :88-94)", "2025-01-01 00:00:00", 1, Period, Options{}, "2025-01-20 23:59:59"},
		{"period default day 20 mid month", "2025-01-10 08:00:00", 1, Period, Options{}, "2025-01-20 23:59:59"},
		{"period day 5 from Jan25", "2025-01-25 08:00:00", 1, Period, Options{BillingDay: 5}, "2025-02-05 23:59:59"},
		{"period day 31 x2 leap Feb29 (PHP :146)", "2024-02-29 08:00:00", 2, Period, Options{BillingDay: 31}, "2024-05-01 23:59:59"},
		{"period day 1 year wrap", "2025-12-28 08:00:00", 1, Period, Options{BillingDay: 1}, "2026-02-01 23:59:59"},
		{"period 3 months", "2025-03-15 08:00:00", 3, Period, Options{BillingDay: 10}, "2025-06-10 23:59:59"},
		// extension: overflow first, then set day (PHP :206); time is 23:59:00
		{"period extend day 1", "2025-01-31 23:59:00", 1, Period, Options{Extend: true, BillingDay: 1}, "2025-03-01 23:59:00"},
		{"period extend day 20", "2025-01-20 23:59:00", 1, Period, Options{Extend: true, BillingDay: 20}, "2025-02-20 23:59:00"},
		// deviation: PHP stores invalid 2025-04-31; we normalize to May 1
		{"period extend day 31 in 30-day month", "2025-03-10 23:59:00", 1, Period, Options{Extend: true, BillingDay: 31}, "2025-05-01 23:59:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewExpiry(at(tt.from), tt.value, tt.unit, tt.opts)
			if want := at(tt.want); !got.Equal(want) {
				t.Errorf("got %s, want %s", got.Format("2006-01-02 15:04:05"), tt.want)
			}
		})
	}
}

func TestFirstPeriodInvoice(t *testing.T) {
	tests := []struct {
		name          string
		price         int64
		value         int
		start, expiry string
		want          int64
	}{
		{"15 days of 30 (PHP :437-440)", 300000, 1, "2025-01-05 10:00:00", "2025-01-20 23:59:59", 150000},
		{"capped at price (PHP :441)", 300000, 1, "2025-01-01 00:00:00", "2025-02-28 23:59:59", 300000},
		{"two month plan", 600000, 2, "2025-01-05 00:00:00", "2025-01-20 23:59:59", 150000},
	}
	for _, tt := range tests {
		if got := FirstPeriodInvoice(tt.price, tt.value, at(tt.start), at(tt.expiry)); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}
