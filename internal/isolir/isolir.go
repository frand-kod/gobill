// Package isolir holds the ISOLIR (restricted access for expired customers) defaults and the
// helpers shared by billing, RADIUS and the web layer.
package isolir

import (
	"context"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

const (
	PlanName        = "Isolir"
	BandwidthName   = "Isolir"
	legacyBWName    = "Isolir 64k" // name before v0.1.11; renamed when the isolir speed is saved
	DefaultAddrList = "isolir"
	DefaultDays     = 30
	// DefaultAllowHosts is the allow-list used when isolir_allow_hosts was never saved: WhatsApp, so
	// the customer can still contact the admin.
	DefaultAllowHosts = "*.whatsapp.net\n*.whatsapp.com\nwhatsapp.com\nwhatsapp.net\ng.whatsapp.net\nmmg.whatsapp.net\nwa.me"
)

var hostRe = regexp.MustCompile(`^(\*\.)?[a-z0-9-]+(\.[a-z0-9-]+)*$`)

// HostLines splits an allow-list into lowercase hosts, one per line. Blank lines are dropped.
func HostLines(v string) []string {
	var out []string
	for _, line := range strings.Split(v, "\n") {
		if h := strings.ToLower(strings.TrimSpace(line)); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// ValidHost reports whether h (lowercase) is a hostname or a *. wildcard of one, such as wa.me or
// *.whatsapp.net.
func ValidHost(h string) bool { return hostRe.MatchString(h) }

// Forever is the expiry of an isolation with isolir_days = 0 (isolated until paid).
var Forever = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

// Defaults are shown in the settings fields when a key was never saved. isolir_wa falls back to
// the company phone (see Value).
var Defaults = map[string]string{
	"isolir_enabled":      "no",
	"isolir_plan_id":      "",
	"isolir_days":         "30",
	"isolir_title":        "Paket internet Anda sudah habis",
	"isolir_message":      "Internet Anda dibatasi sampai paket diperpanjang. WhatsApp tetap bisa dipakai untuk menghubungi kami.",
	"isolir_wa":           "",
	"isolir_show_voucher": "yes",
	"isolir_address_list": DefaultAddrList,
	"isolir_allow_hosts":  DefaultAllowHosts,
	"isolir_hotspot_url":  "",
	"isolir_mode":         "default",
	"isolir_html":         "",
}

// Value is the effective setting: the stored value, or the default when never saved.
// An empty isolir_wa means the company phone.
func Value(st map[string]string, key string) string {
	v, ok := st[key]
	if !ok {
		if key == "isolir_wa" {
			return st["phone"]
		}
		return Defaults[key]
	}
	if key == "isolir_wa" && v == "" {
		return st["phone"]
	}
	return v
}

// Days parses isolir_days; unset or invalid is DefaultDays, negative is 0.
func Days(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return DefaultDays
	}
	return max(n, 0)
}

// Until is the end of an isolation starting now: days later, or Forever when days is 0.
func Until(now time.Time, days int) time.Time {
	if days <= 0 {
		return Forever
	}
	return now.AddDate(0, 0, days)
}

// PlanID parses isolir_plan_id, 0 when unset.
func PlanID(v string) int64 {
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// EnsurePlan returns the isolir plan id. It keeps the stored plan when it exists, else reuses a
// plan named PlanName on the Radius device, else creates it with a 64k/64k bandwidth. The plan is
// disabled so the portal does not sell it. The id is stored in isolir_plan_id.
func EnsurePlan(ctx context.Context, q *db.Queries) (int64, error) {
	if row, err := q.GetSetting(ctx, "isolir_plan_id"); err == nil {
		if id := PlanID(row); id > 0 {
			if _, err := q.GetPlan(ctx, id); err == nil {
				return id, nil
			}
		}
	}
	id, err := findOrCreatePlan(ctx, q)
	if err != nil {
		return 0, err
	}
	return id, q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "isolir_plan_id", Value: strconv.FormatInt(id, 10)})
}

func findOrCreatePlan(ctx context.Context, q *db.Queries) (int64, error) {
	plans, err := q.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
	if err != nil {
		return 0, err
	}
	for _, p := range plans {
		if p.Name == PlanName && p.Device == "Radius" {
			return p.ID, nil
		}
	}
	bw, err := bandwidth(ctx, q)
	if err != nil {
		return 0, err
	}
	p, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: PlanName, Type: "Hotspot", Billing: "prepaid", Price: 0,
		Validity: 30, ValidityUnit: "Days", Enabled: 0, Limited: 0,
		BandwidthID: sql.NullInt64{Int64: bw, Valid: true}, Device: "Radius"})
	if err != nil {
		return 0, err
	}
	return p.ID, nil
}

// AddressListFor is the Mikrotik-Address-List for a plan: the isolir list when planID is the isolir
// plan, else "". The name is isolir_address_list, DefaultAddrList when unset or empty.
func AddressListFor(ctx context.Context, q *db.Queries, planID int64) string {
	id, err := q.GetSetting(ctx, "isolir_plan_id")
	if err != nil || planID == 0 || PlanID(id) != planID {
		return ""
	}
	if v, err := q.GetSetting(ctx, "isolir_address_list"); err == nil && v != "" {
		return v
	}
	return DefaultAddrList
}

func bandwidth(ctx context.Context, q *db.Queries) (int64, error) {
	bws, err := q.ListBandwidths(ctx, db.ListBandwidthsParams{Limit: 1000})
	if err != nil {
		return 0, err
	}
	for _, b := range bws {
		if b.Name == BandwidthName || b.Name == legacyBWName {
			return b.ID, nil
		}
	}
	b, err := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: BandwidthName, RateDown: 64, RateDownUnit: "Kbps", RateUp: 64, RateUpUnit: "Kbps"})
	return b.ID, err
}
