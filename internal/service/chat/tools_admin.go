package chat

import (
	"context"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/service/diagnose"
)

func init() {
	tools = append(tools,
		Tool{Name: "info", Kind: KindAdmin, Roles: rolesCustomers, Run: (*Service).info},
		Tool{Name: "jatuhtempo", Aliases: []string{"expiring"}, Kind: KindAdmin, Roles: rolesCustomers, Run: (*Service).jatuhTempo},
		Tool{Name: "router", Aliases: []string{"offline"}, Kind: KindAdmin, Roles: rolesCustomers, Run: (*Service).routerOffline},
	)
}

func (s *Service) day(unix int64) string { return time.Unix(unix, 0).In(s.loc()).Format("2006-01-02") }

func (s *Service) info(ctx context.Context, _ Caller, args string) string {
	if args == "" {
		return s.t("Info: info <username>")
	}
	cu, err := s.Q.GetCustomerByUsername(ctx, strings.TrimSpace(args))
	if err != nil {
		return s.t("No customer found.")
	}
	phone := cu.Phone
	if phone == "" {
		phone = "-"
	}
	lines := []string{cu.Username + " - " + cu.Fullname, s.t("Status: %s", cu.Status), s.t("Phone: %s", phone)}
	now := s.now()
	st := map[string]string{}
	if v, err := s.Q.GetSetting(ctx, "isolir_enabled"); err == nil {
		st["isolir_enabled"] = v
	}
	if isolir.Active(ctx, s.Q, st, cu.ID, now) {
		lines = append(lines, s.t("Isolated: yes"))
	} else {
		lines = append(lines, s.t("Isolated: no"))
	}
	subs, err := s.Q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: cu.ID, Limit: 20})
	if err != nil {
		return s.failed()
	}
	n := 0
	for _, sub := range subs {
		if sub.Status != "active" {
			continue
		}
		p, err := s.Q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			continue
		}
		n++
		if sub.PendingStart != 0 {
			lines = append(lines, s.t("%s: waiting to start", p.Name))
		} else {
			lines = append(lines, s.t("%s until %s (%d days left)", p.Name, s.day(sub.ExpiresAt), max(0, (sub.ExpiresAt-now.Unix()+86399)/86400)))
		}
		q := diagnose.PlanQuota(sub, p)
		if q.HasData {
			lines = append(lines, s.t("%s data: %s of %s used", p.Name, bytesText(q.Data), bytesText(q.DataLim)))
		}
		if q.HasTime {
			lines = append(lines, s.t("%s time: %s of %s used", p.Name, hoursText(q.Time), hoursText(q.TimeLim)))
		}
	}
	if n == 0 {
		lines = append(lines, s.t("No active package."))
	}
	if open, err := s.Q.ListOpenRadiusSessionsByUser(ctx, cu.Username); err == nil && len(open) > 0 {
		lines = append(lines, s.t("Online, IP %s", open[0].FramedIp))
	} else {
		lines = append(lines, s.t("Offline"))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) nasFresh(ip string) time.Duration {
	if s.NASFresh != nil {
		return s.NASFresh(ip)
	}
	return 15 * time.Minute
}

func (s *Service) jatuhTempo(ctx context.Context, _ Caller, _ string) string {
	now := s.now().Unix()
	l, err := s.Q.ListExpiringSubscriptions(ctx, db.ListExpiringSubscriptionsParams{Since: now, Until: now + 3*86400, PageLimit: 10})
	if err != nil {
		return s.failed()
	}
	if len(l) == 0 {
		return s.t("No package expires within 3 days.")
	}
	lines := []string{s.t("Expiring within 3 days:")}
	for _, x := range l {
		lines = append(lines, x.Username+" - "+x.Fullname+" - "+x.PlanName+" - "+s.day(x.ExpiresAt))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) routerOffline(ctx context.Context, _ Caller, _ string) string {
	routers, e1 := s.Q.ListOfflineRouters(ctx)
	nas, e2 := s.Q.ListNAS(ctx)
	stats, e3 := s.Q.ListNASPacketStats(ctx)
	if e1 != nil || e2 != nil || e3 != nil {
		return s.failed()
	}
	var names []string
	for _, r := range routers {
		names = append(names, "Router "+r.Name)
	}
	seen := make(map[string]int64, len(stats))
	for _, x := range stats {
		seen[x.NasIp] = x.LastSeen
	}
	now := s.now()
	for _, n := range nas {
		// same rule as the network health page: seen once, but not within the fresh window
		if last, ok := seen[n.Ip]; ok && now.Sub(time.Unix(last, 0)) > s.nasFresh(n.Ip) {
			names = append(names, "NAS "+n.Name)
		}
	}
	if len(names) == 0 {
		return s.t("All routers and NAS are online.")
	}
	out := s.t("Offline now: %d", len(names))
	if len(names) > 10 {
		names = append(names[:10], s.t("and %d more", len(names)-10))
	}
	return out + "\n" + strings.Join(names, "\n")
}
