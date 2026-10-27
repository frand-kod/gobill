package web

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/wg"
)

func wgSetup(t *testing.T) (*Server, http.Handler, *db.Queries, *http.Cookie) {
	t.Helper()
	s, q := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	s.lang.Store("english")
	s.WG = &wg.Manager{Q: q, Key: s.SecretKey}
	h := s.Handler()
	return s, h, q, login(t, h, "alice")
}

func wgRouter(t *testing.T, q *db.Queries, name string) db.Router {
	t.Helper()
	r, err := q.CreateRouter(t.Context(), db.CreateRouterParams{Name: name, Host: "192.0.2.9", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func wgSave(h http.Handler, c *http.Cookie, kv map[string]string) *httptest.ResponseRecorder {
	form := url.Values{}
	for k, v := range kv {
		form.Set(k, v)
	}
	return do(h, "POST", "/admin/settings/wireguard", form, c)
}

func wgPost(h http.Handler, c *http.Cookie, id int64, action string) *httptest.ResponseRecorder {
	return do(h, "POST", "/admin/routers/"+strconv.FormatInt(id, 10)+"/wg/"+action, url.Values{}, c)
}

func TestWGSettingsSave(t *testing.T) {
	if !wg.Built {
		t.Skip("nowg build")
	}
	s, h, _, c := wgSetup(t)
	for name, kv := range map[string]map[string]string{
		"port 0":             {"wg_enabled": "no", "wg_port": "0", "wg_subnet": "10.77.0.0/24", "wg_endpoint": ""},
		"port text":          {"wg_enabled": "no", "wg_port": "abc", "wg_subnet": "10.77.0.0/24", "wg_endpoint": ""},
		"public subnet":      {"wg_enabled": "no", "wg_port": "51820", "wg_subnet": "8.8.8.0/24", "wg_endpoint": ""},
		"too small subnet":   {"wg_enabled": "no", "wg_port": "51820", "wg_subnet": "10.77.0.0/31", "wg_endpoint": ""},
		"endpoint with port": {"wg_enabled": "no", "wg_port": "51820", "wg_subnet": "10.77.0.0/24", "wg_endpoint": "vpn.example.com:51820"},
	} {
		if w := wgSave(h, c, kv); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422", name, w.Code)
		}
	}
	w := wgSave(h, c, map[string]string{"wg_enabled": "yes", "wg_port": "38211", "wg_subnet": "10.77.0.0/24", "wg_endpoint": "vpn.example.com"})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	st, _ := s.loadSettings(t.Context())
	if st["wg_enabled"] != "yes" || st["wg_port"] != "38211" || st["wg_subnet"] != "10.77.0.0/24" || st["wg_endpoint"] != "vpn.example.com" {
		t.Fatalf("settings not stored: %v", st)
	}
	if _, ok := st["wg_confirm"]; ok {
		t.Fatal("the confirmation box was stored as a setting")
	}
	if st["wg_private"] == "" && wg.Built {
		t.Fatal("no server key made when WireGuard was switched on")
	}
	if wg.Built && !s.WG.Running() {
		t.Fatalf("tunnel not running after save: %v", s.WG.Err())
	}
	// rita is not a SuperAdmin: no tab
	if w := do(h, "GET", "/admin/settings/wireguard", nil, login(t, h, "rita")); w.Code != http.StatusForbidden {
		t.Errorf("Report role opened the tab: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/settings/wireguard", nil, c); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Tunnel subnet") {
		t.Errorf("tab: %d", w.Code)
	}
}

func TestWGProvisionImpactAndRevoke(t *testing.T) {
	if !wg.Built {
		t.Skip("nowg build")
	}
	s, h, q, c := wgSetup(t)
	wgSave(h, c, map[string]string{"wg_enabled": "yes", "wg_port": "38212", "wg_subnet": "10.77.0.0/24", "wg_endpoint": "vpn.example.com"})
	r1, r2 := wgRouter(t, q, "alpha"), wgRouter(t, q, "beta")

	if w := wgPost(h, login(t, h, "rita"), r1.ID, "provision"); w.Code != http.StatusForbidden {
		t.Fatalf("Report role provisioned: %d", w.Code)
	}
	if w := wgPost(h, c, r1.ID, "provision"); w.Code != http.StatusSeeOther {
		t.Fatalf("provision: %d", w.Code)
	}
	p1, err := q.GetWgPeer(t.Context(), r1.ID)
	if err != nil || p1.Ip != "10.77.0.2" {
		t.Fatalf("peer 1: %+v %v", p1, err)
	}
	page := do(h, "GET", "/admin/routers/"+strconv.FormatInt(r1.ID, 10)+"/edit", nil, c).Body.String()
	for _, want := range []string{"/interface wireguard add name=wg-gobill", "allowed-address=10.77.0.1/32", "endpoint-address=vpn.example.com endpoint-port=38212",
		"src-address=10.77.0.2", "persistent-keepalive=25s", "/system ntp client"} {
		if !strings.Contains(page, want) {
			t.Errorf("command block lacks %q", want)
		}
	}
	if strings.Contains(do(h, "GET", "/admin/routers/"+strconv.FormatInt(r1.ID, 10)+"/edit", nil, c).Body.String(), "/interface wireguard add") {
		t.Error("the private key block was shown a second time")
	}
	// the private key is stored sealed, not as the base64 text the block printed
	if strings.Contains(string(p1.PrivateEnc), "=") && len(p1.PrivateEnc) < 48 {
		t.Error("private key looks unsealed")
	}
	nas, _ := q.ListNAS(t.Context())
	if len(nas) != 1 || nas[0].Ip != "10.77.0.2" || nas[0].Name != "wg-alpha" {
		t.Fatalf("nas rows: %+v", nas)
	}

	// provisioning again: a new key, the same address, the old key is gone from the tunnel
	wgPost(h, c, r1.ID, "provision")
	p1b, _ := q.GetWgPeer(t.Context(), r1.ID)
	if p1b.PublicKey == p1.PublicKey || p1b.Ip != p1.Ip {
		t.Fatalf("re-provision kept the key or moved the address: %+v", p1b)
	}
	if _, ok := s.WG.Stats()[p1.PublicKey]; ok {
		t.Error("old key still on the tunnel")
	}
	if _, ok := s.WG.Stats()[p1b.PublicKey]; !ok {
		t.Error("new key not on the tunnel")
	}
	wgPost(h, c, r2.ID, "provision")
	if p2, _ := q.GetWgPeer(t.Context(), r2.ID); p2.Ip != "10.77.0.3" {
		t.Fatalf("second router got %q", p2.Ip)
	}

	// a port change shows how many routers it cuts, and saves nothing until it is confirmed
	change := map[string]string{"wg_enabled": "yes", "wg_port": "38213", "wg_subnet": "10.77.0.0/24", "wg_endpoint": "vpn.example.com"}
	w := wgSave(h, c, change)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "2 routers have a tunnel and will be cut") {
		t.Fatalf("unconfirmed change: %d", w.Code)
	}
	if st, _ := s.loadSettings(t.Context()); st["wg_port"] != "38212" {
		t.Fatalf("unconfirmed change was saved: %q", st["wg_port"])
	}
	change["wg_confirm"] = "1"
	if w := wgSave(h, c, change); w.Code != http.StatusSeeOther {
		t.Fatalf("confirmed change: %d", w.Code)
	}
	for _, id := range []int64{r1.ID, r2.ID} {
		if p, _ := q.GetWgPeer(t.Context(), id); p.Stale != 1 {
			t.Errorf("router %d not marked for provisioning again", id)
		}
	}
	if !strings.Contains(do(h, "GET", "/admin/routers/"+strconv.FormatInt(r1.ID, 10)+"/edit", nil, c).Body.String(), "Needs provisioning again") {
		t.Error("the router page does not say it needs provisioning again")
	}
	// a change that touches neither port, endpoint nor subnet needs no confirmation and marks nothing
	wgPost(h, c, r1.ID, "provision")
	if p, _ := q.GetWgPeer(t.Context(), r1.ID); p.Stale != 0 {
		t.Error("provisioning did not clear the mark")
	}
	same := map[string]string{"wg_enabled": "yes", "wg_port": "38213", "wg_subnet": "10.77.0.0/24", "wg_endpoint": "vpn.example.com"}
	if w := wgSave(h, c, same); w.Code != http.StatusSeeOther {
		t.Fatalf("unchanged save: %d", w.Code)
	}
	if p, _ := q.GetWgPeer(t.Context(), r1.ID); p.Stale != 0 {
		t.Error("an unchanged save marked a router")
	}
	// switching off cuts the routers too, so it asks as well
	off := map[string]string{"wg_enabled": "no", "wg_port": "38213", "wg_subnet": "10.77.0.0/24", "wg_endpoint": "vpn.example.com"}
	if w := wgSave(h, c, off); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("switch off without confirming: %d", w.Code)
	}

	// a new subnet: the address moves into it on the next provisioning
	sub := map[string]string{"wg_enabled": "yes", "wg_port": "38213", "wg_subnet": "10.88.0.0/24", "wg_endpoint": "vpn.example.com", "wg_confirm": "1"}
	wgSave(h, c, sub)
	wgPost(h, c, r1.ID, "provision")
	if p, _ := q.GetWgPeer(t.Context(), r1.ID); p.Ip != "10.88.0.2" {
		t.Fatalf("address after a subnet change: %q", p.Ip)
	}

	// revoke removes the peer, its NAS row and the key on the tunnel
	p1c, _ := q.GetWgPeer(t.Context(), r1.ID)
	wgPost(h, c, r1.ID, "revoke")
	if _, err := q.GetWgPeer(t.Context(), r1.ID); err == nil {
		t.Error("peer still stored after revoke")
	}
	if _, ok := s.WG.Stats()[p1c.PublicKey]; ok {
		t.Error("revoked key still on the tunnel")
	}
	for _, n := range func() []db.Na { x, _ := q.ListNAS(t.Context()); return x }() {
		if n.Ip == p1c.Ip {
			t.Error("NAS row of the revoked address kept")
		}
	}
	// deleting a router revokes it as well
	p2, _ := q.GetWgPeer(t.Context(), r2.ID)
	do(h, "POST", "/admin/routers/"+strconv.FormatInt(r2.ID, 10)+"/delete", url.Values{}, c)
	if _, ok := s.WG.Stats()[p2.PublicKey]; ok {
		t.Error("deleted router still on the tunnel")
	}
}

func TestWGKillSwitch(t *testing.T) {
	if !wg.Built {
		t.Skip("nowg build")
	}
	s, h, q, c := wgSetup(t)
	t.Setenv("GOBILL_WG", "off")
	wgSave(h, c, map[string]string{"wg_enabled": "yes", "wg_port": "38214", "wg_subnet": "10.77.0.0/24", "wg_endpoint": "vpn.example.com"})
	if st, _ := s.loadSettings(t.Context()); st["wg_enabled"] != "yes" {
		t.Fatal("the setting itself should still save")
	}
	if s.WG.Running() {
		t.Fatal("tunnel runs although GOBILL_WG=off")
	}
	if st, _ := s.loadSettings(t.Context()); st["wg_private"] != "" {
		t.Fatal("a key was made although GOBILL_WG=off")
	}
	r := wgRouter(t, q, "alpha")
	wgPost(h, c, r.ID, "provision")
	if _, err := q.GetWgPeer(t.Context(), r.ID); err == nil {
		t.Fatal("provisioned although GOBILL_WG=off")
	}
	if page := do(h, "GET", "/admin/settings/wireguard", nil, c).Body.String(); !strings.Contains(page, "GOBILL_WG=off") {
		t.Error("the tab does not say the kill-switch is on")
	}
}

func TestWGAddress(t *testing.T) {
	local := []net.IP{net.ParseIP("203.0.113.5")}
	look := func(m map[string]string) func(string) ([]net.IP, error) {
		return func(h string) ([]net.IP, error) {
			if ip, ok := m[h]; ok {
				return []net.IP{net.ParseIP(ip)}, nil
			}
			return nil, errors.New("no such host")
		}
	}
	l := look(map[string]string{"here.test": "203.0.113.5", "nat.test": "198.51.100.7", "lan.test": "192.168.1.5"})
	for _, c := range []struct {
		host string
		ok   bool
		sub  string
	}{
		{"", false, "No public endpoint"},
		{"gone.test", false, "does not resolve"},
		{"lan.test", false, "private address"},
		{"here.test", true, "of this server"},
		{"nat.test", true, "not one of this server"},
	} {
		msg, ok := wgAddress(c.host, l, local)
		if ok != c.ok || !strings.Contains(msg, c.sub) {
			t.Errorf("%q: ok=%v %q", c.host, ok, msg)
		}
	}
}
