package web

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

// restSetup: customer "bob"/"pw" with a Hotspot plan (5M down, 512K up) expiring in +1h (or -1h if expired).
func restSetup(t *testing.T, expired bool) (*Server, *db.Queries) {
	t.Helper()
	s, q := newTestApp(t)
	ctx := t.Context()
	s.SecretKey = make([]byte, 32)
	bw, _ := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 5, RateDownUnit: "Mbps", RateUp: 512, RateUpUnit: "Kbps"})
	rt, _ := q.CreateRouter(ctx, db.CreateRouterParams{Name: "r", Host: "h", Port: 8728, Username: "a", PasswordEnc: []byte("x"), Enabled: 1})
	plan, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: "p", Type: "Hotspot", Billing: "prepaid", Validity: 1, ValidityUnit: "Days",
		BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}, RouterID: sql.NullInt64{Int64: rt.ID, Valid: true}, Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := secret.Seal(s.SecretKey, []byte("pw"))
	c, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "bob", PasswordHash: "h", Fullname: "B", ServiceType: "Hotspot", SecretEnc: sealed, AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).Unix()
	if expired {
		exp = time.Now().Add(-time.Hour).Unix()
	}
	if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: plan.ID, RouterID: sql.NullInt64{Int64: rt.ID, Valid: true},
		Type: "Hotspot", StartedAt: exp - 7200, ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	return s, q
}

func post(h http.Handler, path string, form url.Values, remote string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	if remote != "" {
		r.RemoteAddr = remote
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRadiusRestAuthorize(t *testing.T) {
	s, _ := restSetup(t, false)
	h := s.Handler()
	w := post(h, "/radius.php?action=authorize", url.Values{"username": {"bob"}, "password": {"pw"}}, "")
	if w.Code != 200 {
		t.Fatalf("code %d %s", w.Code, w.Body)
	}
	var b map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if b["control:Auth-Type"] != "Accept" || b["reply:Mikrotik-Rate-Limit"] != "512K/5M" {
		t.Fatalf("body %v", b)
	}
	if v := b["reply"].(map[string]any)["Reply-Message"].(map[string]any)["value"]; v != "success" {
		t.Fatalf("reply obj %v", b["reply"])
	}
	// the other path and authenticate (204)
	if w := post(h, "/radius/rest?action=authenticate", url.Values{"username": {"bob"}, "password": {"pw"}}, ""); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("authenticate %d %q", w.Code, w.Body)
	}
	// wrong password: old reject
	w = post(h, "/radius.php?action=authorize", url.Values{"username": {"bob"}, "password": {"bad"}}, "")
	if w.Code != 401 || !strings.Contains(w.Body.String(), "Username or Password is wrong") {
		t.Fatalf("wrong pw %d %s", w.Code, w.Body)
	}
	// post-auth: empty 200
	if w := post(h, "/radius.php?action=post-auth", url.Values{"username": {"bob"}}, ""); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("post-auth %d %q", w.Code, w.Body)
	}
}

func TestRadiusRestCHAP(t *testing.T) {
	s, _ := restSetup(t, false)
	h := s.Handler()
	ch := []byte("0123456789abcdef")
	sum := func(pw string) string {
		m := md5.New()
		m.Write([]byte{7})
		m.Write([]byte(pw))
		m.Write(ch)
		return "0x07" + hex.EncodeToString(m.Sum(nil))
	}
	f := func(pw string) url.Values {
		return url.Values{"username": {"bob"}, "CHAPchallenge": {"0x" + hex.EncodeToString(ch)}, "CHAPassword": {sum(pw)}}
	}
	if w := post(h, "/radius.php?action=authorize", f("pw"), ""); w.Code != 200 {
		t.Fatalf("chap ok: %d %s", w.Code, w.Body)
	}
	if w := post(h, "/radius.php?action=authorize", f("nope"), ""); w.Code != 401 {
		t.Fatalf("chap bad: %d", w.Code)
	}
}

func TestRadiusRestExpired(t *testing.T) {
	s, _ := restSetup(t, true)
	w := post(s.Handler(), "/radius.php?action=authorize", url.Values{"username": {"bob"}, "password": {"pw"}}, "")
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"control:Auth-Type":"Reject"`) || !strings.Contains(w.Body.String(), "expired") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestRadiusRestAccountingOneRow(t *testing.T) {
	s, q := restSetup(t, false)
	h := s.Handler()
	acct := func(typ, in, out string) {
		w := post(h, "/radius.php?action=accounting", url.Values{"username": {"bob"}, "nasIpAddress": {"10.0.0.1"}, "acctSessionId": {"s1"},
			"macAddr": {"AA:BB"}, "acctStatusType": {typ}, "acctInputOctets": {in}, "acctOutputOctets": {out}, "acctSessionTime": {"5"},
			"framedIPAddress": {"10.1.1.5"}}, "")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", typ, w.Code, w.Body)
		}
	}
	acct("Start", "0", "0")
	acct("Interim-Update", "100", "200")
	ss, _ := q.ListOpenRadiusSessionsByUser(t.Context(), "bob")
	if len(ss) != 1 || ss[0].InputOctets != 100 || ss[0].OutputOctets != 200 || ss[0].FramedIp != "10.1.1.5" {
		t.Fatalf("open %+v", ss)
	}
	acct("Stop", "150", "250")
	if ss, _ := q.ListOpenRadiusSessionsByUser(t.Context(), "bob"); len(ss) != 0 {
		t.Fatalf("still open %+v", ss)
	}
	var n int
	s.conn.QueryRow(`SELECT COUNT(*) FROM radius_sessions`).Scan(&n)
	if n != 1 {
		t.Fatalf("rows %d", n)
	}
}

func TestRadiusRestAllowList(t *testing.T) {
	s, q := restSetup(t, false)
	h := s.Handler()
	q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "radius_rest_allow", Value: "10.0.0.0/24, 192.168.1.9, 127.0.0.1"})
	form := url.Values{"username": {"bob"}, "password": {"pw"}}
	for remote, want := range map[string]int{"10.0.0.7:5000": 200, "192.168.1.9:1": 200, "8.8.8.8:1": 403} {
		if w := post(h, "/radius.php?action=authorize", form, remote); w.Code != want {
			t.Errorf("%s: %d want %d", remote, w.Code, want)
		}
	}
	// X-Forwarded-For is only honoured with trust_proxy=yes and a loopback or trusted TCP peer.
	spoof := func(peer, xff string) int {
		r := httptest.NewRequest("POST", "/radius.php?action=authorize", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Forwarded-For", xff)
		r.RemoteAddr = peer
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := spoof("8.8.8.8:1", "10.0.0.7"); c != 403 {
		t.Fatalf("spoofed XFF accepted: %d", c)
	}
	q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "trust_proxy", Value: "yes"})
	s.ReloadSessionSettings(t.Context())
	if c := spoof("8.8.8.8:1", "10.0.0.7"); c != 403 {
		t.Fatalf("XFF from a non-proxy peer accepted with trust_proxy=yes: %d", c)
	}
	if c := spoof("8.8.8.8:1", "127.0.0.1"); c != 403 {
		t.Fatalf("spoofed XFF 127.0.0.1 from a non-loopback peer accepted: %d", c)
	}
	if c := spoof("127.0.0.1:1", "8.8.8.8"); c != 200 {
		t.Fatalf("loopback peer (FreeRADIUS via local proxy) refused: %d", c)
	}
}

func TestRadiusRestCSRFAndMaintenance(t *testing.T) {
	s, q := restSetup(t, false)
	h := s.Handler()
	q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "maintenance_mode", Value: "yes"})
	cross := func(path string) int {
		r := httptest.NewRequest("POST", path, strings.NewReader("username=bob&password=pw"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, p := range []string{"/radius.php?action=authorize", "/radius/rest?action=authorize"} {
		if c := cross(p); c != 200 {
			t.Errorf("%s: cross-origin %d (CSRF bypass/maintenance)", p, c)
		}
	}
	if c := cross("/portal/login"); c != 403 {
		t.Errorf("other path must keep CSRF protection, got %d", c)
	}
}

func TestRadiusRestEmptyAllowLoopbackOnly(t *testing.T) {
	m := map[string]string{}
	for remote, want := range map[string]bool{"127.0.0.1:1": true, "[::1]:1": true, "10.0.0.5:1": false} {
		if got := radiusRestAllowed(&http.Request{RemoteAddr: remote}, m); got != want {
			t.Errorf("%s: %v want %v", remote, got, want)
		}
	}
}

func TestRealIPMiddleware(t *testing.T) {
	s, q := restSetup(t, false)
	var got string
	h := s.realIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = clientIP(r) }))
	call := func(peer string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", "1.1.1.1, 9.9.9.9")
		h.ServeHTTP(nil, r)
		return got
	}
	reload := func(kv map[string]string) {
		for k, v := range kv {
			q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: k, Value: v})
		}
		s.ReloadSessionSettings(t.Context())
	}
	reload(map[string]string{"trust_proxy": "yes"})
	if g := call("10.0.0.2:99"); g != "10.0.0.2" {
		t.Fatalf("XFF from a non-proxy peer honoured: %s", g)
	}
	if g := call("127.0.0.1:99"); g != "9.9.9.9" {
		t.Fatalf("XFF from loopback ignored: %s", g)
	}
	reload(map[string]string{"trusted_proxies": "10.0.0.0/24, 192.168.5.5"})
	if g := call("10.0.0.2:99"); g != "9.9.9.9" {
		t.Fatalf("XFF from trusted CIDR ignored: %s", g)
	}
	if g := call("192.168.5.5:1"); g != "9.9.9.9" {
		t.Fatalf("XFF from trusted IP ignored: %s", g)
	}
	if g := call("172.16.0.1:1"); g != "172.16.0.1" {
		t.Fatalf("XFF from untrusted peer honoured: %s", g)
	}
	reload(map[string]string{"trust_proxy": "no"})
	if g := call("127.0.0.1:99"); g != "127.0.0.1" {
		t.Fatalf("trust_proxy=no still honoured XFF: %s", g)
	}
}
