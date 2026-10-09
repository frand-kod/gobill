package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestTransportErrorsAreRedacted(t *testing.T) {
	conn, _ := db.Open(filepath.Join(t.TempDir(), "t.db"))
	defer conn.Close()
	db.Migrate(conn)
	q := db.New(conn)
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close() // connection refused
	n := nt(map[string]string{"sms_url": dead + "/s?key=APIKEY123&t=[text]", "telegram_bot": "123:BOTTOKEN", "telegram_target_id": "-1"})
	n.TelegramAPI = dead
	n.Log = LogTo(q)
	ctx := context.Background()
	errs := []error{n.SMS(ctx, "0812", "OTP 654321"), n.Telegram(ctx, "OTP 654321")}
	rows, _ := q.SearchMessageLogs(ctx, db.SearchMessageLogsParams{PageLimit: -1})
	if len(rows) != 2 {
		t.Fatalf("rows %d", len(rows))
	}
	all := rows[0].Error + "|" + rows[1].Error
	for _, e := range errs {
		if e == nil {
			t.Fatal("want error")
		}
		all += "|" + e.Error()
	}
	for _, bad := range []string{"BOTTOKEN", "APIKEY123", "654321", "key="} {
		if strings.Contains(all, bad) {
			t.Fatalf("%q leaked: %s", bad, all)
		}
	}
}
