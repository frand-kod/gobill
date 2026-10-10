package web

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// Expiring soon lists only active subscriptions ending from now to +7 days; the recently expired card lists the
// subscriptions that ended in the last 3 days. Anything older, or ending later, is in neither card.
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
	mk(now.AddDate(0, 0, -5), "expired")   // ended 5 days ago: neither card
	mk(now.Add(-24*time.Hour), "active")   // past due, not yet marked expired: recently expired
	mk(now.Add(-time.Minute*10), "active") // ended 10 minutes ago: recently expired
	w, err := e.srv.widgetData(ctx, now, 0, 0)
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

	body := do(e.h, "GET", "/admin", nil, e.c).Body.String()
	if !strings.Contains(body, "Baru habis (3 hari terakhir)") || !strings.Contains(body, `data-collapse="recent"`) {
		t.Fatal("recently expired card missing")
	}
}

// The network card lists offline devices first, then unknown, then online; the offline row is tinted and marked.
func TestNetworkCardOrderAndTint(t *testing.T) {
	_, h, q, c, _ := healthApp(t, nil)
	mkRouter(t, q, "on-router", 1, "api", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{})
	mkRouter(t, q, "off-router", 1, "api", sql.NullInt64{Int64: 0, Valid: true}, sql.NullInt64{})
	mkRouter(t, q, "parked-router", 0, "api", sql.NullInt64{}, sql.NullInt64{})
	body := do(h, "GET", "/admin", nil, c).Body.String()

	off, unk, on := strings.Index(body, ">off-router<"), strings.Index(body, ">parked-router<"), strings.Index(body, ">on-router<")
	if off < 0 || unk < 0 || on < 0 || !(off < unk && unk < on) {
		t.Fatalf("network rows out of order: off=%d unknown=%d online=%d", off, unk, on)
	}
	if !strings.Contains(body, `<li class="bg-err-bg">`) {
		t.Fatal("offline row is not tinted with the error token")
	}
	if !strings.Contains(body, `<li class="bg-surface-2">`) {
		t.Fatal("unknown row is not neutral")
	}
	if !strings.Contains(body, `text-err-fg">1 offline<`) {
		t.Fatal("header summary does not show the offline count in the error colour")
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
