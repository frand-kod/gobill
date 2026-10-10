package web

// Customer diagnosis card ("internet mati"): account, plan, connection, quota and router in plain words.
// Read-only and cheap: a few indexed queries, no call to any router.

import (
	"context"
	"fmt"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
)

type diagItem struct{ Label, Cls, State, Text string }

type diagnosis struct {
	Items  []diagItem
	Advice string
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
	add := func(label, level, text string) {
		it := diagItem{Label: label, Text: text}
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
	var advice []string // first one wins
	tip := func(k string, a ...any) { advice = append(advice, t(k, a...)) }

	// account
	if c.Status == "Active" {
		add("Account", "ok", t("Account is Active"))
	} else {
		add("Account", "bad", t("Account is %s: the customer cannot log in", t(c.Status)))
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
		add("Plan", "bad", t("Plan %s ended on %s", name, s.ts(subs[0].ExpiresAt)))
		tip("Plan ended: press Recharge")
	} else if sub == nil {
		add("Plan", "bad", t("No active plan"))
		tip("No plan: press Recharge")
	} else if p, err := s.queries.GetPlan(ctx, sub.PlanID); err != nil {
		sub = nil
		add("Plan", "warn", t("Plan could not be read"))
	} else {
		plan = p
		if sub.ExpiresAt <= now {
			expired = true
			add("Plan", "bad", t("Plan %s ended on %s", p.Name, s.ts(sub.ExpiresAt)))
			tip("Plan ended: press Recharge")
		} else {
			add("Plan", "ok", t("Plan %s is active until %s (%s left)", p.Name, s.ts(sub.ExpiresAt), humanDur(sub.ExpiresAt-now)))
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
	connected := false
	switch {
	case len(open) > 0:
		x := open[0]
		for _, o := range open[1:] {
			if o.UpdatedAt > x.UpdatedAt {
				x = o
			}
		}
		if age := now - x.UpdatedAt; age > radius.StaleAfter {
			add("Connection", "warn", t("A session is open but silent for %s (the device may be off)", humanDur(age)))
		} else {
			connected = true
			add("Connection", "ok", t("Online now: IP %s, MAC %s, router %s, last update %s ago", x.FramedIp, x.Mac, x.NasIp, humanDur(age)))
		}
	case last != nil:
		seen := max(last.UpdatedAt, last.StoppedAt.Int64)
		add("Connection", "warn", t("Offline. Last seen %s (%s ago)", s.ts(seen), humanDur(now-seen)))
	case online != "":
		lvl := map[string]string{"Online": "ok", "Offline": "warn"}[online]
		if lvl == "" {
			lvl = "bad"
		}
		add("Connection", lvl, t(online))
		connected = online == "Online"
	default:
		add("Connection", "muted", t("No RADIUS session recorded for this customer"))
	}

	// quota: same arithmetic as the RADIUS login check
	if sub != nil && plan.Device == "Radius" {
		q, quotaOut := s.planQuota(ctx, names, *sub, plan), false
		if q.HasTime {
			quotaOut = quotaOut || q.Time >= q.TimeLim
			add("Quota", quotaLevel(q.Time, q.TimeLim), t("Online time used %s of %s", humanDur(q.Time), humanDur(q.TimeLim)))
		}
		if q.HasData {
			quotaOut = quotaOut || q.Data >= q.DataLim
			add("Quota", quotaLevel(q.Data, q.DataLim), t("Data used %s of %s", humanBytes(q.Data), humanBytes(q.DataLim)))
		}
		if quotaOut && !expired {
			tip("Quota used up: press Recharge for a new plan")
		}
	}

	// router
	routerDown := false
	switch {
	case sub == nil:
	case plan.Device == "Radius":
		add("Router", "ok", t("Served by RADIUS, no direct router link needed"))
	case plan.RouterID.Valid:
		if r, err := s.queries.GetRouter(ctx, plan.RouterID.Int64); err != nil {
			add("Router", "warn", t("The router of this plan was not found"))
		} else if r.Enabled == 0 {
			add("Router", "warn", t("Router %s is switched off under Routers", r.Name))
		} else if !r.Online.Valid {
			add("Router", "warn", t("Router %s has not been checked yet", r.Name))
		} else if r.Online.Int64 == 0 {
			routerDown = true
			if r.LastSeenAt.Valid {
				add("Router", "bad", t("Router %s is offline (last seen %s)", r.Name, s.ts(r.LastSeenAt.Int64)))
			} else {
				add("Router", "bad", t("Router %s is offline (never seen online)", r.Name))
			}
			tip("Router offline: check its power and cables")
		} else {
			add("Router", "ok", t("Router %s is online", r.Name))
		}
	}

	switch {
	case len(advice) > 0:
		d.Advice = advice[0]
	case !connected && sub != nil && !routerDown:
		d.Advice = t("Account and plan are fine but the device is not connected: check the customer's modem and cable")
	default:
		d.Advice = t("Everything looks normal")
	}
	return d
}

// quotaUse is what a limited plan has used since its subscription started, summed over the
// customer's login names. Has* is false when the plan has no such limit (or is not a RADIUS plan).
type quotaUse struct {
	HasTime, HasData bool
	Time, TimeLim    int64 // seconds
	Data, DataLim    int64 // bytes
}

// planQuota: the same sums the RADIUS login check (radius.authorize) uses to reject or cut off a session.
func (s *Server) planQuota(ctx context.Context, names []string, sub db.Subscription, plan db.Plan) quotaUse {
	var q quotaUse
	if plan.Limited != 1 || plan.Device != "Radius" {
		return q
	}
	lt := plan.LimitType.String
	if (lt == "Time_Limit" || lt == "Both_Limit") && plan.TimeLimit.Valid {
		q.HasTime, q.TimeLim = true, radius.LimitSeconds(plan.TimeLimit.Int64, plan.TimeUnit.String)
		for _, n := range names {
			u, _ := s.queries.SumRadiusSessionTime(ctx, db.SumRadiusSessionTimeParams{Username: n, StartedAt: sub.StartedAt})
			q.Time += u
		}
	}
	if (lt == "Data_Limit" || lt == "Both_Limit") && plan.DataLimit.Valid {
		q.HasData, q.DataLim = true, radius.LimitBytes(plan.DataLimit.Int64, plan.DataUnit.String)
		for _, n := range names {
			u, _ := s.queries.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: n, StartedAt: sub.StartedAt})
			q.Data += u
		}
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
