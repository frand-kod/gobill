package customers

import (
	"path/filepath"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/core"
)

func TestMessagePlaceholders(t *testing.T) {
	c := db.Customer{Username: "budi", Fullname: "Budi", Phone: "0812"}
	n := &notify.Notifier{Settings: map[string]string{"app_url": "https://bill.example"}}
	st := map[string]string{"company_name": "ACME"}
	got := messageFor(n, c, st, "Hi [[name]] ([[username]], [[phone]]) from [[company]] [[payment_link]] [[unknown]]")
	want := "Hi Budi (budi, 0812) from ACME https://bill.example/portal/plans "
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBulkInboxSends(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	s := New(&adminx.Base{Core: &core.Core{Queries: q}})
	mk := func(user string) db.Customer {
		c, err := q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: user, PasswordHash: "x", Fullname: user + " name",
			ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := mk("bulk1")
	mk("bulk2")
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
