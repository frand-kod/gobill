// Package diagnose is the customer status strip ("internet mati"): account, plan, connection, quota and router
// in plain words. Read-only and cheap: a few indexed queries, no call to any router. Text goes through the
// Fmt funcs the caller gives, so this package holds no language catalog.
package diagnose

import (
	"context"
	"fmt"

	"github.com/frand-kod/gobill/internal/clock"
	"github.com/frand-kod/gobill/internal/db"
)

// Item is one check: Short is the short text for the status line, Text the full sentence (the tests read it).
type Item struct{ Label, Cls, State, Short, Text string }

// Diagnosis is the list of checks and the advice that follows from them.
type Diagnosis struct {
	Items  []Item
	Advice []string // one next step per failing check, in check order; "Everything looks normal" when none
}

// Item returns the check with this label, nil when it did not run.
func (d Diagnosis) Item(label string) *Item {
	for i := range d.Items {
		if d.Items[i].Label == label {
			return &d.Items[i]
		}
	}
	return nil
}

// NeedsAction: some item is warn or bad, so the page shows the advice.
func (d Diagnosis) NeedsAction() bool {
	for _, it := range d.Items {
		if it.Cls == "badge-warn" || it.Cls == "badge-bad" {
			return true
		}
	}
	return false
}

// Service needs the database and the caller's text and format funcs: T translates a key and fills its
// verbs, TS formats a unix time, Dur a number of seconds, Bytes a size.
type Service struct {
	Q     *db.Queries
	T     func(key string, args ...any) string
	TS    func(unix int64) string
	Dur   func(sec int64) string
	Bytes func(n int64) string
	Now   clock.Func
	// StaleFor is how long (seconds) a session on a NAS may stay silent before it counts as stale.
	StaleFor func(nas string) int64
}

// Diagnose looks at the customer from the outside in. subs are the customer's subscriptions, online
// is the optional router check already done for the page ("" when off).
func (s *Service) Diagnose(ctx context.Context, c db.Customer, subs []db.Subscription, online string) Diagnosis {
	now := s.Now.Now().Unix()
	t := s.T
	var d Diagnosis
	add := func(label, level, short, text string) {
		it := Item{Label: label, Short: short, Text: text}
		switch level {
		case "ok":
			it.Cls, it.State = "badge-ok", "OK"
		case "warn":
			it.Cls, it.State = "badge-warn", "Warning"
		case "bad":
			it.Cls, it.State = "badge-bad", "Problem"
		default:
			it.Cls, it.State = "badge-muted", "Info"
		}
		d.Items = append(d.Items, it)
	}
	var advice []string // one next step per failing check, in check order
	tip := func(k string, a ...any) { advice = append(advice, t(k, a...)) }

	// account
	if c.Status == "Active" {
		add("Account", "ok", t(c.Status), t("Account is Active"))
	} else {
		add("Account", "bad", t(c.Status), t("Account is %s: the customer cannot log in", t(c.Status)))
		tip("Account is %s: set it to Active under Edit", t(c.Status))
	}

	// plan: the active subscription that ends last
	var sub *db.Subscription
	for i := range subs {
		if subs[i].Status == "active" && (sub == nil || subs[i].ExpiresAt > sub.ExpiresAt) {
			sub = &subs[i]
		}
	}
	var plan db.Plan
	expired := false
	if sub == nil && len(subs) > 0 { // only finished plans: say which one ended
		name := ""
		if p, err := s.Q.GetPlan(ctx, subs[0].PlanID); err == nil {
			name = p.Name
		}
		add("Plan", "bad", t("Ended %s", s.TS(subs[0].ExpiresAt)), t("Plan %s ended on %s", name, s.TS(subs[0].ExpiresAt)))
		tip("Plan ended: press Recharge")
	} else if sub == nil {
		add("Plan", "bad", t("No active plan"), t("No active plan"))
		tip("No plan: press Recharge")
	} else if p, err := s.Q.GetPlan(ctx, sub.PlanID); err != nil {
		sub = nil
		add("Plan", "warn", t("Plan could not be read"), t("Plan could not be read"))
		tip("Plan could not be read: check the plan under Service Plan")
	} else {
		plan = p
		switch {
		case sub.PendingStart == 1: // start_on_first_login: the period starts at the first RADIUS login
			add("Plan", "warn", t("Not started"), t("Plan %s starts at the customer's first login", p.Name))
			tip("Plan has not started: ask the customer to log in once (PPPoE or hotspot) to start the period")
		case sub.Method == "Isolir" && sub.ExpiresAt > now:
			add("Plan", "warn", t("Isolated until %s", s.TS(sub.ExpiresAt)), t("Customer is isolated until %s: internet is limited to the isolir page", s.TS(sub.ExpiresAt)))
			tip("Customer is isolated: press Recharge to end the isolation")
		case sub.ExpiresAt <= now:
			expired = true
			add("Plan", "bad", t("Ended %s", s.TS(sub.ExpiresAt)), t("Plan %s ended on %s", p.Name, s.TS(sub.ExpiresAt)))
			tip("Plan ended: press Recharge")
		default:
			add("Plan", "ok", t("Until %s · %s left", s.TS(sub.ExpiresAt), s.Dur(sub.ExpiresAt-now)), t("Plan %s is active until %s (%s left)", p.Name, s.TS(sub.ExpiresAt), s.Dur(sub.ExpiresAt-now)))
		}
	}

	// connection: RADIUS sessions under the customer's login names
	names := []string{c.Username}
	if c.PppoeUsername != "" && c.PppoeUsername != c.Username {
		names = append(names, c.PppoeUsername)
	}
	var open []db.RadiusSession
	var last *db.RadiusSession
	for _, n := range names {
		if ss, err := s.Q.ListOpenRadiusSessionsByUser(ctx, n); err == nil {
			open = append(open, ss...)
		}
		if ss, err := s.Q.ListRecentRadiusSessionsByUser(ctx, n); err == nil && len(ss) > 0 && (last == nil || ss[0].StartedAt > last.StartedAt) {
			last = &ss[0]
		}
	}
	switch {
	case len(open) > 0:
		x := open[0]
		for _, o := range open[1:] {
			if o.UpdatedAt > x.UpdatedAt {
				x = o
			}
		}
		if age := now - x.UpdatedAt; age > s.StaleFor(x.NasIp) {
			add("Connection", "warn", t("Silent"), t("A session is open but silent for %s (the device may be off)", s.Dur(age)))
			tip("Device is silent: check the customer's modem or ONT is on and the cable is connected")
		} else {
			add("Connection", "ok", t("Online"), t("Online now: IP %s, NAS %s, MAC %s, up for %s (last update %s ago)", x.FramedIp, x.NasIp, x.Mac, s.Dur(now-x.StartedAt), s.Dur(now-x.UpdatedAt)))
		}
	case last != nil:
		end := last.UpdatedAt
		if last.StoppedAt.Valid {
			end = last.StoppedAt.Int64
		}
		add("Connection", "warn", t("Offline"), t("Offline. Last session ended %s (%s ago)", s.TS(end), s.Dur(now-end)))
		tip("Offline: check the customer's modem, ONT and cable, then ask them to reconnect")
	case online != "":
		lvl := map[string]string{"Online": "ok", "Offline": "warn"}[online]
		if lvl == "" {
			lvl = "bad"
		}
		add("Connection", lvl, t(online), t(online))
		switch online {
		case "Offline":
			tip("Device reports offline: check the customer's modem and cable")
		case "Error":
			tip("The online check failed: check the router connection under Routers")
		}
	case sub != nil && plan.Device != "Radius":
		add("Connection", "warn", t("Not seen yet"), t("No connection data for this plan yet"))
		tip("Connection check is off: turn on Check Customer Online under Settings to see whether the customer is online")
	case sub != nil:
		add("Connection", "warn", t("Not seen yet"), t("No RADIUS session recorded for this customer"))
		tip("Customer has never connected: check the PPPoE or hotspot username and password on the device")
	default:
		add("Connection", "muted", t("No data"), t("No RADIUS session recorded for this customer"))
	}

	// quota: same arithmetic as the RADIUS login check
	if sub != nil && plan.Device == "Radius" {
		q, quotaOut := PlanQuota(*sub, plan), false
		if q.HasTime {
			quotaOut = quotaOut || q.Time >= q.TimeLim
			add("Quota", quotaLevel(q.Time, q.TimeLim), pct(q.Time, q.TimeLim), t("Online time used %s of %s", s.Dur(q.Time), s.Dur(q.TimeLim)))
		}
		if q.HasData {
			quotaOut = quotaOut || q.Data >= q.DataLim
			add("Quota", quotaLevel(q.Data, q.DataLim), pct(q.Data, q.DataLim), t("Data used %s of %s", s.Bytes(q.Data), s.Bytes(q.DataLim)))
		}
		if quotaOut && !expired {
			tip("Quota used up: press Recharge for a new plan")
		}
		if (q.HasTime || q.HasData) && s.usageLogAhead(ctx, c, *sub) {
			add("Usage log", "warn", t("Behind"), t("The session log holds more used data or time than the plan counts"))
			tip("The session log holds more used data or time than the plan counts: use More > Recount usage")
		}
	}

	// router
	switch {
	case sub == nil:
	case plan.Device == "Radius":
		add("Router", "ok", t("RADIUS"), t("Served by RADIUS, no direct router link needed"))
	case plan.RouterID.Valid:
		if r, err := s.Q.GetRouter(ctx, plan.RouterID.Int64); err != nil {
			add("Router", "warn", t("Not found"), t("The router of this plan was not found"))
			tip("The router of this plan was not found: check the plan under Service Plan")
		} else if r.Enabled == 0 {
			add("Router", "warn", r.Name, t("Router %s is switched off under Routers", r.Name))
			tip("Router %s is switched off: turn it on under Routers", r.Name)
		} else if !r.Online.Valid {
			add("Router", "warn", r.Name, t("Router %s has not been checked yet", r.Name))
			tip("Router %s has not been checked yet: open Routers and check its connection", r.Name)
		} else if r.Online.Int64 == 0 {
			if r.LastSeenAt.Valid {
				add("Router", "bad", r.Name, t("Router %s is offline (last seen %s)", r.Name, s.TS(r.LastSeenAt.Int64)))
			} else {
				add("Router", "bad", r.Name, t("Router %s is offline (never seen online)", r.Name))
			}
			tip("Router offline: check its power and cables")
		} else {
			add("Router", "ok", r.Name, t("Router %s is online", r.Name))
		}
	}

	if len(advice) == 0 {
		advice = []string{t("Everything looks normal")}
	}
	d.Advice = advice
	return d
}

// pct: share of a limit used, for the short chip text.
func pct(used, lim int64) string {
	if lim <= 0 {
		return ""
	}
	return fmt.Sprintf("%d%%", min(used*100/lim, 100))
}

// QuotaUse is what a limited plan has used since its subscription started, summed over the
// customer's login names. Has* is false when the plan has no such limit (or is not a RADIUS plan).
type QuotaUse struct {
	HasTime, HasData bool
	Time, TimeLim    int64 // seconds
	Data, DataLim    int64 // bytes
}

// PlanQuota: the same sums the RADIUS login check (radius.authorize) uses to reject or cut off a session.
func PlanQuota(sub db.Subscription, plan db.Plan) QuotaUse {
	var q QuotaUse
	if plan.Limited != 1 || plan.Device != "Radius" {
		return q
	}
	lt := plan.LimitType.String
	q.HasTime = (lt == "Time_Limit" || lt == "Both_Limit") && plan.TimeLimit.Valid
	q.HasData = (lt == "Data_Limit" || lt == "Both_Limit") && plan.DataLimit.Valid
	if q.HasTime || q.HasData {
		q.Data, q.Time = sub.UsedInOctets+sub.UsedOutOctets, sub.UsedSeconds
	}
	if q.HasTime {
		q.TimeLim = db.QuotaSeconds(plan.TimeLimit.Int64, plan.TimeUnit.String, sub.QuotaCarrySeconds)
	}
	if q.HasData {
		q.DataLim = db.QuotaBytes(plan.DataLimit.Int64, plan.DataUnit.String, sub.QuotaCarryBytes)
	}
	return q
}

// quotaLevel is ok until 90% is used, warn after, bad at the limit.
func quotaLevel(used, lim int64) string {
	switch {
	case used >= lim:
		return "bad"
	case used*10 >= lim*9:
		return "warn"
	}
	return "ok"
}

// usageLogAhead reports whether the session log holds more data or time than the subscription's
// counters, which Recount usage fixes (accounting missed, e.g. under an older version). The other way
// round is normal: retention and manual cleaning prune the log, never the counters. Small gaps are
// tolerated.
func (s *Service) usageLogAhead(ctx context.Context, c db.Customer, sub db.Subscription) bool {
	bytes, secs, err := db.LogUsage(ctx, s.Q, c, sub.StartedAt)
	if err != nil {
		return false
	}
	used := sub.UsedInOctets + sub.UsedOutOctets
	return (bytes-used > 1<<20 && (bytes-used)*100 > bytes) || secs-sub.UsedSeconds > 600
}
