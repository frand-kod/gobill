package web

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// Expiring soon lists only active subscriptions ending from now to +7 days; the Expired list (Customers card) lists the
// subscriptions that ended in the last 7 days. Anything older, or ending later, is in neither list.
func TestExpiringSoonWindow(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	p := e.plan(t, "p1", "PPPoE", 25000)
	now := time.Now()
	// one active subscription per customer (partial unique index), so each row gets its own customer
	mk := func(at time.Time, status string) {
		t.Helper()
		cust, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "w" + strconv.Itoa(int(at.Unix()%100000)) + status,
			PasswordHash: "h", Fullname: "W", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: cust.ID, PlanID: p.ID,
			RouterID: sql.NullInt64{Int64: e.rt, Valid: true}, Type: "PPPoE", StartedAt: now.Add(-30 * 24 * time.Hour).Unix(),
			ExpiresAt: at.Unix(), Method: "Cash"})
		if err != nil {
			t.Fatal(err)
		}
		if status != "active" {
			if _, err := e.srv.conn.Exec(`UPDATE subscriptions SET status = ? WHERE id = ?`, status, sub.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk(now.Add(2*time.Hour), "active")     // expiring soon
	mk(now.AddDate(0, 0, 3), "active")     // expiring soon
	mk(now.AddDate(0, 0, 10), "active")    // later than 7 days: neither card
	mk(now.Add(-24*time.Hour), "expired")  // ended yesterday: recently expired
	mk(now.AddDate(0, 0, -8), "expired")   // ended 8 days ago: neither list
	mk(now.Add(-24*time.Hour), "active")   // past due, not yet marked expired: recently expired
	mk(now.Add(-time.Minute*10), "active") // ended 10 minutes ago: recently expired
	w, err := e.srv.widgetData(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Expiring) != 2 {
		t.Fatalf("expiring soon: want 2 rows, got %d", len(w.Expiring))
	}
	for _, r := range w.Expiring {
		if r.Expires == "" {
			t.Fatal("expiring row without a date")
		}
	}
	if len(w.Recent) != 3 {
		t.Fatalf("recently expired: want 3 rows, got %d", len(w.Recent))
	}
	for _, r := range w.Recent {
		if r.Days < 0 || r.Days > 1 {
			t.Fatalf("recently expired row with Days %d, want 0 or 1 (days back from today)", r.Days)
		}
	}

	body := do(e.h, "GET", "/admin", nil, e.c).Body.String()
	if !strings.Contains(body, `data-collapse="customers"`) || !strings.Contains(body, `data-list="expired"`) || !strings.Contains(body, `count('expired')">3</span>`) {
		t.Fatal("expired list of the customers card missing")
	}
}

// The network list (networkCard, on the routers page now) puts offline devices first, then unknown, then online,
// and tints the offline row. The dashboard shows only the Network tile: red when a device is offline.
func TestNetworkCardOrderAndTint(t *testing.T) {
	srv, h, q, c, _ := healthApp(t, nil)
	mkRouter(t, q, "on-router", 1, "api", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{})
	mkRouter(t, q, "off-router", 1, "api", sql.NullInt64{Int64: 0, Valid: true}, sql.NullInt64{})
	mkRouter(t, q, "parked-router", 0, "api", sql.NullInt64{}, sql.NullInt64{})
	card, err := srv.networkCard(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(card.Rows) != 3 || card.Rows[0].Name != "off-router" || card.Rows[1].Name != "parked-router" || card.Rows[2].Name != "on-router" {
		t.Fatalf("network rows out of order: %+v", card.Rows)
	}
	if card.Rows[0].Tint != "bg-err-bg" || card.Rows[1].Tint != "bg-surface-2" {
		t.Fatalf("offline row tint %q, unknown row tint %q", card.Rows[0].Tint, card.Rows[1].Tint)
	}
	body := do(h, "GET", "/admin", nil, c).Body.String()
	if !strings.Contains(body, `class="stat-num text-err-fg" id="tile-network">1/3 `) {
		t.Fatal("network tile is not red with the online count when a device is offline")
	}
}

// Phone list markup: the filter toggle and panel, the status dot in the first cell, and the more-actions menu.
func TestListPhoneControls(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "gold", "PPPoE", 10000)
	now := time.Now().Unix()
	if _, err := e.q.CreateSubscription(t.Context(), db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: p.ID,
		RouterID: sql.NullInt64{Int64: e.rt, Valid: true}, Type: "PPPoE", StartedAt: now, ExpiresAt: now + 86400}); err != nil {
		t.Fatal(err)
	}
	body := do(e.h, "GET", "/admin/subscriptions?status=active", nil, e.c).Body.String()
	for _, want := range []string{
		`data-filter-toggle`, `aria-controls="list-filters"`, `data-filter-panel`, `(1)</span>`,
		`class="me-1.5 inline-flex align-middle sm:hidden"`, `<span class="sr-only">`, `bg-ok-fg`,
		`role="menu"`, `class="btn btn-ghost btn-icon" x-ref="t"`, `name="days"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("subscriptions list missing %q", want)
		}
	}
	// the expiry column stays visible on phones
	if !strings.Contains(body, `<td class="font-medium">`) {
		t.Error("expiry column is not marked primary")
	}
}
