package web

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func keyApp(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, _ := newTestApp(t)
	s.SecretKey = bytes.Repeat([]byte{7}, 32)
	return s, s.Handler()
}

func TestKeyDownloadRoleAndPassword(t *testing.T) {
	s, h := keyApp(t)
	alice, rita := login(t, h, "alice"), login(t, h, "rita")
	dl := keyPage + "/download"
	if w := do(h, "POST", dl, url.Values{"current": {"secret123"}}, rita); w.Code != http.StatusForbidden {
		t.Fatalf("non-SuperAdmin: %d", w.Code)
	}
	w := do(h, "POST", dl, url.Values{"current": {"wrong"}}, alice)
	if w.Code != http.StatusSeeOther || strings.Contains(w.Body.String(), hex.EncodeToString(s.SecretKey)) {
		t.Fatalf("wrong password: %d", w.Code)
	}
	w = do(h, "POST", dl, url.Values{"current": {"secret123"}}, alice)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != hex.EncodeToString(s.SecretKey) {
		t.Fatalf("download: %d %q", w.Code, w.Body.String())
	}
	var n int
	s.conn.QueryRow("SELECT COUNT(*) FROM activity_logs WHERE action = 'settings.key.download'").Scan(&n)
	if n != 1 {
		t.Fatalf("download logged %d times, want 1", n)
	}
}

func TestKeyWarningUntilSaved(t *testing.T) {
	s, h := keyApp(t)
	alice, rita := login(t, h, "alice"), login(t, h, "rita")
	warned := func(p string) bool {
		w := do(h, "GET", p, nil, alice)
		return w.Code == http.StatusOK && strings.Contains(w.Body.String(), "alert-warn")
	}
	for _, p := range []string{"/admin/status", "/admin/settings/system"} {
		if !warned(p) {
			t.Fatalf("%s: no key warning", p)
		}
	}
	if w := do(h, "POST", keyPage+"/saved", nil, rita); w.Code != http.StatusForbidden {
		t.Fatalf("mark saved by non-SuperAdmin: %d", w.Code)
	}
	do(h, "POST", keyPage+"/saved", nil, alice)
	for _, p := range []string{"/admin/status", "/admin/settings/system"} {
		if warned(p) {
			t.Fatalf("%s: warning still shown after marking saved", p)
		}
	}
	s.SecretKey = bytes.Repeat([]byte{9}, 32) // a different key shows the warning again
	if !warned("/admin/status") {
		t.Fatal("warning not shown again for a new fingerprint")
	}
}
