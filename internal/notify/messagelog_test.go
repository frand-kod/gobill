package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func TestSendAttemptsAreLogged(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
	defer srv.Close()
	n := nt(map[string]string{"sms_url": srv.URL + "/sms?n=[number]&t=[text]", "telegram_bot": "123:secret", "telegram_target_id": "-1"})
	n.TelegramAPI = srv.URL
	n.Log = LogTo(q)
	ctx := context.Background()

	if err := n.SMS(ctx, "0812", "hello"); err != nil {
		t.Fatal(err)
	}
	code = http.StatusBadGateway
	if err := n.SMS(ctx, "0813", "again"); err == nil {
		t.Fatal("want gateway error")
	}
	if err := n.Telegram(ctx, "alert"); err == nil {
		t.Fatal("want telegram error")
	}
	rows, err := q.SearchMessageLogs(ctx, db.SearchMessageLogsParams{PageLimit: -1})
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows %d err %v", len(rows), err)
	}
	// newest first
	if r := rows[0]; r.Channel != "telegram" || r.Status != "error" || r.Body != "alert" || strings.Contains(r.Error, "secret") {
		t.Fatalf("telegram row (token must be redacted): %+v", r)
	}
	if r := rows[1]; r.Channel != "sms" || r.Recipient != "0813" || r.Status != "error" || !strings.Contains(r.Error, "502") {
		t.Fatalf("sms error row: %+v", r)
	}
	if r := rows[2]; r.Status != "ok" || r.Recipient != "0812" || r.Body != "hello" || r.Error != "" {
		t.Fatalf("sms ok row: %+v", r)
	}
}
