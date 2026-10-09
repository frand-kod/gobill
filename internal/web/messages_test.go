package web

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

// msgCustomer creates a customer with password pw12345, optionally with a phone.
func msgCustomer(t *testing.T, q *db.Queries, user, svc, phone string) db.Customer {
	t.Helper()
	h, _ := bcrypt.GenerateFromPassword([]byte("pw12345"), bcrypt.MinCost)
	c, err := q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: user, PasswordHash: string(h), Fullname: user + " name",
		Phone: phone, ServiceType: svc, AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMessagePlaceholders(t *testing.T) {
	c := db.Customer{Username: "budi", Fullname: "Budi", Phone: "0812"}
	got := messageFor(c, "ACME", "Hi [[name]] ([[user_name]], [[phone]]) from [[company_name]] [[unknown]]")
	want := "Hi Budi (budi, 0812) from ACME [[unknown]]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBulkRecipientFilter(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	rt2, err := e.q.CreateRouter(ctx, db.CreateRouterParams{Name: "r2", Host: "h", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	hot := msgCustomer(t, e.q, "hot", "Hotspot", "")
	ppp := msgCustomer(t, e.q, "ppp", "PPPoE", "")
	p := e.plan(t, "day", "PPPoE", 10000)
	sub := func(c db.Customer, router int64) {
		if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: p.ID,
			RouterID: sql.NullInt64{Int64: router, Valid: true}, Type: "PPPoE", StartedAt: 1, ExpiresAt: time.Now().Add(time.Hour).Unix(), Method: "cash"}); err != nil {
			t.Fatal(err)
		}
	}
	sub(hot, e.rt)
	sub(ppp, rt2.ID)
	ids := func(svc string, router int64, status string) []string {
		t.Helper()
		cs, err := e.q.ListMessageRecipients(ctx, db.ListMessageRecipientsParams{ServiceType: svc, RouterID: router, SubStatus: status})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cs {
			out = append(out, c.Username)
		}
		return out
	}
	cases := []struct {
		name   string
		svc    string
		router int64
		status string
		want   string
	}{
		{"everyone", "", 0, "", "u1 hot ppp"},
		{"service", "PPPoE", 0, "", "u1 ppp"},
		{"router", "", e.rt, "", "hot"},
		{"router+service", "PPPoE", rt2.ID, "", "ppp"},
		{"active", "", 0, "active", "hot ppp"},
		{"expired", "", 0, "expired", ""},
	}
	for _, tc := range cases {
		if got := strings.Join(ids(tc.svc, tc.router, tc.status), " "); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBulkInboxSends(t *testing.T) {
	s, _, q, _ := crudApp(t)
	c := msgCustomer(t, q, "bulk1", "PPPoE", "")
	msgCustomer(t, q, "bulk2", "PPPoE", "")
	cs, _ := q.ListMessageRecipients(t.Context(), db.ListMessageRecipientsParams{})
	bulk.start(len(cs))
	s.runBulk(map[string]string{"message_delay": "0"}, cs, "inbox", "alice", "Promo", "Hi [[name]]")
	if n, _ := q.CountUnreadInbox(t.Context(), c.ID); n != 1 {
		t.Fatalf("unread for bulk1 = %d", n)
	}
	if bulk.sent != len(cs) || bulk.active {
		t.Fatalf("progress sent=%d total=%d active=%v", bulk.sent, bulk.total, bulk.active)
	}
}

func TestInboxSendVisibleOnlyToCustomer(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	msgCustomer(t, e.q, "u2", "PPPoE", "")
	a, _ := custLogin(t, e, "u1", "pw12345")
	b, _ := custLogin(t, e, "u2", "pw12345")
	form := url.Values{"customer_id": {itoa(e.cust.ID)}, "channel": {"inbox"}, "subject": {"Tagihan"}, "message": {"Halo [[name]]"}}
	wantCode(t, do(e.h, "POST", "/admin/message/send", form, e.c), 200, "send inbox")
	if w := do(e.h, "GET", "/portal/inbox", nil, a); !strings.Contains(w.Body.String(), "Tagihan") {
		t.Fatal("owner does not see the message")
	}
	if w := do(e.h, "GET", "/portal/inbox", nil, b); strings.Contains(w.Body.String(), "Tagihan") {
		t.Fatal("other customer sees the message")
	}
	msgs, _ := e.q.ListInboxByCustomer(t.Context(), db.ListInboxByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	if len(msgs) != 1 || msgs[0].Body != "Halo U One" {
		t.Fatalf("stored: %+v", msgs)
	}
	id := strconv.FormatInt(msgs[0].ID, 10)
	wantCode(t, do(e.h, "GET", "/portal/inbox/"+id, nil, b), 404, "other customer opens message")
	wantCode(t, do(e.h, "GET", "/portal/inbox/"+id, nil, a), 200, "owner opens message")
}

func TestInboxMarkRead(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	a, _ := custLogin(t, e, "u1", "pw12345")
	if err := e.q.CreateInboxMessage(t.Context(), db.CreateInboxMessageParams{CustomerID: e.cust.ID, FromName: "alice", Subject: "S", Body: "B"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.q.CountUnreadInbox(t.Context(), e.cust.ID); n != 1 {
		t.Fatalf("unread before = %d", n)
	}
	msgs, _ := e.q.ListInboxByCustomer(t.Context(), db.ListInboxByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	if w := do(e.h, "GET", "/portal/inbox/"+strconv.FormatInt(msgs[0].ID, 10), nil, a); w.Code != 200 {
		t.Fatalf("open: %d", w.Code)
	}
	if n, _ := e.q.CountUnreadInbox(t.Context(), e.cust.ID); n != 0 {
		t.Fatalf("unread after open = %d", n)
	}
}

func TestReportCannotSend(t *testing.T) {
	_, h, _, _ := crudApp(t)
	cr := login(t, h, "rita")
	wantCode(t, do(h, "GET", "/admin/message/send", nil, cr), 403, "report send form")
	wantCode(t, do(h, "POST", "/admin/message/send", url.Values{"customer_id": {"1"}, "channel": {"inbox"}, "message": {"x"}}, cr), 403, "report send")
	wantCode(t, do(h, "POST", "/admin/message/bulk", url.Values{"service": {"all"}, "router": {""}, "status": {"all"}, "channel": {"inbox"}, "message": {"x"}}, cr), 403, "report bulk")
}

func TestWAChannelDispatch(t *testing.T) {
	e := billApp(t)
	var got []string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query().Get("to")+"|"+r.URL.Query().Get("text"))
	}))
	defer gw.Close()
	ctx := t.Context()
	for k, v := range map[string]string{"wa_url": gw.URL + "/send?to=[number]&text=[text]", "country_code_phone": "62", "company_name": "ACME"} {
		if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	w := msgCustomer(t, e.q, "wati", "PPPoE", "0812345")
	form := url.Values{"customer_id": {itoa(w.ID)}, "channel": {"wa"}, "message": {"Hi [[name]] from [[company_name]]"}}
	wantCode(t, do(e.h, "POST", "/admin/message/send", form, e.c), 200, "send wa")
	if len(got) != 1 || got[0] != "62812345|Hi wati name from ACME" {
		t.Fatalf("gateway got %q", got)
	}
	// sms has no gateway configured: reported as an error, nothing sent
	form["channel"] = []string{"sms"}
	if resp := do(e.h, "POST", "/admin/message/send", form, e.c); !strings.Contains(resp.Body.String(), "gateway not configured") {
		t.Fatal("sms without gateway should say so")
	}
	if len(got) != 1 {
		t.Fatalf("unexpected extra sends: %q", got)
	}
}
