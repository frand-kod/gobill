package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// A customer whose balance renews the plan is never told it expired (early window or expiry).
func TestExpiredNoticeSkippedWhenAutoRenewed(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.conn.Exec(`UPDATE customers SET phone = "08123456789", auto_renewal = 1`)
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 50000, ID: e.cust.ID})
	got := withNotify(t, e)
	exp := time.Unix(e.sub(t).ExpiresAt, 0)
	e.now = exp.Add(-4 * time.Minute)
	e.s.ExpireDue(ctx)
	e.now = exp.Add(time.Minute)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 0 || count(reqs, "recharge.expired") != 0 {
		t.Fatalf("renewed customer notified: %v", reqs)
	}
	if s := e.sub(t); s.Status != "active" || s.ExpiresAt <= exp.Unix() {
		t.Fatalf("not renewed: %+v", s)
	}
}

// A failed send releases the claim, so the next run retries it.
func TestExpiredNoticeRetriesAfterFailure(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.conn.Exec(`UPDATE customers SET phone = "08123456789"`)
	got := withNotify(t, e)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	good := e.s.notifier().Settings["wa_url"]
	e.s.notifier().Settings["wa_url"] = bad.URL + "/wa?n=[number]&t=[text]"
	exp := time.Unix(e.sub(t).ExpiresAt, 0)
	e.now = exp.Add(-4 * time.Minute)
	e.s.ExpireDue(ctx)
	drain(got)
	if s := e.sub(t); s.ExpiredNotifiedAt.Valid {
		t.Fatalf("claim kept after failure: %+v", s)
	}
	e.s.notifier().Settings["wa_url"] = good
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 1 {
		t.Fatalf("retry: %v", reqs)
	}
}
