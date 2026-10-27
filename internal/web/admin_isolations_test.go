package web

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestIsolationListShowsRunningAndEnded(t *testing.T) {
	_, h, q, c := crudApp(t)
	ctx, now := t.Context(), time.Now()
	cust, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "siti", PasswordHash: "h", Fullname: "Siti Aminah", ServiceType: "Hotspot", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateIsolation(ctx, db.CreateIsolationParams{CustomerID: cust.ID, StartedAt: now.Unix(), EndsAt: now.AddDate(0, 0, 3).Unix()}); err != nil {
		t.Fatal(err)
	}
	old, err := q.CreateIsolation(ctx, db.CreateIsolationParams{CustomerID: cust.ID, StartedAt: now.AddDate(0, 0, -9).Unix(), EndsAt: now.AddDate(0, 0, -2).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.EndIsolation(ctx, db.EndIsolationParams{ID: old.ID, Now: now.Unix(), Reason: "paid"}); err != nil {
		t.Fatal(err)
	}
	body := do(h, "GET", "/admin/isolations", nil, c).Body.String()
	if !strings.Contains(body, "siti") || !strings.Contains(body, "paid") || !strings.Contains(body, "running") {
		t.Fatalf("list misses rows: %s", body)
	}
	running := do(h, "GET", "/admin/isolations?status=running", nil, c).Body.String()
	if strings.Contains(running, ">paid<") {
		t.Fatal("status=running shows an ended isolation")
	}
	wantCode(t, do(h, "GET", "/admin/isolations?status=x%27--&q=%27", nil, c), 200, "bad filter")
	csv := do(h, "GET", "/admin/isolations/export?status=ended", nil, c).Body.String()
	if !strings.Contains(csv, "siti,Siti Aminah") || strings.Contains(csv, "running\n") {
		t.Fatalf("export wrong: %s", csv)
	}
	page := do(h, "GET", "/admin/customers/"+strconv.FormatInt(cust.ID, 10), nil, c).Body.String()
	if !strings.Contains(page, "/admin/isolations?q=siti") {
		t.Fatal("customer page misses the isolation link")
	}
}
