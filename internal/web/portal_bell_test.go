package web

import (
	"database/sql"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func bellItems(t *testing.T, e *billEnv) []bellItem {
	t.Helper()
	items, err := e.s.portal.PortalBell(httptest.NewRequest("GET", "/portal", nil).WithContext(t.Context()), &e.cust)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func bellUnread(t *testing.T, e *billEnv) int {
	t.Helper()
	return len(bellShown(t, e))
}

// bellShown is what the page sends back when the bell opens: every unread key:ver.
func bellShown(t *testing.T, e *billEnv) []string {
	t.Helper()
	var v []string
	for _, it := range bellItems(t, e) {
		if it.Unread {
			v = append(v, it.Key+":"+it.Ver)
		}
	}
	return v
}

func bellItem1(t *testing.T, e *billEnv, key string) *bellItem {
	t.Helper()
	for _, it := range bellItems(t, e) {
		if it.Key == key {
			return &it
		}
	}
	return nil
}

func TestPortalBellUnreadAndSeen(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	a, _ := custLogin(t, e, "u1", "pw12345")
	setAnn := func(body string) {
		if err := e.q.UpdatePageBody(t.Context(), db.UpdatePageBodyParams{Body: body, Slug: "announcement"}); err != nil {
			t.Fatal(err)
		}
	}
	setAnn("Maintenance tonight")
	if n := bellUnread(t, e); n != 1 {
		t.Fatalf("unread = %d, want 1", n)
	}
	shown := bellShown(t, e)
	// the announcement changes between page render and the POST: the new text stays unread
	setAnn("Maintenance moved to Friday")
	wantCode(t, do(e.h, "POST", "/portal/bell/seen", url.Values{"seen": shown}, a), 204, "stale seen")
	if n := bellUnread(t, e); n != 1 {
		t.Fatalf("changed item marked seen: unread = %d, want 1", n)
	}
	wantCode(t, do(e.h, "POST", "/portal/bell/seen", url.Values{"seen": bellShown(t, e)}, a), 204, "mark seen")
	if n := bellUnread(t, e); n != 0 {
		t.Fatalf("unread after seen = %d", n)
	}
}

func TestPortalBellLatestPayment(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	a, _ := custLogin(t, e, "u1", "pw12345")
	p := e.plan(t, "day", "PPPoE", 10000)
	pr, err := e.q.CreatePaymentRequest(t.Context(), db.CreatePaymentRequestParams{Ref: "R1", Gateway: "tripay", CustomerID: sql.NullInt64{Int64: e.cust.ID, Valid: true},
		Username: "u1", PlanID: p.ID, Amount: 10000, ExpiresAt: 1 << 40})
	if err != nil {
		t.Fatal(err)
	}
	if bellItem1(t, e, "payment") != nil {
		t.Fatal("a pending payment is not a result yet")
	}
	if _, err := e.q.ClosePaymentRequest(t.Context(), db.ClosePaymentRequestParams{Status: "failed", Ref: "R1"}); err != nil {
		t.Fatal(err)
	}
	it := bellItem1(t, e, "payment")
	if it == nil || it.Title != "Payment failed" || it.Href != "/portal/payments/"+strconv.FormatInt(pr.ID, 10) || !it.Unread {
		t.Fatalf("failed payment item = %+v", it)
	}
	wantCode(t, do(e.h, "POST", "/portal/bell/seen", url.Values{"seen": bellShown(t, e)}, a), 204, "seen")
	if bellItem1(t, e, "payment").Unread {
		t.Fatal("still unread after seen")
	}
	// paid later: same request, new status, unread again
	if _, err := e.q.ClaimPaymentPaid(t.Context(), "R1"); err != nil {
		t.Fatal(err)
	}
	if it := bellItem1(t, e, "payment"); it.Title != "Payment received" || !it.Unread {
		t.Fatalf("paid item = %+v", it)
	}
	// older than 7 days: gone
	if _, err := e.s.conn.ExecContext(t.Context(), `UPDATE payment_requests SET created_at = ?`, time.Now().Unix()-8*86400); err != nil {
		t.Fatal(err)
	}
	if bellItem1(t, e, "payment") != nil {
		t.Fatal("old payment still in the bell")
	}
}

func TestPortalBellImpersonationKeepsUnread(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	if err := e.q.UpdatePageBody(t.Context(), db.UpdatePageBodyParams{Body: "Hello", Slug: "announcement"}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, do(e.h, "POST", "/admin/customers/"+strconv.FormatInt(e.cust.ID, 10)+"/login", nil, e.c), 303, "login as")
	wantCode(t, do(e.h, "POST", "/portal/bell/seen", url.Values{"seen": bellShown(t, e)}, e.c), 204, "seen while impersonating")
	if n := bellUnread(t, e); n != 1 {
		t.Fatalf("impersonating admin cleared the customer's unread: %d", n)
	}
}

func TestPortalBellQuotaLevels(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	a, _ := custLogin(t, e, "u1", "pw12345")
	now := time.Now().Unix()
	dashPlan(t, e, "Data_Limit", 0, 100, now+86400)
	dashSess(t, e, "u1", "a", "10.1.0.1", "", now-3600, now-60, 45<<20, 45<<20) // 90 of 100 MB
	q := bellItem1(t, e, "quota")
	if q == nil || q.Title != "Quota almost used up" {
		t.Fatalf("at 90%%: %+v", q)
	}
	wantCode(t, do(e.h, "POST", "/portal/bell/seen", url.Values{"seen": {q.Key + ":" + q.Ver}}, a), 204, "seen")
	if bellItem1(t, e, "quota").Unread {
		t.Fatal("seen at 90% should be read")
	}
	dashSess(t, e, "u1", "b", "10.1.0.1", "", now-1800, now-30, 10<<20, 10<<20) // now 110 MB
	if q := bellItem1(t, e, "quota"); q.Title != "Quota used up" || !q.Unread {
		t.Fatalf("at 100%%: %+v", q)
	}
}
