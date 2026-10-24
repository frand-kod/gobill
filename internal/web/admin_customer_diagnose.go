package web

// Customer status strip ("internet mati"): account, plan, connection, quota and router in plain words.
// Read-only and cheap: a few indexed queries, no call to any router.

import (
	"context"
	"fmt"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
)

// diagItem is one status chip: Short is the chip text, Text the full sentence (the tests read it).
type diagItem struct{ Label, Cls, State, Short, Text string }

type diagnosis struct {
	Items  []diagItem
	Advice []string // one next step per failing check, in check order; "Everything looks normal" when none
}

// NeedsAction: some item is warn or bad, so the page shows the advice.
func (d diagnosis) NeedsAction() bool {
	for _, it := range d.Items {
		if it.Cls == "badge-warn" || it.Cls == "badge-bad" {
			return true
		}
	}
	return false
}

// diagnose looks at the customer from the outside in. subs are the customer's subscriptions, online
// is the optional router check already done for the page ("" when off).
func (s *Server) diagnose(ctx context.Context, c db.Customer, subs []db.Subscription, online string) diagnosis {
	now := time.Now().Unix()
	t := func(k string, a ...any) string {
		k = s.catalog.T(s.language(), k)
		if len(a) == 0 {
			return k
		}
		return fmt.Sprintf(k, a...)
	}
	var d diagnosis
	add := func(label, level, short, text string) {
		it := diagItem{Label: label, Short: short, Text: text}
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
		if p, err := s.queries.GetPlan(ctx, subs[0].PlanID); err == nil {
			name = p.Name
		}
		add("Plan", "bad", t("Ended %s", s.ts(subs[0].ExpiresAt)), t("Plan %s ended on %s", name, s.ts(subs[0].ExpiresAt)))
		tip("Plan ended: press Recharge")
	} else if sub == nil {
		add("Plan", "bad", t("No active plan"), t("No active plan"))
		tip("No plan: press Recharge")
	} else if p, err := s.queries.GetPlan(ctx, sub.PlanID); err != nil {
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
			add("Plan", "warn", t("Isolated until %s", s.ts(sub.ExpiresAt)), t("Customer is isolated until %s: internet is limited to the isolir page", s.ts(sub.ExpiresAt)))
			tip("Customer is isolated: press Recharge to end the isolation")
		case sub.ExpiresAt <= now:
			expired = true
			add("Plan", "bad", t("Ended %s", s.ts(sub.ExpiresAt)), t("Plan %s ended on %s", p.Name, s.ts(sub.ExpiresAt)))
			tip("Plan ended: press Recharge")
		default:
			add("Plan", "ok", t("Until %s · %s left", s.ts(sub.ExpiresAt), humanDur(sub.ExpiresAt-now)), t("Plan %s is active until %s (%s left)", p.Name, s.ts(sub.ExpiresAt), humanDur(sub.ExpiresAt-now)))
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
		if ss, err := s.queries.ListOpenRadiusSessionsByUser(ctx, n); err == nil {
			open = append(open, ss...)
		}
		if ss, err := s.queries.ListRecentRadiusSessionsByUser(ctx, n); err == nil && len(ss) > 0 && (last == nil || ss[0].StartedAt > last.StartedAt) {
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
		if age := now - x.UpdatedAt; age > radius.StaleFor(x.NasIp) {
			add("Connection", "warn", t("Silent"), t("A session is open but silent for %s (the device may be off)", humanDur(age)))
			tip("Device is silent: check the customer's modem or ONT is on and the cable is connected")
		} else {
			add("Connection", "ok", t("Online"), t("Online now: IP %s, NAS %s, MAC %s, up for %s (last update %s ago)", x.FramedIp, x.NasIp, x.Mac, humanDur(now-x.StartedAt), humanDur(now-x.UpdatedAt)))
		}
	case last != nil:
		end := last.UpdatedAt
		if last.StoppedAt.Valid {
			end = last.StoppedAt.Int64
		}
		add("Connection", "warn", t("Offline"), t("Offline. Last session ended %s (%s ago)", s.ts(end), humanDur(now-end)))
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
		q, quotaOut := s.planQuota(ctx, c, *sub, plan), false
		if q.HasTime {
			quotaOut = quotaOut || q.Time >= q.TimeLim
			add("Quota", quotaLevel(q.Time, q.TimeLim), pct(q.Time, q.TimeLim), t("Online time used %s of %s", humanDur(q.Time), humanDur(q.TimeLim)))
		}
		if q.HasData {
			quotaOut = quotaOut || q.Data >= q.DataLim
			add("Quota", quotaLevel(q.Data, q.DataLim), pct(q.Data, q.DataLim), t("Data used %s of %s", humanBytes(q.Data), humanBytes(q.DataLim)))
		}
		if quotaOut && !expired {
			tip("Quota used up: press Recharge for a new plan")
		}
	}

	// router
	switch {
	case sub == nil:
	case plan.Device == "Radius":
		add("Router", "ok", t("RADIUS"), t("Served by RADIUS, no direct router link needed"))
	case plan.RouterID.Valid:
		if r, err := s.queries.GetRouter(ctx, plan.RouterID.Int64); err != nil {
			add("Router", "warn", t("Not found"), t("The router of this plan was not found"))
			tip("The router of this plan was not found: check the plan under Service Plan")
		} else if r.Enabled == 0 {
			add("Router", "warn", t("Off"), t("Router %s is switched off under Routers", r.Name))
			tip("Router %s is switched off: turn it on under Routers", r.Name)
		} else if !r.Online.Valid {
			add("Router", "warn", t("Not checked"), t("Router %s has not been checked yet", r.Name))
			tip("Router %s has not been checked yet: open Routers and check its connection", r.Name)
		} else if r.Online.Int64 == 0 {
			if r.LastSeenAt.Valid {
				add("Router", "bad", t("Offline"), t("Router %s is offline (last seen %s)", r.Name, s.ts(r.LastSeenAt.Int64)))
			} else {
				add("Router", "bad", t("Offline"), t("Router %s is offline (never seen online)", r.Name))
			}
			tip("Router offline: check its power and cables")
		} else {
			add("Router", "ok", t("Online"), t("Router %s is online", r.Name))
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

// quotaUse is what a limited plan has used since its subscription started, summed over the
// customer's login names. Has* is false when the plan has no such limit (or is not a RADIUS plan).
type quotaUse struct {
	HasTime, HasData bool
	Time, TimeLim    int64 // seconds
	Data, DataLim    int64 // bytes
}

// planQuota: the same sums the RADIUS login check (radius.authorize) uses to reject or cut off a session.
func (s *Server) planQuota(ctx context.Context, c db.Customer, sub db.Subscription, plan db.Plan) quotaUse {
	var q quotaUse
	if plan.Limited != 1 || plan.Device != "Radius" {
		return q
	}
	lt := plan.LimitType.String
	q.HasTime = (lt == "Time_Limit" || lt == "Both_Limit") && plan.TimeLimit.Valid
	q.HasData = (lt == "Data_Limit" || lt == "Both_Limit") && plan.DataLimit.Valid
	if q.HasTime || q.HasData {
		q.Data, q.Time, _ = db.LoginUsage(ctx, s.queries, c, sub.StartedAt)
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
