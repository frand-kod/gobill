package web

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/web/core"
)

// A 2FA change logs the admin out of other sessions and keeps the current one.
func TestTOTPChangeEndsOtherSessions(t *testing.T) {
	s, _ := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	h := s.Handler()
	state := func(c *http.Cookie) int { return do(h, "GET", "/admin/2fa", nil, c).Code }
	check := func(label string, cur, other *http.Cookie) {
		t.Helper()
		if c, o := state(cur), state(other); c != 200 || o != 303 {
			t.Fatalf("%s: current %d (want 200), other %d (want 303)", label, c, o)
		}
	}

	// enable
	cur, other := login(t, h, "alice"), login(t, h, "alice")
	enrollTOTP(t, s, h, cur, "alice")
	check("enable", cur, other)

	// disable: 2FA is switched on straight in the database so a second session can exist
	cur2, other2 := login(t, h, "rita"), login(t, h, "rita")
	do(h, "POST", "/admin/2fa/setup", nil, cur2)
	rita, _ := s.Queries.GetAdminByUsername(t.Context(), "rita")
	if err := s.Queries.EnableAdminTOTP(t.Context(), rita.ID); err != nil {
		t.Fatal(err)
	}
	key, err := s.TotpOpen(rita.TotpSecretEnc)
	if err != nil {
		t.Fatal(err)
	}
	do(h, "POST", "/admin/2fa/disable", url.Values{"current": {"secret123"}, "code": {core.TotpCode(key, time.Now().Unix()/30)}}, cur2)
	check("disable", cur2, other2)

	// SuperAdmin reset ends the target's sessions
	do(h, "POST", "/admin/2fa/setup", nil, cur2)
	if err := s.Queries.EnableAdminTOTP(t.Context(), rita.ID); err != nil {
		t.Fatal(err)
	}
	if state(cur2) != 200 {
		t.Fatal("rita session lost before the reset")
	}
	do(h, "POST", "/admin/users/"+strconv.FormatInt(rita.ID, 10)+"/2fa/reset", nil, cur)
	if w := state(cur2); w != 303 {
		t.Fatalf("rita after reset: %d, want 303", w)
	}
}
