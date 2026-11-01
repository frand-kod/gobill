package billing

import (
	"context"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// Resolved isolations follow log_keep_days; unread inbox messages go after 180 days whatever it says.
func TestLogCleanRetentionIsolationsAndUnreadInbox(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	now := e.now
	day := func(n int) int64 { return now.AddDate(0, 0, -n).Unix() }

	iso := func(endedDaysAgo int) int64 {
		t.Helper()
		row, err := e.q.CreateIsolation(ctx, db.CreateIsolationParams{CustomerID: e.cust.ID, StartedAt: day(300), EndsAt: day(299)})
		if err != nil {
			t.Fatal(err)
		}
		if endedDaysAgo > 0 {
			if _, err := e.q.EndIsolation(ctx, db.EndIsolationParams{Now: day(endedDaysAgo), Reason: "expired", ID: row.ID}); err != nil {
				t.Fatal(err)
			}
		}
		return row.ID
	}
	oldEnded, recentEnded, running := iso(200), iso(10), iso(0)

	msg := func(readAt bool, createdDaysAgo int) int64 {
		t.Helper()
		if err := e.q.CreateInboxMessage(ctx, db.CreateInboxMessageParams{CustomerID: e.cust.ID, FromName: "a", Subject: "s", Body: "b"}); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := e.conn.QueryRowContext(ctx, "SELECT MAX(id) FROM customers_inbox").Scan(&id); err != nil {
			t.Fatal(err)
		}
		q := "UPDATE customers_inbox SET created_at = ? WHERE id = ?"
		if _, err := e.conn.ExecContext(ctx, q, day(createdDaysAgo), id); err != nil {
			t.Fatal(err)
		}
		if readAt {
			if _, err := e.conn.ExecContext(ctx, "UPDATE customers_inbox SET read_at = ? WHERE id = ?", day(createdDaysAgo), id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	unreadOld, unreadRecent, readOld := msg(false, 200), msg(false, 100), msg(true, 200)

	e.s.Now = func() time.Time { return now }
	if err := e.s.LogCleanJob(nil)(ctx); err != nil {
		t.Fatal(err)
	}

	exists := func(table string, id int64) bool {
		t.Helper()
		var n int
		if err := e.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id = ?", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	for name, c := range map[string]struct {
		table string
		id    int64
		want  bool
	}{
		"ended 200 days ago": {"isolations", oldEnded, false},
		"ended 10 days ago":  {"isolations", recentEnded, true},
		"running":            {"isolations", running, true},
		"unread 200 days":    {"customers_inbox", unreadOld, false},
		"unread 100 days":    {"customers_inbox", unreadRecent, true},
		"read 200 days":      {"customers_inbox", readOld, false},
	} {
		if got := exists(c.table, c.id); got != c.want {
			t.Errorf("%s: kept=%v, want %v", name, got, c.want)
		}
	}
}
