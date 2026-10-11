package web

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
)

// failDev is a router that refuses every change.
type failDev struct{ device.Dummy }

func (failDev) AddCustomer(context.Context, device.Customer, device.Plan) error {
	return errors.New("router offline")
}

// syncBuf is a log sink safe for the notify goroutines.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// xlate is the text the app shows for an English message, in the language the tests run in.
func xlate(e *billEnv, msg string) string { return e.s.catalog.T(e.s.language(), msg) }

// afterPost is the page a post redirects to, read with the session that post set (its flash).
func afterPost(e *billEnv, w *httptest.ResponseRecorder) string {
	ck := sessionOf(w)
	if ck == nil {
		ck = e.c
	}
	return do(e.h, "GET", w.Header().Get("Location"), nil, ck).Body.String()
}

// addForm posts the add-customer form; plan "" means no plan.
func addForm(user, svc, plan, status string) url.Values {
	return url.Values{"username": {user}, "password": {"secret123"}, "fullname": {"Budi"}, "service_type": {svc},
		"status": {status}, "plan": {plan}}
}

func TestAddCustomerWithPlanActivatesCash(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	p := e.plan(t, "Gold", "PPPoE", 150000)
	w := do(e.h, "POST", "/admin/customers", addForm("budi01", "PPPoE", itoa(p.ID), "Active"), e.c)
	wantCode(t, w, 303, "add with plan")
	c, err := e.q.GetCustomerByUsername(ctx, "budi01")
	if err != nil {
		t.Fatal(err)
	}
	if loc := w.Header().Get("Location"); loc != "/admin/customers/"+itoa(c.ID) {
		t.Fatalf("redirect to %q", loc)
	}
	subs, err := e.q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 10})
	if err != nil || len(subs) != 1 || subs[0].PlanID != p.ID || subs[0].Status != "active" {
		t.Fatalf("subscriptions: %+v %v", subs, err)
	}
	trx, err := e.q.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 10})
	if err != nil || len(trx) != 1 || !strings.HasPrefix(trx[0].Method, "Cash") {
		t.Fatalf("transactions: %+v %v", trx, err)
	}
	if page := afterPost(e, w); !strings.Contains(page, xlate(e, "Customer created and plan activated, paid in Cash")) {
		t.Fatal("no success flash on the customer page")
	}
}

func TestAddCustomerActivationFailureKeepsCustomer(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	p := e.plan(t, "Gold", "PPPoE", 150000)
	// a suspended customer cannot be recharged: the customer is created, the plan is not
	w := do(e.h, "POST", "/admin/customers", addForm("budi02", "PPPoE", itoa(p.ID), "Suspended"), e.c)
	wantCode(t, w, 303, "add with failing plan")
	c, err := e.q.GetCustomerByUsername(ctx, "budi02")
	if err != nil {
		t.Fatalf("customer not created: %v", err)
	}
	if subs, _ := e.q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 10}); len(subs) != 0 {
		t.Fatalf("subscription created despite failure: %+v", subs)
	}
	page := afterPost(e, w)
	if !strings.Contains(page, xlate(e, "Customer created, but the plan could not be activated")) || !strings.Contains(page, xlate(e, "account is not active")) {
		t.Fatal("no activation failure with its reason on the customer page")
	}
}

func TestAddCustomerRouterFailureWarns(t *testing.T) {
	e := billApp(t)
	e.s.Billing.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return failDev{}, nil }
	p := e.plan(t, "Gold", "PPPoE", 150000)
	w := do(e.h, "POST", "/admin/customers", addForm("budi03", "PPPoE", itoa(p.ID), "Active"), e.c)
	wantCode(t, w, 303, "add, router down")
	page := afterPost(e, w)
	if !strings.Contains(page, xlate(e, "Customer created and plan activated, paid in Cash")) || !strings.Contains(page, xlate(e, msgNotSynced)) {
		t.Fatal("router failure not shown after create")
	}
}

func TestAddCustomerPlanMustFitServiceType(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "Gold", "PPPoE", 150000)
	wantCode(t, do(e.h, "POST", "/admin/customers", addForm("budi04", "Hotspot", itoa(p.ID), "Active"), e.c), 422, "PPPoE plan for Hotspot customer")
	noCustomer(t, e, "budi04")
}

// waitFor polls cond for up to 3 seconds.
func waitFor(cond func() bool) bool {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

func TestAddCustomerWelcomeCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, creds, notify string
		wantSent            bool
		wantPass            bool
	}{
		{"yes sends real password", "yes", "", true, true},
		{"no sends mask", "no", "", true, false},
		{"customers off sends nothing", "yes", "no", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs syncBuf
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(old)

			e := billApp(t)
			var mu sync.Mutex
			var got []string
			gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				got = append(got, r.URL.Query().Get("text"))
				mu.Unlock()
			}))
			defer gw.Close()
			settings := map[string]string{"wa_url": gw.URL + "/send?to=[number]&text=[text]", "country_code_phone": "62",
				"company_name": "ACME", "send_credentials_on_create": tc.creds}
			if tc.notify != "" {
				settings["notify_customers"] = tc.notify
			}
			for k, v := range settings {
				if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
					t.Fatal(err)
				}
			}
			form := addForm("budi05", "PPPoE", "", "Active")
			form.Set("phone", "0812345678")
			form.Set("send_welcome_message", "1")
			form.Set("notify_wa", "1")
			wantCode(t, do(e.h, "POST", "/admin/customers", form, e.c), 303, "add with welcome")

			if tc.wantSent && !waitFor(func() bool { mu.Lock(); defer mu.Unlock(); return len(got) > 0 }) {
				t.Fatal("welcome message not sent")
			}
			mu.Lock()
			defer mu.Unlock()
			if !tc.wantSent {
				time.Sleep(200 * time.Millisecond)
				if len(got) != 0 {
					t.Fatalf("message sent with notify_customers=no: %q", got)
				}
			} else {
				msg := got[0]
				if !strings.Contains(msg, "budi05") {
					t.Fatalf("welcome text: %q", msg)
				}
				if tc.wantPass && !strings.Contains(msg, "Password: secret123") {
					t.Fatalf("real password missing: %q", msg)
				}
				if !tc.wantPass && (strings.Contains(msg, "secret123") || strings.Contains(msg, "Password:")) {
					t.Fatalf("password leaked with setting no: %q", msg)
				}
			}
			if strings.Contains(logs.String(), "secret123") {
				t.Fatal("password in slog output")
			}
			rows, err := e.q.SearchMessageLogs(t.Context(), db.SearchMessageLogsParams{PageLimit: 100})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if strings.Contains(r.Body, "secret123") {
					t.Fatalf("password stored in message log: %q", r.Body)
				}
			}
		})
	}
}
