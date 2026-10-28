package web

import (
	"strings"
	"testing"
	"time"
)

// The customer page: the Usage card has the daily chart once there is usage, and an empty field such as
// Email is left out of the customer data instead of showing a dash.
func TestCustomerPageUsageCardAndEmptyFields(t *testing.T) {
	e := englishApp(t)
	page := "/admin/customers/" + itoa(e.cust.ID)
	body := do(e.h, "GET", page, nil, e.c).Body.String()
	if !strings.Contains(body, "No usage recorded yet") {
		t.Fatal("empty usage should say so")
	}
	if strings.Contains(body, `<dt class="text-xs text-ink-2">Email</dt>`) {
		t.Fatal("empty email is shown")
	}
	if !strings.Contains(body, `<span class="text-base font-normal text-ink-2">`+e.cust.Fullname+`</span>`) {
		t.Fatal("filled full name is missing from the header")
	}

	day := time.Now().In(e.srv.location()).Format("2006-01-02")
	if _, err := e.s.conn.ExecContext(t.Context(), `INSERT INTO radius_usage_daily (username, day, input_octets, output_octets) VALUES (?, ?, ?, ?)`,
		e.cust.Username, day, 1<<20, 3<<20); err != nil {
		t.Fatal(err)
	}
	body = do(e.h, "GET", page, nil, e.c).Body.String()
	for _, want := range []string{`id="cust-usage-title"`, `class="big-num"`, `aria-label="Daily usage, last 7 days"`,
		`aria-label="Daily usage, last 30 days"`, "4.00 MB", "7 days", "30 days"} {
		if !strings.Contains(body, want) {
			t.Errorf("usage card missing %q", want)
		}
	}
	if strings.Contains(body, "No usage recorded yet") {
		t.Error("usage with rows still says none recorded")
	}
}
