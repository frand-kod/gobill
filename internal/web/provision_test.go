package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/wg"
)

var provisionPathRe = regexp.MustCompile(`/provision/\d+-[0-9a-f]{64}`)

// provisionPath is the /provision/<token> path in the router page right after provisioning.
func provisionPath(t *testing.T, page string) string {
	t.Helper()
	m := provisionPathRe.FindString(page)
	if m == "" {
		t.Fatal("no provisioning line on the page")
	}
	return m
}

// fetchScript is what the router does: a GET from addr, without any session.
func fetchScript(t *testing.T, h http.Handler, path, addr string, want int) string {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = addr
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("GET %s from %s: %d, want %d: %s", path, addr, w.Code, want, w.Body)
	}
	return w.Body.String()
}

var provPort atomic.Int32 // each test its own UDP port: tunnels of earlier tests stay up

func init() { provPort.Store(38300) }

// provisioned makes a router with the tunnel on, provisions it and returns its /provision path.
func provisioned(t *testing.T) (*Server, http.Handler, *http.Cookie, int64, string) {
	t.Helper()
	if !wg.Built {
		t.Skip("nowg build")
	}
	s, h, q, c := wgSetup(t)
	wgSave(h, c, map[string]string{"wg_enabled": "yes", "wg_port": strconv.Itoa(int(provPort.Add(1))), "wg_subnet": "10.77.0.0/24", "wg_endpoint": "vpn.example.com"})
	rt := wgRouter(t, q, "alpha")
	if w := wgPost(h, c, rt.ID, "provision"); w.Code != http.StatusSeeOther {
		t.Fatalf("provision: %d", w.Code)
	}
	page := do(h, "GET", "/admin/routers/"+strconv.FormatInt(rt.ID, 10)+"/edit", nil, c).Body.String()
	return s, h, c, rt.ID, provisionPath(t, page)
}

func TestProvisionTokenSingleUse(t *testing.T) {
	s, h, c, id, path := provisioned(t)
	// no session needed, and a wrong token or id gets nothing
	bad := path[:len(path)-1] + "0"
	if path[len(path)-1] == '0' {
		bad = path[:len(path)-1] + "1"
	}
	for _, p := range []string{bad, "/provision/999-" + strings.Repeat("a", 64), "/provision/x", "/provision/" + strconv.FormatInt(id, 10) + "-"} {
		fetchScript(t, h, p, "198.51.100.7:4000", http.StatusGone)
	}
	if k, _ := s.queries.GetWgToken(t.Context(), id); k.UsedAt != 0 {
		t.Fatal("a wrong token used the real one up")
	}
	script := fetchScript(t, h, path, "198.51.100.7:4000", http.StatusOK)
	rt, _ := s.queries.GetRouter(t.Context(), id)
	pw, _ := secret.Open(s.SecretKey, rt.PasswordEnc)
	if rt.Host != "10.77.0.2" || rt.Username != "gobill" || rt.Port != 8728 || !strings.Contains(script, `password="`+string(pw)+`"`) {
		t.Errorf("API user not set up for the tunnel: %s %s %d", rt.Host, rt.Username, rt.Port)
	}
	// the token is gone: same address, another address, and a later time all fail
	fetchScript(t, h, path, "198.51.100.7:4001", http.StatusGone)
	fetchScript(t, h, path, "203.0.113.9:4000", http.StatusGone)
	k, _ := s.queries.GetWgToken(t.Context(), id)
	if k.UsedIp != "198.51.100.7" || k.UsedAt == 0 {
		t.Fatalf("the first use was not kept: %+v", k)
	}
	if k.TokenHash == path || strings.Contains(path, k.TokenHash) {
		t.Error("the token is stored as is")
	}
	page := do(h, "GET", "/admin/routers/"+strconv.FormatInt(id, 10)+"/edit", nil, c).Body.String()
	if !strings.Contains(page, "Script fetched from 198.51.100.7") {
		t.Error("the page does not say where the script was fetched from")
	}
}

func TestProvisionTokenExpired(t *testing.T) {
	s, h, _, id, path := provisioned(t)
	k, _ := s.queries.GetWgToken(t.Context(), id)
	if left := time.Until(time.Unix(k.ExpiresAt, 0)); left > provisionTTL || left < provisionTTL-time.Minute {
		t.Fatalf("token lives %v, want 15 minutes", left)
	}
	if err := s.queries.SetWgToken(t.Context(), db.SetWgTokenParams{RouterID: id, TokenHash: tokenHash(path[strings.LastIndex(path, "/")+1:]), ExpiresAt: time.Now().Add(-time.Second).Unix()}); err != nil {
		t.Fatal(err)
	}
	fetchScript(t, h, path, "198.51.100.7:4000", http.StatusGone)
	if k, _ := s.queries.GetWgToken(t.Context(), id); k.UsedAt != 0 {
		t.Error("an expired token was marked used")
	}
}

func TestProvisionAgainCancelsOldToken(t *testing.T) {
	s, h, c, id, old := provisioned(t)
	oldPeer, _ := s.queries.GetWgPeer(t.Context(), id)
	wgPost(h, c, id, "provision")
	fresh := provisionPath(t, do(h, "GET", "/admin/routers/"+strconv.FormatInt(id, 10)+"/edit", nil, c).Body.String())
	if fresh == old {
		t.Fatal("same token twice")
	}
	fetchScript(t, h, old, "198.51.100.7:4000", http.StatusGone)
	script := fetchScript(t, h, fresh, "198.51.100.7:4000", http.StatusOK)
	newPeer, _ := s.queries.GetWgPeer(t.Context(), id)
	if newPeer.PublicKey == oldPeer.PublicKey {
		t.Fatal("provisioning again kept the key")
	}
	if !strings.Contains(script, "/interface wireguard add name=wg-gobill") {
		t.Error("no script")
	}
}

func TestProvisionRemoveRevokes(t *testing.T) {
	s, h, c, id, path := provisioned(t)
	before, _ := s.queries.GetRouter(t.Context(), id)
	if w := wgPost(h, c, id, "revoke"); w.Code != http.StatusSeeOther {
		t.Fatalf("revoke: %d", w.Code)
	}
	after, _ := s.queries.GetRouter(t.Context(), id)
	pw1, _ := secret.Open(s.SecretKey, before.PasswordEnc)
	pw2, _ := secret.Open(s.SecretKey, after.PasswordEnc)
	if len(pw2) == 0 || string(pw1) == string(pw2) {
		t.Error("the API password gobill holds was not replaced")
	}
	fetchScript(t, h, path, "198.51.100.7:4000", http.StatusGone) // no peer: no script
	// deleting the router takes the token with it
	wgPost(h, c, id, "provision")
	p2 := provisionPath(t, do(h, "GET", "/admin/routers/"+strconv.FormatInt(id, 10)+"/edit", nil, c).Body.String())
	do(h, "POST", "/admin/routers/"+strconv.FormatInt(id, 10)+"/delete", nil, c)
	if _, err := s.queries.GetWgToken(t.Context(), id); err == nil {
		t.Error("token kept after the router was deleted")
	}
	fetchScript(t, h, p2, "198.51.100.7:4000", http.StatusGone)
}

// The script is the same for the same router and removes what it adds before adding it, so a second run
// ends in the same state; RouterOS 6 stops before anything changes.
func TestProvisionScriptShape(t *testing.T) {
	b := wgBlock{Private: "PRIV", ServerPub: "PUB", Endpoint: "vpn.example.com", Secret: "SEC", APIPass: "PW", Port: 51820,
		Self: netip.MustParseAddr("10.99.0.1"), Peer: netip.MustParseAddr("10.99.0.2"), Bits: 24}
	sc := b.script()
	if sc != b.script() {
		t.Fatal("script changes between calls")
	}
	idx := func(sub string) int {
		i := strings.Index(sc, sub)
		if i < 0 {
			t.Fatalf("script lacks %q", sub)
		}
		return i
	}
	if v, first := idx("RouterOS 7 is required"), idx("/user remove"); v > first || !strings.Contains(sc[:v], ":error") || !strings.Contains(sc[v:], "L2TP/IPsec or SSTP") {
		t.Error("the version check must stop the script before any change")
	}
	for _, pair := range [][2]string{
		{"/interface wireguard remove [find name=wg-gobill]", "/interface wireguard add name=wg-gobill"},
		{"/radius remove [find comment=gobill]", "/radius add "},
		{"/user remove [find name=gobill]", "/user add name=gobill"},
		{"/user group remove [find name=gobill-api]", "/user group add name=gobill-api"},
		{"/ip firewall filter remove [find comment=gobill]", "/ip firewall filter add "},
	} {
		if idx(pair[0]) > idx(pair[1]) {
			t.Errorf("%q must come before %q", pair[0], pair[1])
		}
	}
	for _, want := range []string{"place-before=[:pick [/ip firewall filter find] 0]", "/ip service set api address=10.99.0.1/32", "group=gobill-api", "address=10.99.0.1/32",
		"/file remove [find name=gobill.rsc]"} {
		if !strings.Contains(sc, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Count(sc, "place-before=") != 2 || strings.Contains(sc, "place-before=0") {
		t.Error("both firewall rules go first, by id, never by index 0")
	}
	// An empty filter table has no first rule: each add with place-before is the else of an emptiness check, a plain add is the then.
	for _, ln := range strings.Split(sc, "\n") {
		if strings.Contains(ln, "place-before=") && (!strings.HasPrefix(ln, `:if ([:len [/ip firewall filter find]] = 0) do={ /ip firewall filter add `) || !strings.Contains(ln, "} else={ /ip firewall filter add ")) {
			t.Errorf("unguarded place-before: %s", ln)
		}
	}
}

func TestWGCheckStages(t *testing.T) {
	_, h, c, id, _ := provisioned(t)
	if w := wgPost(h, c, id, "check"); w.Code != http.StatusSeeOther {
		t.Fatalf("check: %d", w.Code)
	}
	page := do(h, "GET", "/admin/routers/"+strconv.FormatInt(id, 10)+"/edit", nil, c).Body.String()
	for _, want := range []string{"Handshake: never", "API login failed: Router connection is not configured", "RADIUS: no packet from this router yet", "CoA failed", "Check result"} {
		if !strings.Contains(page, want) {
			t.Errorf("stage result lacks %q", want)
		}
	}
	if strings.Count(page, "✗") < 4 {
		t.Error("each failed stage should be marked on its own")
	}
}
