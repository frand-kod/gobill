package billing

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

var invoiceRe = regexp.MustCompile(`^INV-\d{4}-\d{6,}$`)

// Invoices are INV-YYMM-NNNNNN: YYMM is the month in the service zone, NNNNNN the global
// sequence (last transaction id + 1), zero-padded and never truncated.
func TestInvoiceFormatAndPadding(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	for i := 0; i < 1531; i++ { // ids 1..1531 already used
		if _, err := e.q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: fmt.Sprintf("OLD-%d", i),
			Username: "u", PlanName: "p", RouterName: "balance", Type: "Balance", Method: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	tx := func(now time.Time) string {
		t.Helper()
		var inv string
		if err := e.s.tx(ctx, func(q *db.Queries) error {
			var err error
			inv, err = e.s.nextInvoice(ctx, q, now)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return inv
	}
	if got, want := tx(e.now), "INV-2501-001532"; got != want {
		t.Fatalf("invoice %q want %q", got, want)
	}
	if got := tx(e.now); !invoiceRe.MatchString(got) {
		t.Fatalf("invoice %q does not match %v", got, invoiceRe)
	}
}

// The month comes from the given time converted to the service zone, not from UTC or the host.
func TestInvoiceMonthFromServiceZone(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	var inv string
	err := e.s.tx(ctx, func(q *db.Queries) error {
		var err error
		inv, err = e.s.nextInvoice(ctx, q, time.Date(2025, 1, 31, 23, 30, 0, 0, time.UTC)) // 2025-02-01 06:30 WIB
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "INV-2502-000001"; inv != want {
		t.Fatalf("invoice %q want %q", inv, want)
	}
}

// Across a month boundary the month part changes but the sequence keeps counting.
func TestInvoiceSequenceAcrossMonth(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	e.now = time.Date(2025, 2, 1, 9, 0, 0, 0, jkt)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.GetTransactionByInvoice(ctx, "INV-2501-000001"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.GetTransactionByInvoice(ctx, "INV-2502-000002"); err != nil {
		t.Fatal(err)
	}
}
