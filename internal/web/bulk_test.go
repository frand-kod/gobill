package web

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

func bulkAgent(t *testing.T, e *billEnv, role string) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	if _, err := e.q.CreateAdmin(t.Context(), db.CreateAdminParams{Username: role, Fullname: role, PasswordHash: string(hash), Role: role}); err != nil {
		t.Fatal(err)
	}
}

func vchCount(t *testing.T, e *billEnv) int {
	t.Helper()
	rows, err := e.q.ListVouchers(t.Context(), db.ListVouchersParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func TestVoucherBulkDelete(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	bulkAgent(t, e, "Agent")
	bulkAgent(t, e, "Sales")
	p := e.plan(t, "P", "PPPoE", 1000)
	var ids []string
	for _, c := range []string{"AAA", "BBB", "CCC", "DDD"} {
		v, err := e.q.CreateVoucher(ctx, db.CreateVoucherParams{Code: c, PlanID: p.ID})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, itoa(v.ID))
	}
	// BBB is used (PHP voucher-delete-many deletes used ones too)
	if _, err := e.q.UseVoucher(ctx, db.UseVoucherParams{UsedBy: sql.NullInt64{Int64: e.cust.ID, Valid: true}, ID: mustAtoi(ids[1])}); err != nil {
		t.Fatal(err)
	}

	agent := login(t, e.h, "Agent")
	wantCode(t, do(e.h, "POST", "/admin/vouchers/delete-many", map[string][]string{"ids": ids}, agent), 403, "agent")
	if vchCount(t, e) != 4 {
		t.Fatal("agent deleted vouchers")
	}
	// bad ids: nothing deleted, redirect back with an error flash
	for _, bad := range [][]string{nil, {"x"}, {ids[0], "-1"}, {ids[0], "1; DROP TABLE vouchers"}, {ids[0], "0"}, {"1e3"}} {
		w := do(e.h, "POST", "/admin/vouchers/delete-many", map[string][]string{"ids": bad}, e.c)
		if w.Code != 303 || vchCount(t, e) != 4 {
			t.Fatalf("bad ids %v: %d, %d left", bad, w.Code, vchCount(t, e))
		}
	}
	tooMany := make([]string, maxBulkIDs+1)
	for i := range tooMany {
		tooMany[i] = "1"
	}
	do(e.h, "POST", "/admin/vouchers/delete-many", map[string][]string{"ids": tooMany}, e.c)
	if vchCount(t, e) != 4 {
		t.Fatal("over-cap request deleted vouchers")
	}

	// good: three of four (one used), a nonexistent id is ignored, count in the flash and one log entry
	w := do(e.h, "POST", "/admin/vouchers/delete-many", map[string][]string{"ids": {ids[0], ids[1], ids[2], "99999"}}, e.c)
	wantCode(t, w, 303, "delete many")
	if vchCount(t, e) != 1 {
		t.Fatalf("left %d", vchCount(t, e))
	}
	list := do(e.h, "GET", "/admin/vouchers", nil, e.c).Body.String()
	if !strings.Contains(list, "3 Data Deleted Successfully") && !strings.Contains(list, "3 Data Berhasil Dihapus") {
		t.Fatalf("no count flash: %s", list)
	}
	logs, _ := e.q.SearchActivityLogs(ctx, db.SearchActivityLogsParams{Q: "voucher.delete_many", PageLimit: 10})
	if len(logs) != 1 || logs[0].Description != "3" {
		t.Fatalf("logs %+v", logs)
	}
}

func mustAtoi(s string) int64 { n, _ := posInt(s); return n }

func TestVoucherRemoveOld(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	bulkAgent(t, e, "Agent")
	p := e.plan(t, "P", "PPPoE", 1000)
	old := time.Now().AddDate(0, -4, 0).Unix()
	for _, c := range []string{"OLD1", "OLD2", "NEW1", "UNUSED", "KEEP"} {
		v, err := e.q.CreateVoucher(ctx, db.CreateVoucherParams{Code: c, PlanID: p.ID})
		if err != nil {
			t.Fatal(err)
		}
		at := old
		if c == "NEW1" {
			at = time.Now().Unix()
		}
		if c != "UNUSED" {
			if _, err := e.s.conn.Exec(`UPDATE vouchers SET status='used', used_by=?, used_at=? WHERE id=?`, e.cust.ID, at, v.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	// KEEP is still the method of a live subscription (PHP skips those)
	sub, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: p.ID, Type: "PPPoE",
		StartedAt: old, ExpiresAt: time.Now().Add(time.Hour).Unix(), Method: "Voucher - KEEP"})
	if err != nil {
		t.Fatal(err)
	}
	_ = sub

	wantCode(t, do(e.h, "POST", "/admin/vouchers/remove-old", nil, login(t, e.h, "Agent")), 403, "agent")
	if vchCount(t, e) != 5 {
		t.Fatal("agent removed vouchers")
	}
	wantCode(t, do(e.h, "POST", "/admin/vouchers/remove-old", nil, e.c), 303, "remove old")
	if n := vchCount(t, e); n != 3 {
		t.Fatalf("left %d, want NEW1, UNUSED, KEEP", n)
	}
	if _, err := e.q.GetVoucherByCode(ctx, "OLD1"); err == nil {
		t.Fatal("OLD1 survived")
	}
	logs, _ := e.q.SearchActivityLogs(ctx, db.SearchActivityLogsParams{Q: "voucher.remove_old", PageLimit: 10})
	if len(logs) != 1 || logs[0].Description != "2" {
		t.Fatalf("logs %+v", logs)
	}
}

func TestCouponBulkDelete(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	bulkAgent(t, e, "Agent")
	bulkAgent(t, e, "Sales")
	var ids []string
	for _, c := range []string{"C1", "C2", "C3"} {
		wantCode(t, do(e.h, "POST", "/admin/coupons", cpnForm(c), e.c), 303, "create")
		cp, _ := e.q.GetCouponByCode(ctx, c)
		ids = append(ids, itoa(cp.ID))
	}
	wantCode(t, do(e.h, "POST", "/admin/coupons/delete-many", map[string][]string{"ids": ids}, login(t, e.h, "Agent")), 403, "agent")
	wantCode(t, do(e.h, "POST", "/admin/coupons/delete-many", map[string][]string{"ids": {"abc"}}, e.c), 303, "bad id")
	left := func() int {
		l, _ := e.q.SearchCoupons(ctx, db.SearchCouponsParams{PageLimit: 10})
		return len(l)
	}
	if left() != 3 {
		t.Fatal("bad request deleted coupons")
	}
	// Sales may delete, like PHP
	wantCode(t, do(e.h, "POST", "/admin/coupons/delete-many", map[string][]string{"ids": ids[:2]}, login(t, e.h, "Sales")), 303, "sales")
	if left() != 1 {
		t.Fatalf("left %d", left())
	}
}

func TestListSortWhitelist(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	p := e.plan(t, "P", "PPPoE", 1000)
	for i, price := range []int64{300, 100, 200} {
		if _, err := e.q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: "INV" + itoa(int64(i)), Username: "u" + itoa(int64(i)), PlanName: "P",
			PlanID: sql.NullInt64{Int64: p.ID, Valid: true}, Type: "Hotspot", Price: price}); err != nil {
			t.Fatal(err)
		}
	}
	if s, d, param := listSort(httptest.NewRequest("GET", "/x?sort=amount&dir=desc", nil), "date", "amount"); s != "amount" || d != "desc" || param != "amount_desc" {
		t.Fatalf("%q %q %q", s, d, param)
	}
	for _, bad := range []string{"/x?sort=id;DROP", "/x?sort=password_hash", "/x?sort=", "/x"} {
		if s, d, param := listSort(httptest.NewRequest("GET", bad, nil), "date", "amount"); s != "" || param != "" || d != "asc" {
			t.Fatalf("%s accepted: %q %q %q", bad, s, d, param)
		}
	}
	// the list honours the sort, and unknown keys keep the default order (newest id first)
	body := func(q string) string { return do(e.h, "GET", "/admin/transactions"+q, nil, e.c).Body.String() }
	asc := body("?sort=amount&dir=asc")
	if !(strings.Index(asc, "INV1") < strings.Index(asc, "INV2") && strings.Index(asc, "INV2") < strings.Index(asc, "INV0")) {
		t.Fatal("amount asc order wrong")
	}
	def := body("?sort=bogus")
	if !(strings.Index(def, "INV2") < strings.Index(def, "INV1") && strings.Index(def, "INV1") < strings.Index(def, "INV0")) {
		t.Fatal("default order changed")
	}
	wantCode(t, do(e.h, "GET", "/admin/subscriptions?sort=expires&dir=asc", nil, e.c), 200, "subs sort")
	wantCode(t, do(e.h, "GET", "/admin/subscriptions?sort=x%27--", nil, e.c), 200, "subs bad sort")
}

func TestCustomerSelectedMessage(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	bulkAgent(t, e, "Sales")
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "message_delay", Value: "0"}); err != nil {
		t.Fatal(err)
	}
	c2, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "Two", ServiceType: "Others", Status: "Active"})
	c3, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u3", PasswordHash: "h", Fullname: "Three", ServiceType: "Others", Status: "Active"})

	wantCode(t, do(e.h, "POST", "/admin/customers/message", nil, login(t, e.h, "rita")), 403, "report")
	wantCode(t, do(e.h, "POST", "/admin/customers/message", map[string][]string{"ids": {"zz"}}, e.c), 303, "bad ids")
	w := do(e.h, "POST", "/admin/customers/message", map[string][]string{"ids": {itoa(e.cust.ID), itoa(c3.ID)}}, login(t, e.h, "Sales"))
	wantCode(t, w, 200, "compose")
	want := `name="ids" value="` + itoa(e.cust.ID) + "," + itoa(c3.ID) + `"`
	if !strings.Contains(w.Body.String(), want) {
		t.Fatalf("hidden ids missing: %s", w.Body.String())
	}

	form := map[string][]string{"ids": {itoa(e.cust.ID) + "," + itoa(c3.ID)}, "channel": {"inbox"}, "subject": {"Hi"}, "message": {"Hello [[name]]"}}
	wantCode(t, do(e.h, "POST", "/admin/message/selected", map[string][]string{"ids": {"1,x"}, "channel": {"inbox"}, "message": {"m"}}, e.c), 303, "bad csv")
	wantCode(t, do(e.h, "POST", "/admin/message/selected", map[string][]string{"ids": form["ids"], "channel": {"inbox"}}, e.c), 422, "no message")
	wantCode(t, do(e.h, "POST", "/admin/message/selected", form, e.c), 303, "send")
	for i := 0; i < 100; i++ {
		bulk.mu.Lock()
		done := !bulk.active
		bulk.mu.Unlock()
		if done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := func(id int64) int {
		l, _ := e.q.ListInboxByCustomer(ctx, db.ListInboxByCustomerParams{CustomerID: id, Limit: 10})
		return len(l)
	}
	if got(e.cust.ID) != 1 || got(c3.ID) != 1 || got(c2.ID) != 0 {
		t.Fatalf("inbox counts %d %d %d", got(e.cust.ID), got(c3.ID), got(c2.ID))
	}
}
