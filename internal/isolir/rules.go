package isolir

// The isolation rules: who is isolated, how an isolation starts and ends, the RADIUS plan of an
// isolated customer and the RouterOS rules. billing, radius and web call these and keep no copy.
// The rules decided in fix/isolir-rules are listed in
// docs/internal/rencana/modularisasi.md.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/settings"
)

// PlanIDIn is the one lookup of isolir_plan_id in a settings map, 0 when unset.
func PlanIDIn(st map[string]string) int64 { return PlanID(st["isolir_plan_id"]) }

// StoredPlanID is isolir_plan_id read through q (a transaction sees its own settings), 0 when unset.
func StoredPlanID(ctx context.Context, q *db.Queries) int64 {
	return PlanID(settings.Get(ctx, q, "isolir_plan_id"))
}

// IsPlan reports whether planID is the isolir plan. The isolir plan is a system plan: it is never
// recharged or assigned by hand, and no subscription uses it. It reads through q, so inside a
// transaction it sees the transaction's settings.
func IsPlan(ctx context.Context, q *db.Queries, planID int64) bool {
	return planID != 0 && StoredPlanID(ctx, q) == planID
}

// Active is the one meaning of "isolated": isolir_enabled is "yes" and the customer has an isolation
// that is running at now (ended_at = 0 and ends_at > now). The plan id is not part of it. st is the
// settings table.
func Active(ctx context.Context, q *db.Queries, st map[string]string, customerID int64, now time.Time) bool {
	if st["isolir_enabled"] != "yes" {
		return false
	}
	_, ok := running(ctx, q, customerID, now)
	return ok
}

func running(ctx context.Context, q *db.Queries, customerID int64, now time.Time) (db.Isolation, bool) {
	iso, err := q.GetRunningIsolation(ctx, db.GetRunningIsolationParams{CustomerID: customerID, Now: now.Unix()})
	return iso, err == nil
}

// Isolate starts the isolation of an expired customer. It runs in the caller's transaction (q).
// It does nothing when isolir_enabled is not "yes", the customer has another active subscription
// or a running isolation. planName is only for the activity log. The caller has already checked the
// plan and the customer status.
func Isolate(ctx context.Context, q *db.Queries, now time.Time, c db.Customer, planName string) error {
	if settings.Get(ctx, q, "isolir_enabled") != "yes" {
		return nil
	}
	days := Days(settings.Get(ctx, q, "isolir_days"))
	subs, err := q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 1000})
	if err != nil {
		return err
	}
	for _, sub := range subs {
		if sub.Status == "active" {
			return nil
		}
	}
	if _, ok := running(ctx, q, c.ID, now); ok { // isolir_enabled was checked above
		return nil
	}
	if _, err := EnsurePlan(ctx, q); err != nil {
		return fmt.Errorf("isolir plan: %w", err)
	}
	// an open row past ends_at (the job has not ended it yet) must not stay next to the new one
	if _, err := q.EndCustomerDueIsolations(ctx, db.EndCustomerDueIsolationsParams{CustomerID: c.ID, Now: now.Unix(), Reason: "expired"}); err != nil {
		return err
	}
	if _, err := q.CreateIsolation(ctx, db.CreateIsolationParams{CustomerID: c.ID, StartedAt: now.Unix(), EndsAt: Until(now, days).Unix()}); err != nil {
		return err
	}
	return q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "system", ActorID: 0, Action: "isolir",
		Description: fmt.Sprintf("%s isolir %d hari (%s)", c.Username, days, planName)})
}

// End ends the customer's running isolations with a reason such as "paid". It reports whether one
// ended. Every open row ends, also one past ends_at.
func End(ctx context.Context, q *db.Queries, now int64, customerID int64, reason string) (bool, error) {
	n, err := q.EndCustomerIsolations(ctx, db.EndCustomerIsolationsParams{CustomerID: customerID, Now: now, Reason: reason})
	return n > 0, err
}

// EndOne ends one isolation by id. It reports whether it ended; a row that already ended is left alone.
func EndOne(ctx context.Context, q *db.Queries, id, now int64, reason string) (bool, error) {
	n, err := q.EndIsolation(ctx, db.EndIsolationParams{ID: id, Now: now, Reason: reason})
	return n > 0, err
}

// ListRunning lists every isolation not yet ended. Rows past ends_at
// that the job has not ended yet are included.
func ListRunning(ctx context.Context, q *db.Queries) ([]db.Isolation, error) {
	return q.ListRunningIsolations(ctx)
}

// PlanFor is the RADIUS plan of a running isolation: the isolir profile and pool, for the window of
// the isolation. ok is false when the customer is not Active, or there is no isolir plan to take the
// profile from. st is the settings table (RADIUS passes its cached copy).
func PlanFor(ctx context.Context, q *db.Queries, st map[string]string, customerID int64, now time.Time) (pl db.GetRadiusPlanRow, ok bool) {
	planID := PlanIDIn(st)
	if planID == 0 || st["isolir_enabled"] != "yes" {
		return pl, false
	}
	iso, ok := running(ctx, q, customerID, now)
	if !ok {
		return pl, false
	}
	p, err := q.GetIsolirPlan(ctx, planID)
	if err != nil {
		slog.Error("radius: isolir plan", "plan", planID, "err", err)
		return pl, false
	}
	return db.GetRadiusPlanRow{StartedAt: iso.StartedAt, ExpiresAt: iso.EndsAt,
		PlanID: p.PlanID, PlanName: p.PlanName, PlanType: p.PlanType, Limited: p.Limited, LimitType: p.LimitType,
		TimeLimit: p.TimeLimit, TimeUnit: p.TimeUnit, DataLimit: p.DataLimit, DataUnit: p.DataUnit, SharedUsers: p.SharedUsers,
		RateDown: p.RateDown, RateDownUnit: p.RateDownUnit, RateUp: p.RateUp, RateUpUnit: p.RateUpUnit, Burst: p.Burst, PoolName: p.PoolName}, true
}

// RouterRules is the one block of RouterOS commands that isolates a client except the
// allow-list. The rules are appended to the forward chain, so they must sit above any general
// accept rule. Every allow-list entry gets a tls-host rule that learns its IPs from the TLS SNI;
// exact hosts and raw IPv4 addresses are also added to the allow address-list up front; an IP gets
// no tls-host rule, because SNI carries names.
func RouterRules(list, host, port string, hosts []string) string {
	allow := list + "-allow"
	var b strings.Builder
	fmt.Fprintf(&b, "/ip firewall nat add chain=dstnat src-address-list=%s dst-address-type=!local protocol=tcp dst-port=80 action=dst-nat to-addresses=%s to-ports=%s\n", list, host, port)
	fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s dst-address=%s action=accept\n", list, host)
	for _, h := range hosts {
		if !strings.HasPrefix(h, "*.") {
			fmt.Fprintf(&b, "/ip firewall address-list add list=%s address=%s comment=\"gobill isolir allow\"\n", allow, h)
		}
	}
	for _, h := range hosts {
		if net.ParseIP(h) != nil {
			continue
		}
		fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s protocol=tcp dst-port=443 tls-host=%s action=add-dst-to-address-list address-list=%s address-list-timeout=1d comment=\"isolir: learn %s\"\n", list, h, allow, h)
	}
	fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s protocol=tcp dst-port=443 tcp-flags=syn action=accept comment=\"isolir: TLS handshake\"\n", list)
	fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s dst-address-list=%s action=accept comment=\"isolir: allowed apps\"\n", list, allow)
	fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s action=drop comment=\"isolir: block the rest\"", list)
	return b.String()
}

// LanIPv4 is the first non-loopback IPv4 address of this host, or a placeholder when there is none.
func LanIPv4() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			return ipn.IP.String()
		}
	}
	return "<IP-LAN-GOBILL>"
}
