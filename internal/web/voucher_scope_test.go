package web

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
)

// Agent and Sales see only their own vouchers on every read path; admins still see all.
func TestVoucherScopeByRole(t *testing.T) {
	e := billApp(t)
	h, q := e.h, e.q
	plan := e.plan(t, "p", "PPPoE", 10000)
	agA, agB := mk(t, q, "agA", "Agent", 0), mk(t, q, "agB", "Agent", 0)
	slA := mk(t, q, "slA", "Sales", agA.ID)
	mk(t, q, "adm", "Admin", 0)
	mkv := func(code string, by int64) {
		if _, err := q.CreateVoucher(t.Context(), db.CreateVoucherParams{Code: code, PlanID: plan.ID, GeneratedBy: sql.NullInt64{Int64: by, Valid: by > 0}}); err != nil {
			t.Fatal(err)
		}
	}
	mkv("AAAONE00", agA.ID)
	mkv("BBBONE00", agB.ID)
	mkv("SSSONE00", slA.ID)
	mkv("OPSONE00", 1)
	for _, b := range []struct {
		ag  db.Admin
		pfx string
	}{{agA, "BATA"}, {agB, "BATB"}} {
		n := 0
		if _, err := e.s.Billing.CreateAgentBatch(t.Context(), billing.BatchParams{AgentID: b.ag.ID, PlanID: plan.ID, Paid: 1, Price: 1, AdminID: 1,
			NewCode: func() (string, error) { n++; return fmt.Sprintf("%s%04d", b.pfx, n), nil }}); err != nil {
			t.Fatal(err)
		}
	}
	all := []string{"AAAONE00", "BBBONE00", "SSSONE00", "OPSONE00", "BATA0001", "BATB0001"}
	cases := []struct {
		who  string
		sees []string
	}{
		{"agA", []string{"AAAONE00", "SSSONE00", "BATA0001"}},
		{"agB", []string{"BBBONE00", "BATB0001"}},
		{"slA", []string{"SSSONE00"}},
		{"adm", all},
		{"alice", all},
	}
	check := func(who, what, body string, sees []string) {
		t.Helper()
		for _, c := range all {
			want := false
			for _, s := range sees {
				want = want || s == c
			}
			if got := strings.Contains(body, c); got != want {
				t.Errorf("%s %s: code %s shown=%v want %v", who, what, c, got, want)
			}
		}
	}
	for _, tc := range cases {
		c := login(t, h, tc.who)
		pages := map[string]string{
			"list":    "/admin/vouchers?per=100",
			"search":  "/admin/vouchers?per=100&q=ONE",
			"print":   "/admin/vouchers/print?limit=100",
			"view":    fmt.Sprintf("/admin/vouchers/view?plan=%d&limit=100", plan.ID),
			"palette": "/admin/search.json?q=" + url.QueryEscape("BAT"),
		}
		for what, path := range pages {
			w := do(h, "GET", path, nil, c)
			wantCode(t, w, 200, tc.who+" "+what)
			sees := tc.sees
			switch what {
			case "search": // q=ONE matches only the *ONE* codes
				sees = nil
				for _, s := range tc.sees {
					if strings.Contains(s, "ONE") {
						sees = append(sees, s)
					}
				}
			case "palette": // q=BAT matches only the batch codes
				sees = nil
				for _, s := range tc.sees {
					if strings.HasPrefix(s, "BAT") {
						sees = append(sees, s)
					}
				}
			}
			check(tc.who, what, w.Body.String(), sees)
		}
	}

	// the count and pagination follow the scope: agent A has 3 rows
	w := do(h, "GET", "/admin/vouchers?per=20", nil, login(t, h, "agA"))
	if n, _ := q.CountVouchers(t.Context(), db.CountVouchersParams{Viewer: agA.ID}); n != 3 || !strings.Contains(w.Body.String(), "3") {
		t.Fatalf("scoped count %d", n)
	}

	// redeem by code: a foreign code answers like an unknown one and stays unused; an own code works
	cA := login(t, h, "agA")
	redeem := func(code string) int {
		return do(h, "POST", "/admin/vouchers/redeem", url.Values{"customer": {"u1"}, "code": {code}}, cA).Code
	}
	for _, code := range []string{"BBBONE00", "BATB0001", "OPSONE00"} {
		if got := redeem(code); got != 422 {
			t.Errorf("agent A redeems foreign %s: %d", code, got)
		}
		if v, _ := q.GetVoucherByCode(t.Context(), code); v.Status != "unused" {
			t.Errorf("%s was used", code)
		}
	}
	if got := redeem("BATA0001"); got != 303 {
		t.Errorf("own batch code: %d", got)
	}
}
