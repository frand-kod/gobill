package web

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

func TestNASSecretStrength(t *testing.T) {
	s, h, q, c := crudApp(t)
	post := func(path, ip, sec string) *httptest.ResponseRecorder {
		return do(h, "POST", path, url.Values{"name": {"n" + ip}, "ip": {ip}, "secret": {sec}}, c)
	}
	for _, bad := range []string{"short", "aaaaaaaaaaaaaaaaaaaa", "12345678901234567890"} {
		w := post("/admin/nas", "10.1.0.1", bad)
		wantCode(t, w, 422, bad)
		if !strings.Contains(w.Body.String(), `id="secret-err"`) {
			t.Fatalf("%q: no error", bad)
		}
	}
	const strong = "Xk3-9fQ_veryStrongSecret"
	wantCode(t, post("/admin/nas", "10.1.0.1", strong), 303, "strong")
	w := post("/admin/nas", "10.1.0.2", strong)
	wantCode(t, w, 422, "duplicate")
	if !strings.Contains(w.Body.String(), "already used") {
		t.Fatal("no duplicate message")
	}
	// editing the same NAS with its own secret is not a duplicate
	wantCode(t, post("/admin/nas/1", "10.1.0.1", strong), 303, "edit same")
	wantCode(t, post("/admin/nas/1", "10.1.0.1", "weak"), 422, "edit weak")
	if w := do(h, "GET", "/admin/nas/new", nil, c); !strings.Contains(w.Body.String(), "crypto.getRandomValues") {
		t.Fatal("no Generate button")
	}
	// legacy weak row: still listed, flagged
	enc, _ := secret.Seal(s.SecretKey, []byte("old"))
	q.CreateNAS(t.Context(), db.CreateNASParams{Name: "legacy", Ip: "10.9.9.9", SecretEnc: enc})
	if w := do(h, "GET", "/admin/nas", nil, c); strings.Count(w.Body.String(), "secret lemah") != 1 {
		t.Fatal("weak badge missing or on strong row")
	}
}
