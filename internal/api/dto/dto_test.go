package dto

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func nn(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }

// Guards future edits: no secret-looking field name or json tag.
// "enc" is matched as a whole snake_case word or a CamelCase "Enc" suffix, so "audience" is fine.
func TestNoSecretFields(t *testing.T) {
	bad := []string{"password", "secret", "hash", "totp", "sha1", "private", "token"}
	check := func(typ, s string) {
		l := strings.ToLower(s)
		for _, b := range bad {
			if strings.Contains(l, b) {
				t.Errorf("%s: %q contains %q", typ, s, b)
			}
		}
		if strings.HasSuffix(s, "Enc") || strings.Contains(s, "Enc") && !strings.Contains(s, "_") {
			t.Errorf("%s: %q looks like an encrypted field", typ, s)
		}
		for _, w := range strings.Split(l, "_") {
			if w == "enc" {
				t.Errorf("%s: %q looks like an encrypted field", typ, s)
			}
		}
	}
	for _, v := range []any{Customer{}, Admin{}, NAS{}, Router{}, Plan{}, Subscription{}, Transaction{}, Voucher{}, PaymentRequest{}} {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			check(rt.Name(), f.Name)
			check(rt.Name(), f.Tag.Get("json"))
		}
	}
}

func TestMappers(t *testing.T) {
	c := FromCustomer(db.Customer{ID: 1, Username: "u", PasswordHash: "x", Balance: 5000, AutoRenewal: 1, BillingDay: nn(5)})
	if c.ID != 1 || c.Balance != 5000 || !c.AutoRenewal || c.BillingDay == nil || *c.BillingDay != 5 || c.CreatedBy != nil {
		t.Errorf("customer %+v", c)
	}
	a := FromAdmin(db.Admin{ID: 2, Role: "admin", TotpEnabled: 1, RootID: nn(1)})
	if a.ID != 2 || a.Role != "admin" || !a.TwoFactor || *a.RootID != 1 || a.LastLoginAt != nil {
		t.Errorf("admin %+v", a)
	}
	n := FromNAS(db.Na{ID: 3, Name: "n", Ip: "10.0.0.1", RequireMessageAuth: 1, Type: "mikrotik"})
	if n.IP != "10.0.0.1" || !n.RequireMessageAuth || n.Type != "mikrotik" {
		t.Errorf("nas %+v", n)
	}
	r := FromRouter(db.Router{ID: 4, Host: "h", Port: 8728, Enabled: 1, Online: nn(0), Tls: 1})
	if r.Port != 8728 || !r.Enabled || r.Online == nil || *r.Online || !r.TLS || r.LastSeenAt != nil {
		t.Errorf("router %+v", r)
	}
	p := FromPlan(db.Plan{ID: 5, Price: 100000, Limited: 1, LimitType: sql.NullString{String: "time", Valid: true}, Enabled: 1, Audience: "all"})
	if p.Price != 100000 || !p.Limited || p.LimitType == nil || *p.LimitType != "time" || p.DataLimit != nil || p.Audience != "all" {
		t.Errorf("plan %+v", p)
	}
	s := FromSubscription(db.Subscription{ID: 6, CustomerID: 1, PlanID: 5, ExpiresAt: 99, PendingStart: 1, UsedSeconds: 7})
	if s.CustomerID != 1 || s.ExpiresAt != 99 || !s.PendingStart || s.UsedSeconds != 7 || s.RouterID != nil {
		t.Errorf("sub %+v", s)
	}
	tx := FromTransaction(db.Transaction{ID: 7, Invoice: "INV1", Price: 25000, CustomerID: nn(1), PeriodEnd: 10})
	if tx.Invoice != "INV1" || tx.Price != 25000 || *tx.CustomerID != 1 || tx.PlanID != nil || tx.PeriodEnd != 10 {
		t.Errorf("tx %+v", tx)
	}
	v := FromVoucher(db.Voucher{ID: 8, Code: "ABC", PlanID: 5, UsedBy: nn(1)})
	if v.Code != "ABC" || *v.UsedBy != 1 || v.UsedAt != nil {
		t.Errorf("voucher %+v", v)
	}
	pr := FromPaymentRequest(db.PaymentRequest{ID: 9, Ref: "R", Amount: 50000, PayUrl: "https://p", PaidAt: nn(3)})
	if pr.Ref != "R" || pr.Amount != 50000 || pr.PayURL != "https://p" || *pr.PaidAt != 3 || pr.CustomerID != nil {
		t.Errorf("pay %+v", pr)
	}
}
