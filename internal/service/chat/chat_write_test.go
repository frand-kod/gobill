package chat

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
)

type fakeBiller struct {
	price     int64
	recharges int
}

func (f *fakeBiller) Preview(_ context.Context, _, planID int64) (billing.RechargePreview, error) {
	return billing.RechargePreview{Plan: db.Plan{ID: planID}, Price: f.price}, nil
}

func (f *fakeBiller) Recharge(context.Context, int64, int64, string, int64) error {
	f.recharges++
	return nil
}

var codeRe = regexp.MustCompile(`\b\d{6}\b`)

// writeSetup is a Service with the write deps and a clock the test moves.
func writeSetup(t *testing.T) (*Service, *fakeBiller, *time.Time, Caller) {
	t.Helper()
	s, q := setup(t)
	bw, err := q.CreateBandwidth(context.Background(), db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreatePlan(context.Background(), db.CreatePlanParams{Name: "G10", BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}, Type: "PPPoE", Billing: "prepaid", Price: 100000,
		Validity: 30, ValidityUnit: "Days", Enabled: 1, Device: "Radius", Audience: "public"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	fb := &fakeBiller{price: 100000}
	s.Now = func() time.Time { return now }
	s.Confirm, s.Billing = NewConfirmStore(), fb
	s.Disconnect = func(context.Context, string) error { return nil }
	boss, err := s.Identify(context.Background(), "0811111111")
	if err != nil {
		t.Fatal(err)
	}
	return s, fb, &now, boss
}

func ask(t *testing.T, s *Service, c Caller, text string) (string, string) {
	t.Helper()
	return s.Reply(context.Background(), c, text)
}

func codeOf(t *testing.T, reply string) string {
	t.Helper()
	m := codeRe.FindString(reply)
	if m == "" {
		t.Fatalf("no code in %q", reply)
	}
	return m
}

func TestWriteNeedsCode(t *testing.T) {
	s, fb, _, boss := writeSetup(t)
	reply, _ := ask(t, s, boss, "perpanjang budi G10")
	if fb.recharges != 0 || !strings.Contains(reply, "Rp") {
		t.Fatalf("first message must only preview: %q (%d)", reply, fb.recharges)
	}
	code := codeOf(t, reply)
	out, _ := ask(t, s, boss, code)
	if fb.recharges != 1 || !strings.Contains(out, "Done") {
		t.Fatalf("confirm: %q (%d)", out, fb.recharges)
	}
	ask(t, s, boss, code) // the code is single use
	if fb.recharges != 1 {
		t.Fatalf("second confirm ran again: %d", fb.recharges)
	}
	var n int64
	l, _ := s.Q.ListActivityLogs(context.Background(), db.ListActivityLogsParams{Limit: 50})
	for _, x := range l {
		if x.Action == "customer.recharge" && strings.Contains(x.Description, "source=wa") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("activity log entries: %d", n)
	}
}

func TestWriteWrongCodeThreeTimes(t *testing.T) {
	s, fb, _, boss := writeSetup(t)
	code := codeOf(t, mustReply(ask(t, s, boss, "perpanjang budi G10")))
	bad := "000000"
	if code == bad {
		bad = "111111"
	}
	for i := 0; i < 3; i++ {
		ask(t, s, boss, bad)
	}
	ask(t, s, boss, code)
	if fb.recharges != 0 {
		t.Fatal("pending action survived three wrong codes")
	}
}

func mustReply(r, _ string) string { return r }

func TestWriteExpired(t *testing.T) {
	s, fb, now, boss := writeSetup(t)
	code := codeOf(t, mustReply(ask(t, s, boss, "perpanjang budi G10")))
	*now = now.Add(5*time.Minute + time.Second)
	out, _ := ask(t, s, boss, code)
	if fb.recharges != 0 || !strings.Contains(out, "expired") {
		t.Fatalf("expired code ran: %q", out)
	}
}

func TestWriteOtherPhoneOrAdmin(t *testing.T) {
	s, fb, _, boss := writeSetup(t)
	code := codeOf(t, mustReply(ask(t, s, boss, "perpanjang budi G10")))
	other := boss
	other.Phone = "628999"
	ask(t, s, other, code)
	sales, _ := s.Identify(context.Background(), "0833333333")
	ask(t, s, sales, code)
	if fb.recharges != 0 {
		t.Fatal("code worked for another phone or admin")
	}
	ask(t, s, boss, code)
	if fb.recharges != 1 {
		t.Fatal("the right phone must still confirm")
	}
}

func TestWriteReplacesPending(t *testing.T) {
	s, fb, _, boss := writeSetup(t)
	first := codeOf(t, mustReply(ask(t, s, boss, "perpanjang budi G10")))
	second := codeOf(t, mustReply(ask(t, s, boss, "perpanjang ani G10")))
	if first == second {
		t.Skip("same random code twice")
	}
	ask(t, s, boss, first)
	if fb.recharges != 0 {
		t.Fatal("replaced action ran")
	}
	ask(t, s, boss, second)
	if fb.recharges != 1 {
		t.Fatal("new action did not run")
	}
}

func TestWriteRoles(t *testing.T) {
	s, fb, _, _ := writeSetup(t)
	rita, _ := s.Identify(context.Background(), "0822222222")
	if out, name := ask(t, s, rita, "perpanjang budi G10"); name != "" || strings.Contains(out, "Rp") {
		t.Fatalf("Report may not recharge: %q", out)
	}
	sales, _ := s.Identify(context.Background(), "0833333333")
	if _, name := ask(t, s, sales, "perpanjang budi G10"); name != "perpanjang" {
		t.Fatal("Sales may recharge")
	}
	if _, name := ask(t, s, sales, "putus budi"); name != "" {
		t.Fatal("Sales may not disconnect")
	}
	cust, _ := s.Identify(context.Background(), "628123456789")
	if _, name := ask(t, s, cust, "perpanjang budi G10"); name != "" {
		t.Fatal("customer called a write tool")
	}
	if fb.recharges != 0 {
		t.Fatal("recharged")
	}
}

func TestWritePriceChanged(t *testing.T) {
	s, fb, _, boss := writeSetup(t)
	code := codeOf(t, mustReply(ask(t, s, boss, "perpanjang budi G10")))
	fb.price = 150000
	out, _ := ask(t, s, boss, code)
	if fb.recharges != 0 || !strings.Contains(out, "price changed") {
		t.Fatalf("price change must abort: %q", out)
	}
}

func TestWriteRoleLostBeforeConfirm(t *testing.T) {
	s, fb, _, _ := writeSetup(t)
	ctx := context.Background()
	sales, _ := s.Identify(ctx, "0833333333")
	code := codeOf(t, mustReply(ask(t, s, sales, "perpanjang budi G10")))
	if _, err := s.Q.UpdateAdmin(ctx, db.UpdateAdminParams{Username: "sales", Role: "Report", Status: "Active", Phone: "0833333333", ID: sales.AdminID}); err != nil {
		t.Fatal(err)
	}
	ask(t, s, sales, code)
	if fb.recharges != 0 {
		t.Fatal("role was not checked again")
	}
}
