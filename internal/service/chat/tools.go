package chat

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/money"
	"github.com/frand-kod/gobill/internal/service/diagnose"
)

// Admin roles that may read customer data and money. Agent and Sales are left out: they only see
// their own customers in the web UI and the tools do not scope yet.
var (
	rolesCustomers = []string{"SuperAdmin", "Admin"}
	rolesSummary   = []string{"SuperAdmin", "Admin", "Report"}
)

// tools is the whole list. Add a tool here and it shows up in bantuan.
var tools []Tool

// set in init: bantuan reads the list, so a plain initialiser would be a cycle.
func init() {
	tools = []Tool{
		{Name: "cek", Aliases: []string{"status", "info"}, Kind: KindCustomer, Run: (*Service).cek},
		{Name: "tagihan", Aliases: []string{"bill", "invoice"}, Kind: KindCustomer, Run: (*Service).tagihan},
		{Name: "kuota", Aliases: []string{"pemakaian", "quota"}, Kind: KindCustomer, Run: (*Service).kuota},
		{Name: "paket", Aliases: []string{"harga", "plans"}, Kind: KindCustomer, Run: (*Service).paket},
		{Name: "bayar", Aliases: []string{"pay"}, Kind: KindCustomer, Run: (*Service).bayarGateway},
		{Name: "gangguan", Aliases: []string{"lapor", "rusak", "mati", "offline"}, Kind: KindCustomer, Run: (*Service).gangguan},
		{Name: "bantuan", Aliases: []string{"help", "menu", "?"}, Kind: KindCustomer, Run: (*Service).bantuan},

		{Name: "cari", Aliases: []string{"search"}, Kind: KindAdmin, Roles: rolesCustomers, Run: (*Service).cari},
		{Name: "isolir", Aliases: []string{"terisolir"}, Kind: KindAdmin, Roles: rolesCustomers, Run: (*Service).isolirList},
		{Name: "ringkasan", Aliases: []string{"summary", "laporan"}, Kind: KindAdmin, Roles: rolesSummary, Run: (*Service).ringkasan},
		{Name: "bantuan", Aliases: []string{"help", "menu", "?"}, Kind: KindAdmin, Run: (*Service).bantuan},
	}
}

func (s *Service) now() time.Time { return s.Now.Now() }

func (s *Service) bantuan(_ context.Context, c Caller, _ string) string {
	var names []string
	for _, t := range tools {
		if allowed(t, c) && t.Name != "bantuan" {
			names = append(names, "#"+t.Name)
		}
	}
	if c.Kind == KindCustomer {
		return s.menu()
	}
	out := s.t("Commands:") + " " + strings.Join(names, ", ")
	if slices.Contains(names, "cari") {
		out += "\n" + s.t("Search: #cari <name, username or phone>")
	}
	if _, own := c.asCustomer(); own {
		out += "\n\n" + s.t("Your own account:") + "\n" + s.menu()
	}
	return out
}

// menuItems are the customer commands a single digit picks, in menu order.
var menuItems = []struct{ tool, label string }{
	{"cek", "Check account"}, {"bayar", "Pay"}, {"gangguan", "Report a problem"},
	{"kuota", "Data usage"}, {"tagihan", "Bill"}, {"paket", "Packages"},
}

// menu is the numbered customer menu. Commands also work as words, with or without a leading "/".
func (s *Service) menu() string {
	out := s.t("Reply with # and a number, e.g. #1:")
	for i, m := range menuItems {
		out += fmt.Sprintf("\n#%d %s (#%s)", i+1, s.t(m.label), m.tool)
	}
	return out
}

// menuPick turns a lone menu digit from a customer into the command it stands for. Admins keep
// digits for confirmation codes, which are never one digit long.
func menuPick(c Caller, text string) string {
	t := strings.TrimSpace(text)
	if c.Kind != KindCustomer || len(t) != 1 || t[0] < '1' || int(t[0]-'0') > len(menuItems) {
		return text
	}
	return menuItems[t[0]-'1'].tool
}

func (s *Service) customer(ctx context.Context, c Caller) (db.Customer, bool) {
	cu, err := s.Q.GetCustomer(ctx, c.CustomerID)
	return cu, err == nil
}

func (s *Service) failed() string { return s.t("Something went wrong. Please try again later.") }

func (s *Service) cek(ctx context.Context, c Caller, _ string) string {
	cu, ok := s.customer(ctx, c)
	if !ok {
		return s.failed()
	}
	subs, err := s.Q.ListActiveSubscriptionsWithPlan(ctx, cu.ID)
	if err != nil {
		return s.failed()
	}
	lines := []string{s.t("Hello %s. Account status: %s.", cu.Fullname, cu.Status)}
	now := s.now().Unix()
	if iso, err := s.Q.GetRunningIsolation(ctx, db.GetRunningIsolationParams{CustomerID: cu.ID, Now: now}); err == nil && iso.ID != 0 {
		lines = append(lines, s.t("Your service is isolated. Please pay to activate it again."))
	}
	for _, x := range subs {
		switch {
		case x.PendingStart != 0:
			lines = append(lines, s.t("%s: waiting to start", x.PlanName))
		case x.ExpiresAt <= now:
			lines = append(lines, s.t("%s: expired", x.PlanName))
		default:
			lines = append(lines, s.t("%s: %d days left", x.PlanName, (x.ExpiresAt-now+86399)/86400))
		}
	}
	if len(subs) == 0 {
		lines = append(lines, s.t("No active package."))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) tagihan(ctx context.Context, c Caller, _ string) string {
	cu, ok := s.customer(ctx, c)
	if !ok {
		return s.failed()
	}
	out := s.t("Balance: %s", money.Message(cu.Balance))
	pr, err := s.Q.GetPendingPaymentRequestByCustomer(ctx, db.GetPendingPaymentRequestByCustomerParams{
		CustomerID: sql.NullInt64{Int64: cu.ID, Valid: true}, ExpiresAt: s.now().Unix()})
	if err != nil {
		return out + "\n" + s.t("No unpaid bill.")
	}
	name := ""
	if p, err := s.Q.GetPlan(ctx, pr.PlanID); err == nil {
		name = p.Name
	}
	out += "\n" + s.t("Unpaid: %s %s", name, money.Message(pr.Amount))
	if pr.PayUrl != "" {
		out += "\n" + pr.PayUrl
	}
	return out
}

func (s *Service) kuota(ctx context.Context, c Caller, _ string) string {
	subs, err := s.Q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.CustomerID, Limit: 20})
	if err != nil {
		return s.failed()
	}
	var lines []string
	for _, sub := range subs {
		if sub.Status != "active" {
			continue
		}
		p, err := s.Q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			continue
		}
		q := diagnose.PlanQuota(sub, p)
		if q.HasData {
			lines = append(lines, s.t("%s data: %s of %s used", p.Name, bytesText(q.Data), bytesText(q.DataLim)))
		}
		if q.HasTime {
			lines = append(lines, s.t("%s time: %s of %s used", p.Name, hoursText(q.Time), hoursText(q.TimeLim)))
		}
	}
	if len(lines) == 0 {
		return s.t("Your package has no quota limit.")
	}
	return strings.Join(lines, "\n")
}

func (s *Service) paket(ctx context.Context, c Caller, _ string) string {
	cu, ok := s.customer(ctx, c)
	if !ok {
		return s.failed()
	}
	types := []string{"Hotspot", "PPPoE"}
	if cu.ServiceType != "Others" {
		types = []string{cu.ServiceType}
	}
	hide := isolir.StoredPlanID(ctx, s.Q)
	var lines []string
	for _, ty := range types {
		l, err := s.Q.ListEnabledPlansByType(ctx, ty)
		if err != nil {
			return s.failed()
		}
		for _, p := range l {
			if p.Billing == "prepaid" && p.ID != hide && len(lines) < 10 {
				lines = append(lines, fmt.Sprintf("%s: %s / %d %s", p.Name, money.Message(p.Price), p.Validity, s.t(p.ValidityUnit)))
			}
		}
	}
	if len(lines) == 0 {
		return s.t("No packages for sale.")
	}
	return strings.Join(lines, "\n")
}

func (s *Service) bayar(_ context.Context, _ Caller, _ string) string {
	if s.PortalURL == "" {
		return s.t("Please open the customer portal of your provider to pay.")
	}
	return s.t("Pay or buy a package at the customer portal:") + "\n" + s.PortalURL
}

func (s *Service) cari(ctx context.Context, _ Caller, args string) string {
	if args == "" {
		return s.t("Search: cari <name, username or phone>")
	}
	if r := []rune(args); len(r) > 64 {
		args = string(r[:64])
	}
	l, err := s.Q.SearchCustomersContains(ctx, db.SearchCustomersContainsParams{Q: args, Limit: 5})
	if err != nil {
		return s.failed()
	}
	if len(l) == 0 {
		return s.t("No customer found.")
	}
	lines := make([]string, len(l))
	for i, x := range l {
		parts := []string{x.Username, x.Fullname, x.Phone, x.Status}
		if x.Phone == "" {
			parts = []string{x.Username, x.Fullname, x.Status}
		}
		lines[i] = strings.Join(parts, " - ")
	}
	return strings.Join(lines, "\n")
}

func (s *Service) isolirList(ctx context.Context, _ Caller, _ string) string {
	l, err := s.Q.ListRunningIsolations(ctx)
	if err != nil {
		return s.failed()
	}
	if len(l) == 0 {
		return s.t("No customer is isolated.")
	}
	lines := []string{s.t("Isolated now: %d", len(l))}
	for i, x := range l {
		if i == 10 {
			lines = append(lines, s.t("and %d more", len(l)-10))
			break
		}
		name := fmt.Sprint(x.CustomerID)
		if cu, err := s.Q.GetCustomer(ctx, x.CustomerID); err == nil {
			name = cu.Username + " - " + cu.Fullname
		}
		lines = append(lines, name)
	}
	return strings.Join(lines, "\n")
}

func (s *Service) ringkasan(ctx context.Context, _ Caller, _ string) string {
	now := s.now().In(s.loc())
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	active, e1 := s.Q.CountSubscriptionsByStatus(ctx, "active")
	custs, e2 := s.Q.CountCustomers(ctx)
	iso, e3 := s.Q.ListRunningIsolations(ctx)
	inc, e4 := s.Q.SumTransactionsBetween(ctx, db.SumTransactionsBetweenParams{FromTs: day.Unix(), ToTs: day.AddDate(0, 0, 1).Unix()})
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return s.failed()
	}
	return s.t("Summary today:") + "\n" + s.t("Customers: %d", custs) + "\n" + s.t("Active packages: %d", active) + "\n" +
		s.t("Isolated: %d", len(iso)) + "\n" + s.t("Income today: %s", money.Message(inc))
}

func bytesText(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d KB", b>>10)
}

func hoursText(sec int64) string { return fmt.Sprintf("%.1f h", float64(sec)/3600) }
