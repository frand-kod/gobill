package web

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// router#6: the connection mode is its own field, not implied by port 8729.
func TestRouterTLSModeSaved(t *testing.T) {
	_, h, q, c := crudApp(t)
	form := url.Values{"name": {"R1"}, "host": {"10.0.0.1"}, "port": {"9999"}, "tls": {"1"}, "username": {"api"}, "password": {"pw"}, "enabled": {"1"}}
	wantCode(t, do(h, "POST", "/admin/routers", form, c), 303, "create")
	rs, _ := q.ListRouters(t.Context(), db.ListRoutersParams{Limit: 10})
	if len(rs) != 1 || rs[0].Tls != 1 || rs[0].Port != 9999 {
		t.Fatalf("TLS on a custom port not stored: %+v", rs)
	}
	form.Set("tls", "0")
	form.Set("port", "8729") // port 8729 alone no longer means TLS
	form.Set("password", "pw")
	wantCode(t, do(h, "POST", "/admin/routers/"+itoa(rs[0].ID), form, c), 303, "update")
	got, _ := q.GetRouter(t.Context(), rs[0].ID)
	if got.Tls != 0 {
		t.Fatalf("tls = %d, want 0", got.Tls)
	}
	if body := do(h, "GET", "/admin/routers/"+itoa(rs[0].ID)+"/edit", nil, c).Body.String(); !strings.Contains(body, `name="tls"`) {
		t.Fatal("edit form has no connection mode field")
	}
}

// router#6: migration 0038 turns TLS on for the routers that used port 8729.
func TestRouterTLSMigrationBackfill(t *testing.T) {
	s, _, q, _ := crudApp(t)
	ctx := t.Context()
	r, err := q.CreateRouter(ctx, db.CreateRouterParams{Name: "old", Host: "h", Port: 8729, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil || r.Tls != 0 {
		t.Fatalf("new row: tls=%d err=%v", r.Tls, err)
	}
	sqlText, err := os.ReadFile("../db/migrations/0038_router_tls.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(sqlText), "\n") {
		if strings.HasPrefix(line, "UPDATE") {
			if _, err := s.conn.Exec(line); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got, _ := q.GetRouter(ctx, r.ID); got.Tls != 1 {
		t.Fatalf("port 8729 router tls = %d after the backfill", got.Tls)
	}
}
