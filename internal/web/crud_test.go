package web

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

func crudApp(t *testing.T) (*Server, http.Handler, *db.Queries, *http.Cookie) {
	s, q := newTestApp(t)
	s.SecretKey = bytes.Repeat([]byte{7}, 32)
	h := s.Handler()
	return s, h, q, login(t, h, "alice")
}

func wantCode(t *testing.T, w interface{ Result() *http.Response }, code int, what string) {
	t.Helper()
	if got := w.Result().StatusCode; got != code {
		t.Fatalf("%s: got %d, want %d", what, got, code)
	}
}

func TestBandwidthCRUD(t *testing.T) {
	_, h, q, c := crudApp(t)
	form := url.Values{"name": {"10M"}, "rate_down": {"10"}, "rate_down_unit": {"Mbps"}, "rate_up": {"5"}, "rate_up_unit": {"Mbps"}}

	// validation error keeps values
	bad := url.Values{"name": {"X"}, "rate_down": {"0"}, "rate_down_unit": {"Mbps"}, "rate_up": {"5"}, "rate_up_unit": {"Mbps"}}
	w := do(h, "POST", "/admin/bandwidth", bad, c)
	wantCode(t, w, 422, "invalid bandwidth")
	if !strings.Contains(w.Body.String(), `value="X"`) || !strings.Contains(w.Body.String(), `aria-invalid="true"`) {
		t.Fatal("form values or error missing")
	}

	wantCode(t, do(h, "POST", "/admin/bandwidth", form, c), 303, "create")
	list, _ := q.ListBandwidths(t.Context(), db.ListBandwidthsParams{Limit: 10})
	if len(list) != 1 || list[0].Name != "10M" {
		t.Fatalf("bandwidths: %+v", list)
	}
	// duplicate name
	wantCode(t, do(h, "POST", "/admin/bandwidth", form, c), 422, "duplicate")

	id := "/admin/bandwidth/" + itoa(list[0].ID)
	form.Set("name", "20M")
	wantCode(t, do(h, "POST", id, form, c), 303, "update")
	if b, _ := q.GetBandwidth(t.Context(), list[0].ID); b.Name != "20M" {
		t.Fatal("not updated")
	}
	if w := do(h, "GET", id+"/edit", nil, c); w.Code != 200 || !strings.Contains(w.Body.String(), `value="20M"`) {
		t.Fatal("edit form")
	}
	if w := do(h, "GET", "/admin/bandwidth?q=20", nil, c); !strings.Contains(w.Body.String(), "20M") {
		t.Fatal("list search")
	}
	wantCode(t, do(h, "POST", id+"/delete", nil, c), 303, "delete")
	if _, err := q.GetBandwidth(t.Context(), list[0].ID); err != sql.ErrNoRows {
		t.Fatal("not deleted")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestRouterPasswordEncrypted(t *testing.T) {
	s, h, q, c := crudApp(t)
	form := url.Values{"name": {"R1"}, "host": {"10.0.0.1"}, "port": {"8728"}, "username": {"api"}, "password": {"hunter2"}, "enabled": {"1"}}
	wantCode(t, do(h, "POST", "/admin/routers", form, c), 303, "create")
	rs, _ := q.ListRouters(t.Context(), db.ListRoutersParams{Limit: 10})
	if len(rs) != 1 {
		t.Fatal("router not created")
	}
	r := rs[0]
	if bytes.Contains(r.PasswordEnc, []byte("hunter2")) {
		t.Fatal("password stored in plaintext")
	}
	if plain, err := secret.Open(s.SecretKey, r.PasswordEnc); err != nil || string(plain) != "hunter2" {
		t.Fatalf("open: %q %v", plain, err)
	}
	for _, p := range []string{"/admin/routers", "/admin/routers/1/edit"} {
		if w := do(h, "GET", p, nil, c); strings.Contains(w.Body.String(), "hunter2") {
			t.Fatalf("%s shows the password", p)
		}
	}
	// empty password keeps the old one
	form.Set("password", "")
	form.Set("host", "10.0.0.2")
	wantCode(t, do(h, "POST", "/admin/routers/1", form, c), 303, "update")
	r2, _ := q.GetRouter(t.Context(), r.ID)
	if r2.Host != "10.0.0.2" || !bytes.Equal(r2.PasswordEnc, r.PasswordEnc) {
		t.Fatal("password not kept")
	}
	// password required on create, port range checked
	form.Set("name", "R2")
	form.Set("port", "70000")
	w := do(h, "POST", "/admin/routers", form, c)
	wantCode(t, w, 422, "invalid router")
	if !strings.Contains(w.Body.String(), `value="R2"`) {
		t.Fatal("values lost")
	}
}

func TestPoolCRUD(t *testing.T) {
	_, h, q, c := crudApp(t)
	rt, _ := q.CreateRouter(t.Context(), db.CreateRouterParams{Name: "R1", Host: "h", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	form := url.Values{"name": {"P1"}, "range_ip": {"10.0.0.2-10.0.0.9"}, "router_id": {"1"}}
	wantCode(t, do(h, "POST", "/admin/pool", url.Values{"name": {"P1"}, "router_id": {"99"}}, c), 422, "invalid pool")
	wantCode(t, do(h, "POST", "/admin/pool", form, c), 303, "create")
	ps, _ := q.ListPools(t.Context(), db.ListPoolsParams{Limit: 10})
	if len(ps) != 1 || ps[0].RouterID != rt.ID {
		t.Fatalf("pools: %+v", ps)
	}
	if w := do(h, "GET", "/admin/pool", nil, c); !strings.Contains(w.Body.String(), "R1") {
		t.Fatal("router name not listed")
	}
	form.Set("name", "P2")
	wantCode(t, do(h, "POST", "/admin/pool/1", form, c), 303, "update")
	wantCode(t, do(h, "POST", "/admin/pool/1/delete", nil, c), 303, "delete")
	if ps, _ = q.ListPools(t.Context(), db.ListPoolsParams{Limit: 10}); len(ps) != 0 {
		t.Fatal("not deleted")
	}
}

func TestCustomerCRUD(t *testing.T) {
	s, h, q, c := crudApp(t)
	form := url.Values{"username": {"budi"}, "password": {"pw12345"}, "fullname": {"Budi Santoso"}, "phone": {"0812"},
		"service_type": {"PPPoE"}, "status": {"Active"}, "billing_day": {"15"}, "auto_renewal": {"1"}, "secret": {"routerpw"}}

	bad := url.Values{"username": {"x"}, "fullname": {""}, "email": {"nope"}, "service_type": {"PPPoE"}, "status": {"Active"}, "billing_day": {"40"}}
	w := do(h, "POST", "/admin/customers", bad, c)
	wantCode(t, w, 422, "invalid customer")
	if !strings.Contains(w.Body.String(), `value="nope"`) {
		t.Fatal("values lost")
	}

	wantCode(t, do(h, "POST", "/admin/customers", form, c), 303, "create")
	cu, err := q.GetCustomerByUsername(t.Context(), "budi")
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(cu.PasswordHash), []byte("pw12345")) != nil {
		t.Fatal("password not bcrypt")
	}
	if bytes.Contains(cu.SecretEnc, []byte("routerpw")) {
		t.Fatal("secret in plaintext")
	}
	if p, err := secret.Open(s.SecretKey, cu.SecretEnc); err != nil || string(p) != "routerpw" {
		t.Fatal("secret not sealed")
	}
	if !cu.BillingDay.Valid || cu.BillingDay.Int64 != 15 || cu.AutoRenewal != 1 || cu.ServiceType != "PPPoE" {
		t.Fatalf("fields: %+v", cu)
	}
	wantCode(t, do(h, "POST", "/admin/customers", form, c), 422, "duplicate username")

	// detail + search
	if w := do(h, "GET", "/admin/customers/1", nil, c); w.Code != 200 || !strings.Contains(w.Body.String(), "Budi Santoso") || strings.Contains(w.Body.String(), "routerpw") {
		t.Fatal("detail page")
	}
	for _, qs := range []string{"bud", "Santoso", "0812"} {
		if w := do(h, "GET", "/admin/customers?q="+qs, nil, c); !strings.Contains(w.Body.String(), "budi") {
			t.Fatalf("search %q missed", qs)
		}
	}
	if w := do(h, "GET", "/admin/customers?q=zzz", nil, c); strings.Contains(w.Body.String(), "budi") {
		t.Fatal("search matched wrongly")
	}

	// edit: empty password and secret keep current ones
	edit := url.Values{"fullname": {"Budi S"}, "service_type": {"Hotspot"}, "status": {"Banned"}}
	wantCode(t, do(h, "POST", "/admin/customers/1", edit, c), 303, "update")
	cu2, _ := q.GetCustomer(t.Context(), cu.ID)
	if cu2.Fullname != "Budi S" || cu2.PasswordHash != cu.PasswordHash || !bytes.Equal(cu2.SecretEnc, cu.SecretEnc) || cu2.BillingDay.Valid || cu2.AutoRenewal != 0 {
		t.Fatalf("update: %+v", cu2)
	}
	edit.Set("password", "newpw999")
	wantCode(t, do(h, "POST", "/admin/customers/1", edit, c), 303, "update password")
	cu3, _ := q.GetCustomer(t.Context(), cu.ID)
	if bcrypt.CompareHashAndPassword([]byte(cu3.PasswordHash), []byte("newpw999")) != nil {
		t.Fatal("password not changed")
	}

	// delete refused while a transaction exists
	_, err = q.CreateTransaction(t.Context(), db.CreateTransactionParams{Invoice: "INV1", CustomerID: cu.ID, Username: "budi",
		PlanName: "p", Type: "Balance", PeriodStart: 1, PeriodEnd: 1})
	if err != nil {
		t.Fatal(err)
	}
	w = do(h, "POST", "/admin/customers/1/delete", nil, c)
	wantCode(t, w, 303, "delete with transactions")
	w = do(h, "GET", "/admin/customers", nil, c)
	if !strings.Contains(w.Body.String(), "alert-error") || !strings.Contains(w.Body.String(), "budi") {
		t.Fatal("friendly error missing")
	}
}

func TestCustomerDeleteWithoutTransactions(t *testing.T) {
	_, h, q, c := crudApp(t)
	q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u1", PasswordHash: "x", Fullname: "U", ServiceType: "Others", Status: "Active"})
	wantCode(t, do(h, "POST", "/admin/customers/1/delete", nil, c), 303, "delete")
	if _, err := q.GetCustomer(t.Context(), 1); err != sql.ErrNoRows {
		t.Fatal("not deleted")
	}
}

func TestReportRoleReadOnly(t *testing.T) {
	_, h, q, _ := crudApp(t)
	q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u1", PasswordHash: "x", Fullname: "U", ServiceType: "Others", Status: "Active"})
	rc := login(t, h, "rita")
	form := url.Values{"name": {"X"}, "rate_down": {"1"}, "rate_down_unit": {"Mbps"}, "rate_up": {"1"}, "rate_up_unit": {"Mbps"}}
	for _, p := range []string{"/admin/bandwidth", "/admin/routers", "/admin/pool", "/admin/customers", "/admin/customers/1", "/admin/customers/1/delete"} {
		if w := do(h, "POST", p, form, rc); w.Code != http.StatusForbidden {
			t.Fatalf("POST %s: got %d", p, w.Code)
		}
	}
	for _, p := range []string{"/admin/bandwidth", "/admin/routers", "/admin/pool", "/admin/logs", "/admin/customers/new"} {
		if w := do(h, "GET", p, nil, rc); w.Code != http.StatusForbidden {
			t.Fatalf("GET %s: got %d", p, w.Code)
		}
	}
	for _, p := range []string{"/admin/customers", "/admin/customers/1"} {
		if w := do(h, "GET", p, nil, rc); w.Code != 200 {
			t.Fatalf("GET %s: got %d", p, w.Code)
		}
	}
}

func TestActivityLogWritten(t *testing.T) {
	_, h, q, c := crudApp(t)
	form := url.Values{"name": {"10M"}, "rate_down": {"10"}, "rate_down_unit": {"Mbps"}, "rate_up": {"5"}, "rate_up_unit": {"Mbps"}}
	do(h, "POST", "/admin/bandwidth", form, c)
	do(h, "POST", "/admin/bandwidth/1", form, c)
	do(h, "POST", "/admin/bandwidth/1/delete", nil, c)
	logs, _ := q.ListActivityLogs(t.Context(), db.ListActivityLogsParams{Limit: 10})
	if len(logs) != 3 {
		t.Fatalf("got %d logs", len(logs))
	}
	if l := logs[2]; l.ActorType != "admin" || l.ActorID != 1 || l.Action != "bandwidth.create" || l.Description != "10M" {
		t.Fatalf("log: %+v", l)
	}
	w := do(h, "GET", "/admin/logs?q=bandwidth.delete", nil, c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "bandwidth.delete") || strings.Contains(w.Body.String(), "bandwidth.create") {
		t.Fatal("logs page search")
	}
}
