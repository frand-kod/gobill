package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B: SHA-1 secret "12345678901234567890"; 8-digit values truncated to 6 digits.
func TestTOTPRFC6238(t *testing.T) {
	key := []byte("12345678901234567890")
	for _, c := range []struct {
		T    int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}} {
		if got := totpCode(key, c.T/30); got != c.want {
			t.Errorf("T=%d: got %s want %s", c.T, got, c.want)
		}
	}
}

var recoveryRe = regexp.MustCompile(`[A-Z2-7]{4}-[A-Z2-7]{4}`)

// sessionCookie returns the session cookie set by w, or nil.
func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "gobill_session" {
			return c
		}
	}
	return nil
}

// enrollTOTP turns 2FA on for the logged-in admin through the real pages. It returns the key and
// the recovery codes. The first code is for the current step; logins later use the next step.
func enrollTOTP(t *testing.T, s *Server, h http.Handler, cookie *http.Cookie, user string) ([]byte, []string) {
	t.Helper()
	if w := do(h, "POST", "/admin/2fa/setup", nil, cookie); w.Code != http.StatusSeeOther {
		t.Fatalf("setup: %d", w.Code)
	}
	a, err := s.queries.GetAdminByUsername(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.totpOpen(a.TotpSecretEnc)
	if err != nil {
		t.Fatal(err)
	}
	w := do(h, "POST", "/admin/2fa/confirm", url.Values{"code": {totpCode(key, time.Now().Unix()/30)}}, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d", w.Code)
	}
	codes := recoveryRe.FindAllString(w.Body.String(), -1)
	if len(codes) != 8 {
		t.Fatalf("want 8 recovery codes shown, got %d", len(codes))
	}
	return key, codes
}

// passwordStep logs in with the password only and returns the response.
func passwordStep(h http.Handler, user string) *httptest.ResponseRecorder {
	return do(h, "POST", "/login", url.Values{"username": {user}, "password": {"secret123"}}, nil)
}

// secondStep posts a code to the 2FA step of a pending login.
func secondStep(h http.Handler, cookie *http.Cookie, code string) *httptest.ResponseRecorder {
	return do(h, "POST", "/login/2fa", url.Values{"code": {code}}, cookie)
}

func TestTOTPEnrollThenLogin(t *testing.T) {
	s, _ := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	h := s.Handler()
	cookie := login(t, h, "alice")

	if w := do(h, "GET", "/admin/2fa", nil, cookie); w.Code != 200 || strings.Contains(w.Body.String(), "data:image/png") {
		t.Fatalf("off page: %d", w.Code)
	}
	do(h, "POST", "/admin/2fa/setup", nil, cookie)
	if w := do(h, "GET", "/admin/2fa", nil, cookie); !strings.Contains(w.Body.String(), "data:image/png;base64,") {
		t.Fatal("setup page has no QR code")
	}
	// a wrong code confirms nothing
	if w := do(h, "POST", "/admin/2fa/confirm", url.Values{"code": {"000000"}}, cookie); w.Code != http.StatusSeeOther {
		t.Fatalf("wrong confirm: %d", w.Code)
	}
	var on int
	s.conn.QueryRow("SELECT totp_enabled FROM admins WHERE username = 'alice'").Scan(&on)
	if on != 0 {
		t.Fatal("2FA enabled before a valid code")
	}

	key, _ := enrollTOTP(t, s, h, cookie, "alice")
	s.conn.QueryRow("SELECT totp_enabled FROM admins WHERE username = 'alice'").Scan(&on)
	if on != 1 {
		t.Fatal("2FA not enabled after confirm")
	}
	var logs int
	s.conn.QueryRow("SELECT count(*) FROM activity_logs WHERE action = 'users.2fa.enable'").Scan(&logs)
	if logs != 1 {
		t.Fatalf("activity entries: %d", logs)
	}

	// Password alone no longer opens a session: the pending marker sends the admin to the code form.
	w := passwordStep(h, "alice")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login/2fa" {
		t.Fatalf("password step: %d %q", w.Code, w.Header().Get("Location"))
	}
	pending := sessionCookie(w)
	if w := do(h, "GET", "/admin", nil, pending); w.Header().Get("Location") != "/login" {
		t.Fatalf("pending session reached admin: %q", w.Header().Get("Location"))
	}
	if w := secondStep(h, pending, "000000"); w.Code != 200 {
		t.Fatalf("wrong code: %d", w.Code)
	}
	next := time.Now().Unix()/30 + 1
	w = secondStep(h, pending, totpCode(key, next))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin" {
		t.Fatalf("right code: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := do(h, "GET", "/admin", nil, sessionCookie(w)); w.Code != 200 {
		t.Fatalf("logged in page: %d", w.Code)
	}
}

func TestTOTPReplayRejected(t *testing.T) {
	s, _ := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	h := s.Handler()
	key, _ := enrollTOTP(t, s, h, login(t, h, "alice"), "alice")

	code := totpCode(key, time.Now().Unix()/30+1)
	if w := secondStep(h, sessionCookie(passwordStep(h, "alice")), code); w.Code != http.StatusSeeOther {
		t.Fatalf("first use: %d", w.Code)
	}
	if w := secondStep(h, sessionCookie(passwordStep(h, "alice")), code); w.Code != 200 {
		t.Fatalf("replayed code accepted: %d", w.Code)
	}
}

func TestTOTPWrongCodeThrottled(t *testing.T) {
	s, _ := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	h := s.Handler()
	key, _ := enrollTOTP(t, s, h, login(t, h, "alice"), "alice")

	pending := sessionCookie(passwordStep(h, "alice"))
	for i := 0; i < maxFailedLogins; i++ {
		if w := secondStep(h, pending, "000000"); w.Code != 200 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	// the correct code is refused too: the account is locked for the window
	if w := secondStep(h, pending, totpCode(key, time.Now().Unix()/30+1)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("throttle: %d", w.Code)
	}
	// a correct password does not reset the count
	if w := passwordStep(h, "alice"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("password reset the throttle: %d", w.Code)
	}
}

func TestTOTPRecoveryCodeWorksOnce(t *testing.T) {
	s, _ := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	h := s.Handler()
	_, codes := enrollTOTP(t, s, h, login(t, h, "alice"), "alice")

	if w := secondStep(h, sessionCookie(passwordStep(h, "alice")), codes[0]); w.Code != http.StatusSeeOther {
		t.Fatalf("recovery code: %d", w.Code)
	}
	if w := secondStep(h, sessionCookie(passwordStep(h, "alice")), codes[0]); w.Code != 200 {
		t.Fatalf("recovery code reused: %d", w.Code)
	}
	// typed without the dash and in lower case, the next code still works
	typed := strings.ToLower(strings.ReplaceAll(codes[1], "-", ""))
	if w := secondStep(h, sessionCookie(passwordStep(h, "alice")), typed); w.Code != http.StatusSeeOther {
		t.Fatalf("typed recovery code: %d", w.Code)
	}
}

func TestTOTPDisableAndSuperAdminReset(t *testing.T) {
	s, _ := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	h := s.Handler()
	cookie := login(t, h, "alice")
	key, _ := enrollTOTP(t, s, h, cookie, "alice")

	// disable needs the password and a valid code
	base := time.Now().Unix() / 30
	if w := do(h, "POST", "/admin/2fa/disable", url.Values{"current": {"wrong"}, "code": {totpCode(key, base+1)}}, cookie); w.Code != http.StatusSeeOther {
		t.Fatalf("disable: %d", w.Code)
	}
	var on int
	s.conn.QueryRow("SELECT totp_enabled FROM admins WHERE username = 'alice'").Scan(&on)
	if on != 1 {
		t.Fatal("disabled with a wrong password")
	}
	do(h, "POST", "/admin/2fa/disable", url.Values{"current": {"secret123"}, "code": {totpCode(key, base+1)}}, cookie)
	s.conn.QueryRow("SELECT totp_enabled FROM admins WHERE username = 'alice'").Scan(&on)
	if on != 0 {
		t.Fatal("not disabled")
	}

	// rita turns 2FA on; only a SuperAdmin may reset it
	ritaCookie := login(t, h, "rita")
	enrollTOTP(t, s, h, ritaCookie, "rita")
	rita, err := s.queries.GetAdminByUsername(t.Context(), "rita")
	if err != nil {
		t.Fatal(err)
	}
	reset := "/admin/users/" + strconv.FormatInt(rita.ID, 10) + "/2fa/reset"
	if w := do(h, "POST", reset, nil, ritaCookie); w.Code != http.StatusForbidden {
		t.Fatalf("Report admin reset: %d", w.Code)
	}
	if w := do(h, "POST", reset, nil, cookie); w.Code != http.StatusSeeOther {
		t.Fatalf("SuperAdmin reset: %d", w.Code)
	}
	a, _ := s.queries.GetAdminByUsername(t.Context(), "rita")
	if a.TotpEnabled != 0 || a.TotpSecretEnc != "" {
		t.Fatal("reset did not clear 2FA")
	}
	if w := passwordStep(h, "rita"); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin" {
		t.Fatalf("rita after reset: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestLoginWithoutTOTPUnchanged(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	login(t, h, "rita")
	w := passwordStep(h, "rita")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin" {
		t.Fatalf("rita: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := do(h, "GET", "/login/2fa", nil, nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("2fa form without pending login: %d %q", w.Code, w.Header().Get("Location"))
	}
}
