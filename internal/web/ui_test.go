package web

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func TestDashboardTiles(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	p := e.plan(t, "p1", "PPPoE", 25000)
	now := time.Now().Unix()
	if _, err := e.q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: "INV1", CustomerID: e.cust.ID,
		PlanID: sql.NullInt64{Int64: p.ID, Valid: true}, Username: "u1", PlanName: "p1", Type: "PPPoE", Price: 25000, Method: "Cash",
		PeriodStart: now, PeriodEnd: now + 86400}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: p.ID, RouterID: e.rt,
		Type: "PPPoE", StartedAt: now, ExpiresAt: now + 86400, Method: "Cash"}); err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", "/admin", nil, e.c)
	body := w.Body.String()
	for _, want := range []string{
		`id="tile-today">Rp 25.000<`, `id="tile-month">Rp 25.000<`, `id="tile-subs">1 / 0<`, `id="tile-customers">1<`,
		`data-chart="bar"`, `data-money="1"`, `25000`, // sales series; the month position depends on the date
	} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q (status %d)", want, w.Code)
		}
	}
}

func TestPlanFormSections(t *testing.T) {
	e := billApp(t)
	w := do(e.h, "GET", "/admin/plans/new", nil, e.c)
	body := w.Body.String()
	for _, want := range []string{"Dasar", "Harga &amp; masa berlaku", "Batasan", "Jaringan &amp; perangkat", "Prabayar", "Pascabayar",
		`x-model="type"`, `x-show="limited`, "Satuan Masa Berlaku"} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("plan form missing %q (status %d)", want, w.Code)
		}
	}
	if strings.Contains(body, "Validity Unit") || strings.Contains(body, ">prepaid<") {
		t.Error("English leftovers in Indonesian plan form")
	}
}
