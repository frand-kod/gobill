package web

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

func custLogin(t *testing.T, e *billEnv, user, pass string) (*http.Cookie, int) {
	t.Helper()
	w := do(e.h, "POST", "/portal/login", url.Values{"username": {user}, "password": {pass}}, nil)
	for _, c := range w.Result().Cookies() {
		if c.Name == "nuxbill_session" {
			return c, w.Code
		}
	}
	return nil, w.Code
}

func portalCust(t *testing.T, e *billEnv, balance int64) {
	t.Helper()
	h, _ := bcrypt.GenerateFromPassword([]byte("pw12345"), bcrypt.MinCost)
	if err := e.q.SetCustomerPassword(t.Context(), db.SetCustomerPasswordParams{PasswordHash: string(h), ID: e.cust.ID}); err != nil {
		t.Fatal(err)
	}
	if balance > 0 {
		if _, err := e.q.AdjustBalance(t.Context(), db.AdjustBalanceParams{Delta: balance, ID: e.cust.ID}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPortalLoginAndIsolation(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	if w := do(e.h, "POST", "/portal/login", url.Values{"username": {"u1"}, "password": {"bad"}}, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "alert-error") {
		t.Fatalf("bad login: %d", w.Code)
	}
	c, code := custLogin(t, e, "u1", "pw12345")
	if code != http.StatusSeeOther || c == nil {
		t.Fatalf("login: %d", code)
	}
	if w := do(e.h, "GET", "/portal", nil, c); w.Code != 200 || !strings.Contains(w.Body.String(), "Rp 0") {
		t.Fatalf("dashboard: %d", w.Code)
	}
	// customer session is not an admin session
	if w := do(e.h, "GET", "/admin", nil, c); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("customer on admin: %d", w.Code)
	}
	// admin session is not a customer session
	if w := do(e.h, "GET", "/portal", nil, e.c); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/portal/login" {
		t.Fatalf("admin on portal: %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestPortalBalanceOrder(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 5000)
	p := e.plan(t, "Gold", "PPPoE", 10000)
	c, _ := custLogin(t, e, "u1", "pw12345")
	buy := "/portal/plans/" + strconv.FormatInt(p.ID, 10) + "/balance"
	// balance payment disabled
	if w := do(e.h, "POST", buy, url.Values{}, c); w.Code != 200 || !strings.Contains(w.Body.String(), "alert-error") {
		t.Fatalf("disabled: %d", w.Code)
	}
	if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "enable_balance", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	if w := do(e.h, "GET", "/portal/plans", nil, c); !strings.Contains(w.Body.String(), "Gold") {
		t.Fatal("plan not listed")
	}
	// insufficient funds
	if w := do(e.h, "POST", buy, url.Values{}, c); w.Code != 200 || !strings.Contains(w.Body.String(), "alert-error") {
		t.Fatalf("insufficient: %d", w.Code)
	}
	if _, err := e.q.AdjustBalance(t.Context(), db.AdjustBalanceParams{Delta: 5000, ID: e.cust.ID}); err != nil {
		t.Fatal(err)
	}
	if w := do(e.h, "POST", buy, url.Values{}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("buy: %d %s", w.Code, w.Body.String())
	}
	cu, _ := e.q.GetCustomer(t.Context(), e.cust.ID)
	trx, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: e.cust.ID, Valid: true}, Limit: 10})
	if cu.Balance != 0 || len(trx) != 1 {
		t.Fatalf("balance %d trx %d", cu.Balance, len(trx))
	}
	if w := do(e.h, "GET", "/portal/orders", nil, c); !strings.Contains(w.Body.String(), "Gold") {
		t.Fatal("history missing")
	}
}

func TestPortalRegister(t *testing.T) {
	e := billApp(t)
	f := url.Values{"username": {"newbie"}, "fullname": {"New Bie"}, "password": {"abc12345"}, "cpassword": {"abc12345"}}
	if w := do(e.h, "POST", "/portal/register", f, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	c, err := e.q.GetCustomerByUsername(t.Context(), "newbie")
	if err != nil || bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte("abc12345")) != nil {
		t.Fatalf("customer not created with bcrypt hash: %v", err)
	}
	if w := do(e.h, "POST", "/portal/register", f, nil); w.Code != 200 {
		t.Fatal("duplicate accepted")
	}
}

func TestPortalBalanceOrderWhenSettingUnset(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 10000)
	p := e.plan(t, "Gold", "PPPoE", 10000)
	c, _ := custLogin(t, e, "u1", "pw12345")
	// enable_balance never saved: billing treats it as enabled, so the portal must too
	if w := do(e.h, "POST", "/portal/plans/"+strconv.FormatInt(p.ID, 10)+"/balance", url.Values{}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("buy with unset setting: %d %s", w.Code, w.Body.String())
	}
}

func TestPortalSessionRevokedOnPasswordChange(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	old, _ := custLogin(t, e, "u1", "pw12345")
	other, _ := custLogin(t, e, "u1", "pw12345")
	w := do(e.h, "POST", "/portal/password", url.Values{"password": {"pw12345"}, "npass": {"newpass1"}, "cnpass": {"newpass1"}}, old)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("change: %d", w.Code)
	}
	cur := old
	for _, c := range w.Result().Cookies() {
		if c.Name == "nuxbill_session" {
			cur = c
		}
	}
	if w := do(e.h, "GET", "/portal", nil, cur); w.Code != 200 {
		t.Errorf("acting session: %d", w.Code)
	}
	if w := do(e.h, "GET", "/portal", nil, other); w.Code != http.StatusSeeOther {
		t.Errorf("old session still valid: %d", w.Code)
	}
}
