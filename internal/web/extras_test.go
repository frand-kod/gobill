package web

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/device"
)

// remDev records add and remove calls of the device.
type remDev struct {
	device.Dummy
	calls *[]string
}

func (d remDev) AddCustomer(_ context.Context, c device.Customer, _ device.Plan) error {
	*d.calls = append(*d.calls, "add "+c.Username)
	return nil
}

func (d remDev) RemoveCustomer(_ context.Context, c device.Customer, _ device.Plan) error {
	*d.calls = append(*d.calls, "remove "+c.Username)
	return nil
}

func (e *billEnv) recordDevice() {
	e.s.Billing.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return remDev{calls: e.calls}, nil }
}

// capture starts a fake SMS/WA gateway; every request lands (path+query+body) in the channel.
func capture(t *testing.T) (string, chan string) {
	t.Helper()
	got := make(chan string, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.URL.Path + "?" + r.URL.RawQuery + " " + string(b)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, got
}

func waitMsg(t *testing.T, got chan string) string {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no message sent")
		return ""
	}
}

func noMsg(t *testing.T, got chan string) {
	t.Helper()
	select {
	case m := <-got:
		t.Fatalf("unexpected message: %s", m)
	case <-time.After(300 * time.Millisecond):
	}
}

func activityActions(t *testing.T, e *billEnv) string {
	l, err := e.q.ListActivityLogs(t.Context(), db.ListActivityLogsParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, x := range l {
		sb.WriteString(x.Action + " " + x.Description + "\n")
	}
	return sb.String()
}

func TestLoginAsCustomer(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	id := itoa(e.cust.ID)
	rita := login(t, e.h, "rita") // role Report
	if w := do(e.h, "POST", "/admin/customers/"+id+"/login", nil, rita); w.Code != http.StatusForbidden {
		t.Fatalf("non-manager: %d", w.Code)
	}
	if strings.Contains(activityActions(t, e), "customer.impersonate") {
		t.Fatal("forbidden attempt was logged as impersonation")
	}
	w := do(e.h, "POST", "/admin/customers/"+id+"/login", nil, e.c)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/portal" {
		t.Fatalf("login as: %d %s", w.Code, w.Header().Get("Location"))
	}
	if !strings.Contains(activityActions(t, e), "customer.impersonate u1") {
		t.Fatalf("not logged: %s", activityActions(t, e))
	}
	// same browser session (cookie), no customer password involved
	if w := do(e.h, "GET", "/portal", nil, e.c); w.Code != 200 || !strings.Contains(w.Body.String(), `action="/portal/impersonate/end"`) {
		t.Fatalf("portal/banner: %d", w.Code)
	}
	if w := do(e.h, "GET", "/admin", nil, e.c); w.Code != 200 {
		t.Fatalf("admin session touched: %d", w.Code)
	}
	w = do(e.h, "POST", "/portal/impersonate/end", nil, e.c)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/customers/"+id {
		t.Fatalf("end: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := do(e.h, "GET", "/portal", nil, e.c); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/portal/login" {
		t.Fatalf("customer session survived: %d", w.Code)
	}
	if w := do(e.h, "GET", "/admin", nil, e.c); w.Code != 200 {
		t.Fatalf("admin session ended: %d", w.Code)
	}
	// a normal customer login has no banner
	c, _ := custLogin(t, e, "u1", "pw12345")
	if w := do(e.h, "GET", "/portal", nil, c); strings.Contains(w.Body.String(), "/portal/impersonate/end") {
		t.Fatal("banner on a normal session")
	}
}

func TestCustomerDeactivateAndSync(t *testing.T) {
	e := billApp(t)
	e.recordDevice()
	p := e.plan(t, "Gold", "PPPoE", 10000)
	if err := e.s.Billing.Recharge(t.Context(), e.cust.ID, p.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	id := itoa(e.cust.ID)
	*e.calls = nil
	if w := do(e.h, "POST", "/admin/customers/"+id+"/sync", nil, e.c); w.Code != http.StatusSeeOther {
		t.Fatalf("sync: %d", w.Code)
	}
	if got := strings.Join(*e.calls, ","); got != "add u1" {
		t.Fatalf("sync calls: %q", got)
	}
	*e.calls = nil
	if w := do(e.h, "POST", "/admin/customers/"+id+"/deactivate", nil, e.c); w.Code != http.StatusSeeOther {
		t.Fatalf("deactivate: %d", w.Code)
	}
	if got := strings.Join(*e.calls, ","); got != "remove u1" {
		t.Fatalf("deactivate calls: %q", got)
	}
	subs, _ := e.q.ListSubscriptionsByCustomer(t.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	if len(subs) != 1 || subs[0].Status == "active" {
		t.Fatalf("subs: %+v", subs)
	}
	if w := do(e.h, "POST", "/admin/customers/"+id+"/deactivate", nil, login(t, e.h, "rita")); w.Code != http.StatusForbidden {
		t.Fatalf("report role: %d", w.Code)
	}
	// buttons and the message link are on the detail page
	b := do(e.h, "GET", "/admin/customers/"+id, nil, e.c).Body.String()
	for _, want := range []string{"/deactivate", "/sync", "/login", "/admin/message/send?customer=" + id} {
		if !strings.Contains(b, want) {
			t.Errorf("detail page lacks %s", want)
		}
	}
	if w := do(e.h, "GET", "/admin/message/send?customer="+id, nil, e.c); !strings.Contains(w.Body.String(), `value="`+id+`" selected`) {
		t.Error("send form not prefilled")
	}
}

func TestWelcomeMessageHasNoPassword(t *testing.T) {
	e := billApp(t)
	gw, got := capture(t)
	setting(t, e, "wa_url", gw+"/wa?n=[number]&t=[text]", "company_name", "Acme", "notif_welcome_message", "Hi [[name]] user [[Username]] pw [[Password]]")
	f := url.Values{"username": {"newbie"}, "password": {"SuperSecretPw1"}, "fullname": {"New Bie"}, "phone": {"6281234567890"},
		"service_type": {"Others"}, "status": {"Active"}, "send_welcome_message": {"1"}, "notify_wa": {"1"}}
	if w := do(e.h, "POST", "/admin/customers", f, e.c); w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	m := waitMsg(t, got)
	if !strings.Contains(m, "newbie") || strings.Contains(m, "SuperSecretPw1") {
		t.Fatalf("message: %s", m)
	}
	// unchecked: nothing is sent
	f.Set("username", "quiet")
	f.Del("send_welcome_message")
	do(e.h, "POST", "/admin/customers", f, e.c)
	noMsg(t, got)
}

func TestBuyForFriend(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 5000)
	friend, _ := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "Two", ServiceType: "PPPoE", Status: "Active"})
	p := e.plan(t, "Gold", "PPPoE", 10000)
	c, _ := custLogin(t, e, "u1", "pw12345")
	path := "/portal/plans/" + itoa(p.ID) + "/friend"
	bal := func(id int64) int64 { x, _ := e.q.GetCustomer(t.Context(), id); return x.Balance }
	subs := func() int {
		l, _ := e.q.ListSubscriptionsByCustomer(t.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: friend.ID, Limit: 10})
		return len(l)
	}
	trx := func() int {
		l, _ := e.q.ListTransactions(t.Context(), db.ListTransactionsParams{Limit: 50})
		return len(l)
	}
	reject := func(user, why string) {
		t.Helper()
		if w := do(e.h, "POST", path, url.Values{"username": {user}}, c); w.Code != 200 || !strings.Contains(w.Body.String(), "alert-error") {
			t.Fatalf("%s accepted: %d", why, w.Code)
		}
		if bal(e.cust.ID) != 5000 || subs() != 0 || trx() != 0 {
			t.Fatalf("%s changed state: bal=%d subs=%d trx=%d", why, bal(e.cust.ID), subs(), trx())
		}
	}
	reject("u2", "insufficient balance")
	reject("nobody", "unknown friend")
	reject("u1", "self")
	e.q.AdjustBalance(t.Context(), db.AdjustBalanceParams{Delta: 10000, ID: e.cust.ID}) // 15000
	setting(t, e, "enable_balance", "no")
	if w := do(e.h, "POST", path, url.Values{"username": {"u2"}}, c); !strings.Contains(w.Body.String(), "alert-error") || subs() != 0 {
		t.Fatal("balance disabled but accepted")
	}
	setting(t, e, "enable_balance", "yes")

	// atomic: a failure after the debit rolls everything back
	if _, err := e.s.conn.Exec(`CREATE TRIGGER boom BEFORE INSERT ON transactions BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if w := do(e.h, "POST", path, url.Values{"username": {"u2"}}, c); w.Code != http.StatusInternalServerError {
		t.Fatalf("forced failure: %d", w.Code)
	}
	if bal(e.cust.ID) != 15000 || subs() != 0 {
		t.Fatalf("not atomic: bal=%d subs=%d", bal(e.cust.ID), subs())
	}
	e.s.conn.Exec(`DROP TRIGGER boom`)

	if w := do(e.h, "POST", path, url.Values{"username": {"u2"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	if bal(e.cust.ID) != 5000 || subs() != 1 || trx() != 2 {
		t.Fatalf("after send: bal=%d subs=%d trx=%d", bal(e.cust.ID), subs(), trx())
	}
	// friend now has Gold active; a different plan for them is refused
	other := e.plan(t, "Silver", "PPPoE", 1000)
	if w := do(e.h, "POST", "/portal/plans/"+itoa(other.ID)+"/friend", url.Values{"username": {"u2"}}, c); !strings.Contains(w.Body.String(), "alert-error") {
		t.Fatal("different active plan accepted")
	}
	// the friend's activation list shows it (not the sender's balance movement)
	fc, _ := e.q.GetCustomer(t.Context(), friend.ID)
	_ = fc
	if w := do(e.h, "GET", "/portal/activation", nil, c); w.Code != 200 || strings.Contains(w.Body.String(), "Send Plan") {
		t.Fatalf("activation list: %d", w.Code)
	}
}

func TestActivationList(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	p := e.plan(t, "Gold", "PPPoE", 10000)
	if err := e.s.Billing.Recharge(t.Context(), e.cust.ID, p.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	c, _ := custLogin(t, e, "u1", "pw12345")
	b := do(e.h, "GET", "/portal/activation", nil, c).Body.String()
	for _, want := range []string{"Gold", "Admin - Cash", "PPPoE"} {
		if !strings.Contains(b, want) {
			t.Errorf("activation list lacks %q", want)
		}
	}
}

func TestForgotUsernameUniform(t *testing.T) {
	e := billApp(t)
	gw, got := capture(t)
	setting(t, e, "sms_url", gw+"/sms?n=[number]&t=[text]")
	e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "ann", PasswordHash: "h", Fullname: "Ann", Phone: "081234567890", ServiceType: "PPPoE", Status: "Active"})
	body := func(contact string) string {
		w := do(e.h, "POST", "/portal/forgot/username", url.Values{"contact": {contact}}, nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d", contact, w.Code)
		}
		return w.Body.String()
	}
	known, unknown := body("081234567890"), body("089999999999")
	if known != unknown || !strings.Contains(known, "alert-ok") {
		t.Fatal("response differs between known and unknown contact")
	}
	if m := waitMsg(t, got); !strings.Contains(m, "ann") {
		t.Fatalf("message: %s", m)
	}
	noMsg(t, got) // unknown contact sent nothing
	// the OTP limiter applies: one phone gets a 60s cooldown
	if w := do(e.h, "POST", "/portal/forgot/username", url.Values{"contact": {"081234567890"}}, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("limiter: %d", w.Code)
	}
}

func TestLogCleanup(t *testing.T) {
	e := billApp(t)
	old := time.Now().AddDate(0, 0, -100).Unix()
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO activity_logs (actor_type, actor_id, action, description, created_at) VALUES ('admin', 1, 'old.act', 'x', %d)`, old),
		fmt.Sprintf(`INSERT INTO message_logs (channel, recipient, subject, body, status, created_at) VALUES ('sms', 'r', 's', 'b', 'ok', %d)`, old),
		fmt.Sprintf(`INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at) VALUES ('open', 'u', '1.1.1.1', %d, %d)`, old, old),
		fmt.Sprintf(`INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, stopped_at) VALUES ('old', 'u', '1.1.1.1', %d, %d, %d)`, old, old, old),
		fmt.Sprintf(`INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, stopped_at) VALUES ('new', 'u', '1.1.1.1', %d, %d, %d)`, time.Now().Unix()-60, time.Now().Unix(), time.Now().Unix()),
	} {
		if _, err := e.s.conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	count := func(tbl, where string) (n int) {
		e.s.conn.QueryRow("SELECT COUNT(*) FROM " + tbl + " WHERE " + where).Scan(&n)
		return
	}
	if w := do(e.h, "POST", "/admin/logs/clean/radius", url.Values{"keep": {"30"}}, login(t, e.h, "rita")); w.Code != http.StatusForbidden {
		t.Fatalf("report role: %d", w.Code)
	}
	for _, bad := range []string{"", "0", "-5", "abc"} {
		do(e.h, "POST", "/admin/logs/clean/radius", url.Values{"keep": {bad}}, e.c)
	}
	if count("radius_sessions", "1=1") != 3 {
		t.Fatal("invalid keep deleted rows")
	}
	if w := do(e.h, "POST", "/admin/logs/clean/radius", url.Values{"keep": {"30"}}, e.c); w.Code != http.StatusSeeOther {
		t.Fatalf("clean radius: %d", w.Code)
	}
	if count("radius_sessions", "session_id = 'open'") != 1 || count("radius_sessions", "session_id = 'old'") != 0 || count("radius_sessions", "session_id = 'new'") != 1 {
		t.Fatal("radius cleanup must delete only old CLOSED sessions")
	}
	if count("activity_logs", "action = 'old.act'") != 1 || count("message_logs", "1=1") != 1 {
		t.Fatal("radius clean touched other logs")
	}
	do(e.h, "POST", "/admin/logs/clean/messages", url.Values{"keep": {"30"}}, e.c)
	do(e.h, "POST", "/admin/logs/clean/activity", url.Values{"keep": {"30"}}, e.c)
	if count("activity_logs", "action = 'old.act'") != 0 || count("message_logs", "1=1") != 0 {
		t.Fatal("activity/message cleanup failed")
	}
	// pages carry the form
	if b := do(e.h, "GET", "/admin/logs/radius", nil, e.c).Body.String(); !strings.Contains(b, `action="/admin/logs/clean/radius"`) {
		t.Fatal("no clean form")
	}

	// daily job: setting log_keep_days, 0 = keep forever
	e.s.conn.Exec(fmt.Sprintf(`INSERT INTO message_logs (channel, recipient, subject, body, status, created_at) VALUES ('sms', 'r', 's', 'b', 'ok', %d)`, old))
	if err := e.s.Billing.LogCleanJob(nil)(t.Context()); err != nil || count("message_logs", "1=1") != 1 {
		t.Fatalf("job with default 0 deleted rows: %v", err)
	}
	setting(t, e, "log_keep_days", "30")
	if err := e.s.Billing.LogCleanJob(nil)(t.Context()); err != nil || count("message_logs", "1=1") != 0 || count("radius_sessions", "session_id = 'open'") != 1 {
		t.Fatalf("job: %v", err)
	}
}

func TestCustomBalanceTopUp(t *testing.T) {
	e := payApp(t)
	f := url.Values{"amount": {"25000"}, "channel": {"QRIS"}}
	if w := do(e.h, "POST", "/portal/topup", f, e.cookie); w.Code != http.StatusNotFound {
		t.Fatalf("allow_balance_custom off: %d", w.Code)
	}
	setting(t, e.billEnv, "allow_balance_custom", "yes")
	if w := do(e.h, "GET", "/portal/plans", nil, e.cookie); !strings.Contains(w.Body.String(), `action="/portal/topup"`) {
		t.Fatal("custom form missing")
	}
	if w := do(e.h, "POST", "/portal/topup", url.Values{"amount": {"0"}, "channel": {"QRIS"}}, e.cookie); w.Code != 200 {
		t.Fatalf("zero amount: %d", w.Code)
	}
	if w := do(e.h, "POST", "/portal/topup", f, e.cookie); w.Code != 303 || w.Header().Get("Location") != "https://pay.example/T123" {
		t.Fatalf("topup: %d %s", w.Code, w.Header().Get("Location"))
	}
	pr := e.pending(t)
	if pr.PlanID != 0 || pr.Amount != 25000 {
		t.Fatalf("row: %+v", pr)
	}
	body := fmt.Sprintf(`{"merchant_ref":%q,"status":"PAID"}`, pr.Ref)
	for range 2 {
		postCallback(e.h, body, signed(body))
	}
	if c, _ := e.q.GetCustomer(t.Context(), e.cust.ID); c.Balance != 25000 {
		t.Fatalf("balance %d", c.Balance)
	}
	l, _ := e.q.ListTransactions(t.Context(), db.ListTransactionsParams{Limit: 5})
	if len(l) != 1 || l[0].Type != "Balance" || l[0].Price != 25000 {
		t.Fatalf("trx: %+v", l)
	}
	// the audit pages still list a plan-less payment
	if w := do(e.h, "GET", "/admin/payment-gateway/audit/"+itoa(pr.ID), nil, e.c); w.Code != 200 || !strings.Contains(w.Body.String(), "Custom Balance") {
		t.Fatalf("audit: %d", w.Code)
	}
	_ = sql.ErrNoRows
}
