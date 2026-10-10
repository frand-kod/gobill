package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/secret"
	"golang.org/x/crypto/bcrypt"
)

// healthApp wires a Billing whose routers answer through exec and records the sentences sent.
func healthApp(t *testing.T, exec func([]string) ([]map[string]string, error)) (*Server, http.Handler, *db.Queries, *http.Cookie, *[]string) {
	s, h, q, c := crudApp(t)
	sent := new([]string)
	s.Billing = &billing.Service{DB: s.conn, Q: q, Key: s.SecretKey,
		RouterFor: func(db.Router) (device.Router, error) {
			return device.Router{Exec: func(_ context.Context, sn []string) ([]map[string]string, error) {
				*sent = append(*sent, sn[0])
				return exec(sn)
			}}, nil
		}}
	return s, h, q, c, sent
}

func mkRouter(t *testing.T, q *db.Queries, name string, enabled int64, user string, online sql.NullInt64, seen sql.NullInt64) db.Router {
	t.Helper()
	r, err := q.CreateRouter(t.Context(), db.CreateRouterParams{Name: name, Host: "10.1.0.1", Port: 8728, Username: user,
		PasswordEnc: []byte("x"), Enabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	if online.Valid || seen.Valid {
		if err := q.SetRouterStatus(t.Context(), db.SetRouterStatusParams{ID: r.ID, Online: online, LastSeenAt: seen}); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// mkSession stores one RADIUS session seen by nasIP; stopped=true closes it.
func mkSession(t *testing.T, s *Server, nasIP string, updated int64, stopped bool) {
	t.Helper()
	var stop sql.NullInt64
	if stopped {
		stop = sql.NullInt64{Int64: updated, Valid: true}
	}
	sid := nasIP + "-" + time.Unix(updated, 0).String()
	if _, err := s.conn.Exec(`INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, stopped_at) VALUES (?, 'u', ?, ?, ?, ?)`,
		sid, nasIP, updated-60, updated, stop); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkCardRowsAndStatus(t *testing.T) {
	s, _, q, _, _ := healthApp(t, nil)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, s.location())
	on, off := sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true}
	seen := sql.NullInt64{Int64: now.Unix() - 120, Valid: true}
	mkRouter(t, q, "core", 1, "api", on, seen)
	mkRouter(t, q, "edge", 1, "api", off, sql.NullInt64{})
	mkRouter(t, q, "fresh", 1, "api", sql.NullInt64{}, sql.NullInt64{})
	mkRouter(t, q, "parked", 0, "api", on, seen)

	n, err := q.CreateNAS(t.Context(), db.CreateNASParams{Name: "nas-live", Ip: "10.0.0.1", SecretEnc: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	mkSession(t, s, "10.0.0.1", now.Unix()-30, false)
	mkSession(t, s, "10.0.0.1", now.Unix()-40, false)
	q.CreateNAS(t.Context(), db.CreateNASParams{Name: "nas-old", Ip: "10.0.0.2", SecretEnc: []byte("x")})
	mkSession(t, s, "10.0.0.2", now.Unix()-3*3600, true)
	q.CreateNAS(t.Context(), db.CreateNASParams{Name: "nas-none", Ip: "10.0.0.3", SecretEnc: []byte("x")})

	card, err := s.networkCard(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]netRow{}
	for _, r := range card.Rows {
		byName[r.Name] = r
	}
	// the test app runs in Indonesian (the default language)
	want := map[string]struct{ kind, status, metric string }{
		"core":     {"router", "Online", "Terakhir online: "},
		"edge":     {"router", "Offline", "Belum pernah online"},
		"fresh":    {"router", "Unknown", "Belum pernah online"},
		"parked":   {"router", "Unknown", "Nonaktif, tidak dicek"},
		"nas-live": {"nas", "Online", "2 sesi aktif"},
		"nas-old":  {"nas", "Offline", "0 sesi aktif"},
		"nas-none": {"nas", "Unknown", "belum ada paket"},
	}
	if len(card.Rows) != len(want) {
		t.Fatalf("%d rows, want %d: %+v", len(card.Rows), len(want), card.Rows)
	}
	for name, w := range want {
		r, ok := byName[name]
		if !ok {
			t.Fatalf("missing row %s", name)
		}
		if r.Kind != w.kind || r.Status != w.status || !strings.Contains(r.Metric, w.metric) {
			t.Errorf("%s: got %+v, want %+v", name, r, w)
		}
	}
	if card.Online != 2 {
		t.Fatalf("online %d, want 2 (core, nas-live)", card.Online)
	}
	if byName["nas-live"].Href != netHref("nas", n.ID) {
		t.Fatalf("href %q", byName["nas-live"].Href)
	}
}

func TestNetworkDashboardCard(t *testing.T) {
	s, h, q, c, _ := healthApp(t, nil)
	mkRouter(t, q, "core", 1, "api", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{})
	body := do(h, "GET", "/admin", nil, c).Body.String()
	if !strings.Contains(body, `id="network-summary"`) || !strings.Contains(body, "1/1") || !strings.Contains(body, "core") {
		t.Fatalf("card missing: %s", body)
	}
	if !strings.Contains(body, "/admin/network/router/") {
		t.Fatal("no detail link")
	}

	// no devices: the empty state links to both add forms
	_, h2, _, c2, _ := healthApp(t, nil)
	with := do(h2, "GET", "/admin", nil, c2).Body.String()
	if !strings.Contains(with, "/admin/routers/new") || !strings.Contains(with, "/admin/nas/new") {
		t.Fatal("empty state has no add links")
	}

	// the card is for managers only
	agent := roleLogin(t, s, h, "Agent")
	if strings.Contains(do(h, "GET", "/admin", nil, agent).Body.String(), `id="network-title"`) {
		t.Fatal("agent sees the network card")
	}
}

// roleLogin creates an admin with the given role (username = role, password secret123) and logs in.
func roleLogin(t *testing.T, s *Server, h http.Handler, role string) *http.Cookie {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	if _, err := db.New(s.conn).CreateAdmin(t.Context(), db.CreateAdminParams{Username: role, Fullname: role, PasswordHash: string(hash), Role: role}); err != nil {
		t.Fatal(err)
	}
	return login(t, h, role)
}

func TestNetworkDetailRoles(t *testing.T) {
	s, h, q, c, _ := healthApp(t, nil)
	r := mkRouter(t, q, "core", 1, "api", sql.NullInt64{}, sql.NullInt64{})
	n, _ := q.CreateNAS(t.Context(), db.CreateNASParams{Name: "nas", Ip: "10.0.0.9", SecretEnc: []byte("x")})
	wantCode(t, do(h, "GET", netHref("router", r.ID), nil, c), 200, "router detail")
	wantCode(t, do(h, "GET", netHref("nas", n.ID), nil, c), 200, "nas detail")
	wantCode(t, do(h, "GET", "/admin/network/bogus/1", nil, c), 404, "bad kind")
	wantCode(t, do(h, "GET", "/admin/network/router/999", nil, c), 404, "missing router")
	wantCode(t, do(h, "POST", netHref("nas", n.ID)+"/check", nil, c), 404, "check on NAS")
	for _, role := range []string{"Agent", "Report"} {
		cookie := roleLogin(t, s, h, role)
		wantCode(t, do(h, "GET", netHref("router", r.ID), nil, cookie), 403, role)
		wantCode(t, do(h, "POST", netHref("router", r.ID)+"/check", nil, cookie), 403, role+" check")
	}
}

func TestNetworkLiveCheck(t *testing.T) {
	var fail error
	s, h, q, c, sent := healthApp(t, func(sn []string) ([]map[string]string, error) {
		if fail != nil {
			return nil, fail
		}
		switch sn[0] {
		case "/system/resource/print":
			return []map[string]string{{"version": "7.14.3", "board-name": "RB750Gr3", "uptime": "3d4h", "cpu-load": "12",
				"free-memory": "268435456", "total-memory": "536870912"}}, nil
		case "/ip/hotspot/active/print":
			return []map[string]string{{".id": "*1"}, {".id": "*2"}}, nil
		}
		return []map[string]string{{".id": "*1"}, {".id": "*2"}, {".id": "*3"}}, nil
	})
	r := mkRouter(t, q, "core", 1, "api", sql.NullInt64{}, sql.NullInt64{})
	check := netHref("router", r.ID) + "/check"

	w := do(h, "POST", check, nil, c)
	wantCode(t, w, 200, "check")
	body := w.Body.String()
	for _, want := range []string{"7.14.3", "RB750Gr3", "3d4h", "12%", "256 MB bebas dari 512 MB"} {
		if !strings.Contains(body, want) {
			t.Fatalf("no %q in %s", want, body)
		}
	}
	if strings.Join(*sent, " ") != "/system/resource/print /ip/hotspot/active/print /ppp/active/print" {
		t.Fatalf("sent %v", *sent)
	}

	// a timeout is mapped to the plain message; the raw text stays in technical details
	fail = errors.New("dial tcp 10.1.0.1:8728: i/o timeout")
	body = do(h, "POST", check, nil, c).Body.String()
	if !strings.Contains(body, s.catalog.T(s.language(), msgRouterTimeout)) || !strings.Contains(body, "i/o timeout") {
		t.Fatalf("timeout not mapped: %s", body)
	}
	if strings.Contains(body, "7.14.3") {
		t.Fatal("stale live data shown after a failure")
	}

	// no API user: no check button, and the check does not reach the router
	*sent = nil
	bare := mkRouter(t, q, "bare", 1, "", sql.NullInt64{}, sql.NullInt64{})
	page := do(h, "GET", netHref("router", bare.ID), nil, c).Body.String()
	if strings.Contains(page, `action="`+netHref("router", bare.ID)+`/check"`) {
		t.Fatal("check button shown without API user")
	}
	wantCode(t, do(h, "POST", netHref("router", bare.ID)+"/check", nil, c), 200, "bare check")
	if len(*sent) != 0 {
		t.Fatalf("router contacted without credentials: %v", *sent)
	}
}

func TestNASDetailSecretStrength(t *testing.T) {
	s, h, q, c, _ := healthApp(t, nil)
	weak, _ := secret.Seal(s.SecretKey, []byte("short"))
	strong, _ := secret.Seal(s.SecretKey, []byte("s3cretvalue-0123456789"))
	wk, _ := q.CreateNAS(t.Context(), db.CreateNASParams{Name: "weak", Ip: "10.0.0.5", SecretEnc: weak})
	st, _ := q.CreateNAS(t.Context(), db.CreateNASParams{Name: "strong", Ip: "10.0.0.6", SecretEnc: strong})
	body := do(h, "GET", netHref("nas", wk.ID), nil, c).Body.String()
	if !strings.Contains(body, "Secret minimal 16 karakter") {
		t.Fatalf("weak secret not flagged: %s", body)
	}
	body = do(h, "GET", netHref("nas", st.ID), nil, c).Body.String()
	if !strings.Contains(body, "Cukup kuat") || strings.Contains(body, "s3cretvalue") {
		t.Fatalf("strong secret wrong or leaked: %s", body)
	}
}
