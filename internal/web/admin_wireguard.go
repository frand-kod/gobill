package web

// WireGuard: the Settings tab (/admin/settings/wireguard) and the per-router peer actions (internal/wg).
// Every action here is SuperAdmin only: it hands out a private key and a RADIUS secret.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/wg"
)

// wgEndpointRe is a host name or an IPv4 address, without a port.
var wgEndpointRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// wgDefaults fills the tunnel settings and the switch with their defaults.
func wgDefaults(st map[string]string) map[string]string {
	out := map[string]string{"wg_enabled": "no", "wg_port": strconv.Itoa(wg.DefaultPort), "wg_subnet": wg.DefaultSubnet, "wg_endpoint": ""}
	for k := range out {
		if st[k] != "" {
			out[k] = st[k]
		}
	}
	return out
}

// wgOff says why the tunnel cannot run in this build or environment, "" when it can.
func wgOff() string {
	switch {
	case !wg.Built:
		return "This build has no WireGuard (built with the nowg tag)"
	case wg.Disabled():
		return "WireGuard is switched off by GOBILL_WG=off, which overrides this setting"
	}
	return ""
}

// wgErrors checks the posted WireGuard settings.
func wgErrors(v, e map[string]string) {
	if x, ok := v["wg_port"]; ok && !inRange(x, 1, 65535) {
		e["wg_port"] = "Enter a port between 1 and 65535"
	}
	if x, ok := v["wg_subnet"]; ok {
		if _, err := wg.ParseSubnet(x); err != nil {
			e["wg_subnet"] = "Use a private IPv4 range from /16 to /29, e.g. 10.99.0.0/24"
		}
	}
	if x := v["wg_endpoint"]; x != "" && !wgEndpointRe.MatchString(x) {
		e["wg_endpoint"] = "Enter a domain or an IP address without a port, e.g. vpn.example.com"
	}
}

// wgImpact tells what saving v changes for the routers that already have a tunnel: n is how many, and
// rewrite means they must be provisioned again (port, endpoint or subnet changed). Switching off cuts
// them too, but leaves their keys valid.
func (s *Server) wgImpact(ctx context.Context, st, v map[string]string) (n int64, rewrite, off bool, err error) {
	old, nw := wgDefaults(st), wgDefaults(v)
	rewrite = old["wg_port"] != nw["wg_port"] || old["wg_endpoint"] != nw["wg_endpoint"] || old["wg_subnet"] != nw["wg_subnet"]
	off = old["wg_enabled"] == "yes" && nw["wg_enabled"] != "yes"
	n, err = s.queries.CountWgPeers(ctx)
	return n, rewrite, off, err
}

// wgAddress judges the public endpoint. ok false = a warning the SuperAdmin should read. local are the
// addresses of this host's interfaces, lookup resolves a name.
func wgAddress(endpoint string, lookup func(string) ([]net.IP, error), local []net.IP) (msg string, ok bool) {
	if endpoint == "" {
		return "No public endpoint is set. Routers need a domain or public IP that reaches this server on UDP", false
	}
	ips, err := lookup(endpoint)
	if err != nil || len(ips) == 0 {
		return "The endpoint does not resolve to an address", false
	}
	public := false
	for _, ip := range ips {
		if !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified() {
			public = true
		}
	}
	if !public {
		return "The endpoint resolves to a private address, routers on the internet cannot reach it", false
	}
	for _, ip := range ips {
		for _, l := range local {
			if ip.Equal(l) {
				return "The endpoint is a public address of this server", true
			}
		}
	}
	return "The endpoint is a public address, but not one of this server: it must forward UDP to it. A Cloudflare Tunnel or a proxied Cloudflare record does not carry UDP, use a DNS-only record. Without a public IP at all (CGNAT) the server cannot be used", true
}

func localIPs() []net.IP {
	addrs, _ := net.InterfaceAddrs()
	var out []net.IP
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			out = append(out, ipn.IP)
		}
	}
	return out
}

// wgLookup resolves with a short timeout, so a dead resolver cannot stall a page.
func wgLookup(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	var out []net.IP
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, err
}

// wgStatusFields is the status card of the tab: the tunnel, the public address and UDP.
func (s *Server) wgStatusFields(ctx context.Context, st map[string]string) []field {
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	d := wgDefaults(st)
	sec := t("Status")
	state := t("Off")
	switch {
	case wgOff() != "":
		state = t(wgOff())
	case s.WG != nil && s.WG.Err() != nil:
		state = t("Could not start") + ": " + s.WG.Err().Error()
	case s.WG != nil && s.WG.Running():
		state = fmt.Sprintf(t("Running, listening on UDP %s"), d["wg_port"])
	}
	n, _ := s.queries.CountWgPeers(ctx)
	out := []field{
		{Name: "wg_state", Label: t("Tunnel"), Type: "pre", Section: sec, Value: state + "\n" + fmt.Sprintf(t("Routers with a tunnel: %d"), n)},
	}
	msg, ok := wgAddress(d["wg_endpoint"], wgLookup, localIPs())
	udp := fmt.Sprintf(t("Not confirmed yet. gobill cannot test UDP from inside: it is confirmed when the first router completes a handshake. Make sure UDP port %s is open on the server firewall and at the provider"), d["wg_port"])
	if s.WG != nil {
		for _, ps := range s.WG.Stats() {
			if !ps.Handshake.IsZero() {
				udp = fmt.Sprintf(t("Confirmed: a router completed a handshake on %s"), s.ts(ps.Handshake.Unix()))
				break
			}
		}
	}
	mark := "OK: "
	if !ok {
		mark = t("Warning") + ": "
	}
	return append(out,
		field{Name: "wg_addr", Label: t("Public address"), Type: "pre", Section: sec, Value: mark + t(msg)},
		field{Name: "wg_udp", Label: t("UDP reachability"), Type: "pre", Section: sec, Value: udp})
}

// wgWarning is the line the setup wizard shows when WireGuard is on but the server cannot be reached.
func (s *Server) wgWarning(ctx context.Context) string {
	st, err := s.loadSettings(ctx)
	if err != nil || s.WG == nil || wgOff() != "" || st["wg_enabled"] != "yes" {
		return ""
	}
	msg, ok := wgAddress(st["wg_endpoint"], wgLookup, localIPs())
	if ok {
		return ""
	}
	return s.catalog.T(s.language(), "WireGuard: ") + s.catalog.T(s.language(), msg)
}

// wgBlock holds what the RouterOS script of one peer names.
type wgBlock struct {
	Private, ServerPub, Endpoint, Secret, APIPass string
	Port                                          int
	Self, Peer                                    netip.Addr
	Bits                                          int
}

// Names the script owns on the router: it removes them before adding them again, so running it twice
// leaves one copy.
const (
	provIface = "wg-gobill"
	provUser  = "gobill"
	provGroup = "gobill-api"
	provTag   = "gobill"
)

// script is the RouterOS 7 script of one router, served once through /provision/<token>. It stops on
// RouterOS 6, removes what an earlier run made, then adds the tunnel, NTP (a router whose clock restarts
// at its factory date is refused by WireGuard), RADIUS from the tunnel address, the firewall holes for
// CoA and the API from gobill's tunnel address only, and the API user in a group of its own. The text
// depends only on the router and its stored keys.
func (b wgBlock) script() string {
	ep := b.Endpoint
	if ep == "" {
		ep = "<DOMAIN-OR-IP-GOBILL>"
	}
	var w strings.Builder
	w.WriteString("# gobill router provisioning. Safe to run again.\n")
	w.WriteString(":local v [/system resource get version]\n")
	w.WriteString(":if ([:tonum [:pick $v 0 [:find $v \".\"]]] < 7) do={ :error \"gobill: RouterOS 7 is required, this router runs $v. RouterOS 6 has no WireGuard: use L2TP/IPsec or SSTP, see the gobill docs\" }\n")
	fmt.Fprintf(&w, "/user remove [find name=%s]\n", provUser)
	fmt.Fprintf(&w, "/user group remove [find name=%s]\n", provGroup)
	fmt.Fprintf(&w, "/radius remove [find comment=%s]\n", provTag)
	fmt.Fprintf(&w, "/radius remove [find address=%s]\n", b.Self)
	fmt.Fprintf(&w, "/ip firewall filter remove [find comment=%s]\n", provTag)
	fmt.Fprintf(&w, "/ip address remove [find interface=%s]\n", provIface)
	fmt.Fprintf(&w, "/interface wireguard peers remove [find interface=%s]\n", provIface)
	fmt.Fprintf(&w, "/interface wireguard remove [find name=%s]\n", provIface)
	fmt.Fprintf(&w, "/interface wireguard add name=%s private-key=%q mtu=1420\n", provIface, b.Private)
	fmt.Fprintf(&w, "/interface wireguard peers add interface=%s public-key=%q endpoint-address=%s endpoint-port=%d allowed-address=%s/32 persistent-keepalive=25s\n", provIface, b.ServerPub, ep, b.Port, b.Self)
	fmt.Fprintf(&w, "/ip address add address=%s/%d interface=%s\n", b.Peer, b.Bits, provIface)
	w.WriteString("/system ntp client set enabled=yes servers=pool.ntp.org\n")
	fmt.Fprintf(&w, "/radius add service=hotspot,ppp address=%s secret=%q authentication-port=1812 accounting-port=1813 timeout=1s src-address=%s comment=%s\n", b.Self, b.Secret, b.Peer, provTag)
	w.WriteString("/radius incoming set accept=yes\n")
	fmt.Fprintf(&w, "/ip firewall filter add chain=input in-interface=%s src-address=%s protocol=udp dst-port=3799 action=accept place-before=0 comment=%s\n", provIface, b.Self, provTag)
	fmt.Fprintf(&w, "/ip firewall filter add chain=input in-interface=%s src-address=%s protocol=tcp dst-port=8728 action=accept place-before=0 comment=%s\n", provIface, b.Self, provTag)
	fmt.Fprintf(&w, "/user group add name=%s policy=api,read,write,test\n", provGroup)
	fmt.Fprintf(&w, "/user add name=%s group=%s password=%q address=%s/32\n", provUser, provGroup, b.APIPass, b.Self)
	fmt.Fprintf(&w, "/ip service set api address=%s/32 disabled=no\n", b.Self)
	w.WriteString("/file remove [find name=gobill.rsc]\n")
	return w.String()
}

// wgUsable loads the tunnel config, failing when WireGuard is not on for provisioning.
func (s *Server) wgUsable(ctx context.Context) (wg.Config, map[string]string, error) {
	if s.WG == nil || wgOff() != "" {
		return wg.Config{}, nil, errors.New("WireGuard is not available in this build or is switched off by GOBILL_WG")
	}
	st, err := s.loadSettings(ctx)
	if err != nil {
		return wg.Config{}, nil, err
	}
	if st["wg_enabled"] != "yes" {
		return wg.Config{}, nil, errors.New("WireGuard is off. Turn it on in Settings > WireGuard first")
	}
	cfg, err := wg.LoadConfig(st)
	if err != nil {
		return cfg, st, err
	}
	if cfg.Private == "" {
		return cfg, st, errors.New("The tunnel is not up yet. Save the WireGuard settings first")
	}
	return cfg, st, nil
}

// routerWGProvision is POST /admin/routers/{id}/wg/provision, for the first provisioning and again:
// it always makes a new keypair and the old key stops working at once. The commands are shown once.
func (s *Server) routerWGProvision(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	back := fmt.Sprint("/admin/routers/", id, "/edit")
	rt, err := s.queries.GetRouter(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get router", err)
		return
	}
	cfg, _, err := s.wgUsable(r.Context())
	if err != nil {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), err.Error()))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	priv, pub := wg.GenKey()
	sealed, err := secret.Seal(s.SecretKey, []byte(priv))
	if err != nil {
		s.fail(w, "seal wireguard key", err)
		return
	}
	tx, err := s.conn.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, "begin", err)
		return
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	peers, err := q.ListWgPeers(r.Context())
	if err != nil {
		s.fail(w, "list wireguard peers", err)
		return
	}
	var old *db.WgPeer
	var used []string
	for i, p := range peers {
		if p.RouterID == id {
			old = &peers[i]
		} else {
			used = append(used, p.Ip)
		}
	}
	var ip netip.Addr
	if old != nil { // keep the address when it still fits the subnet
		if a, err := netip.ParseAddr(old.Ip); err == nil && cfg.Subnet.Contains(a) && a != cfg.Self() {
			ip = a
		}
	}
	if !ip.IsValid() {
		if ip, err = wg.AllocIP(cfg.Subnet, used); err != nil {
			s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), err.Error()))
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
	}
	if old != nil && old.Ip != ip.String() { // the old address and its secret are not kept
		if err := s.wgDropNAS(r.Context(), q, old.Ip); err != nil {
			s.fail(w, "wireguard nas", err)
			return
		}
	}
	if _, err := s.wgNAS(r.Context(), q, rt, ip.String()); err != nil {
		s.fail(w, "wireguard nas", err)
		return
	}
	if err := q.UpsertWgPeer(r.Context(), db.UpsertWgPeerParams{RouterID: id, Ip: ip.String(), PublicKey: pub, PrivateEnc: sealed, ProvisionedAt: time.Now().Unix()}); err != nil {
		s.fail(w, "save wireguard peer", err)
		return
	}
	// the router is reached through the tunnel as the API user the script creates, with a new password
	apiEnc, err := secret.Seal(s.SecretKey, []byte(secret.RandomPassword(24)))
	if err != nil {
		s.fail(w, "seal api password", err)
		return
	}
	if err := q.UpdateRouter(r.Context(), db.UpdateRouterParams{Name: rt.Name, Host: ip.String(), Port: 8728, Username: provUser, PasswordEnc: apiEnc,
		Description: rt.Description, Enabled: rt.Enabled, Coordinates: rt.Coordinates, Coverage: rt.Coverage, CertPin: rt.CertPin, ID: id}); err != nil {
		s.fail(w, "save router api user", err)
		return
	}
	rnd := make([]byte, 32)
	rand.Read(rnd) // never returns an error in Go 1.24+
	token := fmt.Sprintf("%d-%x", id, rnd)
	if err := q.SetWgToken(r.Context(), db.SetWgTokenParams{RouterID: id, TokenHash: tokenHash(token), ExpiresAt: time.Now().Add(provisionTTL).Unix()}); err != nil {
		s.fail(w, "save provision token", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, "commit", err)
		return
	}
	if old != nil {
		if err := s.WG.RemovePeer(old.PublicKey); err != nil {
			s.fail(w, "wireguard remove old peer", err)
			return
		}
	}
	np, err := s.queries.GetWgPeer(r.Context(), id)
	if err != nil {
		s.fail(w, "get wireguard peer", err)
		return
	}
	if err := s.WG.AddPeer(np); err != nil {
		s.fail(w, "wireguard add peer", err)
		return
	}
	s.sessions.Put(r.Context(), "wg_once", fmt.Sprintf("/tool fetch url=%q dst-path=gobill.rsc; /import gobill.rsc", baseURL(r)+"/provision/"+token))
	s.done(w, r, back, "WireGuard peer is ready. Paste the line into the router", "router.wg.provision", rt.Name)
}

// wgNAS makes sure a NAS row covers the tunnel address of a router and returns its RADIUS secret.
// An existing row for that address keeps its secret; a new one gets its own.
func (s *Server) wgNAS(ctx context.Context, q *db.Queries, rt db.Router, ip string) (string, error) {
	rows, err := q.ListNAS(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range rows {
		if n.Ip == ip {
			b, err := secret.Open(s.SecretKey, n.SecretEnc)
			return string(b), err
		}
	}
	sec := secret.RandomPassword(24)
	enc, err := secret.Seal(s.SecretKey, []byte(sec))
	if err != nil {
		return "", err
	}
	name := "wg-" + rt.Name
	for _, n := range rows {
		if n.Name == name {
			name = fmt.Sprintf("wg-%s-%d", rt.Name, rt.ID)
		}
	}
	_, err = q.CreateNAS(ctx, db.CreateNASParams{Name: name, Ip: ip, SecretEnc: enc, Description: "WireGuard tunnel of router " + rt.Name})
	return sec, err
}

// wgRevoke drops the peer of a router at once, and the NAS row made for its tunnel address so the next
// router that gets the address does not inherit the secret. No peer = nothing to do.
func (s *Server) wgRevoke(ctx context.Context, id int64) error {
	p, err := s.queries.GetWgPeer(ctx, id)
	if err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}
	if err := s.wgRelease(ctx, p); err != nil {
		return err
	}
	// The API user on the router is only reachable from the tunnel address, which is now cut. The stored
	// password is replaced as well, so what gobill holds no longer matches the router.
	if rt, err := s.queries.GetRouter(ctx, id); err == nil {
		enc, err := secret.Seal(s.SecretKey, []byte(secret.RandomPassword(24)))
		if err != nil {
			return err
		}
		if err := s.queries.UpdateRouter(ctx, db.UpdateRouterParams{Name: rt.Name, Host: rt.Host, Port: rt.Port, Username: rt.Username, PasswordEnc: enc,
			Description: rt.Description, Enabled: rt.Enabled, Coordinates: rt.Coordinates, Coverage: rt.Coverage, CertPin: rt.CertPin, ID: id}); err != nil {
			return err
		}
	}
	return s.queries.DeleteWgPeer(ctx, id)
}

// wgRelease cuts the peer from the tunnel and deletes the NAS row of its address.
func (s *Server) wgRelease(ctx context.Context, p db.WgPeer) error {
	if s.WG != nil {
		if err := s.WG.RemovePeer(p.PublicKey); err != nil {
			return err
		}
	}
	return s.wgDropNAS(ctx, s.queries, p.Ip)
}

// wgDropNAS deletes the NAS row of a tunnel address.
func (s *Server) wgDropNAS(ctx context.Context, q *db.Queries, ip string) error {
	rows, err := q.ListNAS(ctx)
	if err != nil {
		return err
	}
	for _, n := range rows {
		if n.Ip == ip {
			if err := q.DeleteNAS(ctx, n.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) routerWGRevoke(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.wgRevoke(r.Context(), id); err != nil {
		s.fail(w, "revoke wireguard peer", err)
		return
	}
	s.done(w, r, fmt.Sprint("/admin/routers/", id, "/edit"), "WireGuard peer revoked", "router.wg.revoke", fmt.Sprint(id))
}

// routerWGReset is POST /admin/routers/{id}/wg/reset: forget the handshake state of the peer.
func (s *Server) routerWGReset(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	back := fmt.Sprint("/admin/routers/", id, "/edit")
	p, err := s.queries.GetWgPeer(r.Context(), id)
	if err == sql.ErrNoRows || s.WG == nil {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	} else if err != nil {
		s.fail(w, "get wireguard peer", err)
		return
	}
	if err := s.WG.ResetPeer(p); err != nil {
		s.fail(w, "reset wireguard peer", err)
		return
	}
	s.done(w, r, back, "Peer session reset", "router.wg.reset", fmt.Sprint(id))
}

// wgStage is one step of getting a router online. Each has its own message, so a failed step says what
// failed: a handshake that never happened is not the same as an API login that was refused.
type wgStage struct {
	OK  bool
	Msg string
}

func (st wgStage) String() string {
	if st.OK {
		return "✓ " + st.Msg
	}
	return "✗ " + st.Msg
}

// wgHandshake and wgRadius are the stages that can be read without calling the router.
func (s *Server) wgHandshake(p db.WgPeer, port string) wgStage {
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	var hs time.Time
	if s.WG != nil {
		hs = s.WG.Stats()[p.PublicKey].Handshake
	}
	switch {
	case hs.IsZero() && p.Endpoint != "":
		return wgStage{false, fmt.Sprintf(t("Handshake: none since gobill started. Last seen at %s"), p.Endpoint)}
	case hs.IsZero():
		return wgStage{false, fmt.Sprintf(t("Handshake: never. Check that UDP port %s reaches this server, and the router clock"), port)}
	case time.Since(hs) > 3*time.Minute:
		return wgStage{false, fmt.Sprintf(t("Handshake: last %s ago, the tunnel looks down"), ago(hs))}
	}
	return wgStage{true, fmt.Sprintf(t("Handshake: OK, %s ago"), ago(hs))}
}

func (s *Server) wgRadius(p db.WgPeer) wgStage {
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	if s.WG == nil || s.WG.RadiusSeen(p.Ip).IsZero() {
		return wgStage{false, t("RADIUS: no packet from this router yet")}
	}
	return wgStage{true, fmt.Sprintf(t("RADIUS: last packet %s ago"), ago(s.WG.RadiusSeen(p.Ip)))}
}

// wgAPI and wgCoA call the router.
func (s *Server) wgAPI(ctx context.Context, rt db.Router, p db.WgPeer) wgStage {
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	if rt.Host != p.Ip {
		return wgStage{false, fmt.Sprintf(t("The router Host is %s, not the tunnel address %s: the API does not use the tunnel"), rt.Host, p.Ip)}
	}
	if s.Billing == nil {
		return wgStage{false, t("API login failed") + ": " + t("Router connection is not configured")}
	}
	ident, err := s.Billing.Ping(ctx, rt)
	if err != nil {
		return wgStage{false, t("API login failed") + ": " + t(routerErr(err, msgRouterOther))}
	}
	return wgStage{true, t("API login OK") + " (" + ident + ")"}
}

func (s *Server) wgCoA(ctx context.Context, p db.WgPeer) wgStage {
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	if err := radius.ProbeCoA(ctx, s.queries, s.SecretKey, p.Ip); err != nil {
		return wgStage{false, t("CoA failed") + ": " + err.Error()}
	}
	return wgStage{true, t("CoA reply received")}
}

// routerWGCheck is POST /admin/routers/{id}/wg/check: all four stages, shown on the router page.
func (s *Server) routerWGCheck(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	back := fmt.Sprint("/admin/routers/", id, "/edit")
	rt, err := s.queries.GetRouter(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.queries.GetWgPeer(r.Context(), id)
	if err != nil || s.WG == nil {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	st, _ := s.loadSettings(r.Context())
	var lines []string
	for _, x := range []wgStage{s.wgHandshake(p, wgDefaults(st)["wg_port"]), s.wgAPI(r.Context(), rt, p), s.wgRadius(p), s.wgCoA(r.Context(), p)} {
		lines = append(lines, x.String())
	}
	s.sessions.Put(r.Context(), "wg_stages", strings.Join(lines, "\n"))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// ago renders how long ago t was.
func ago(t time.Time) string {
	d := time.Since(t).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h", int(d.Hours()))
}

// wgToken says what happened to the provisioning link of a router: waiting, used (from where, when) or expired.
func (s *Server) wgToken(ctx context.Context, id int64, hs time.Time) string {
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	k, err := s.queries.GetWgToken(ctx, id)
	if err != nil {
		return ""
	}
	switch {
	case k.UsedAt != 0:
		out := fmt.Sprintf(t("Script fetched from %s at %s"), k.UsedIp, s.ts(k.UsedAt))
		if time.Since(time.Unix(k.UsedAt, 0)) > 3*time.Minute && hs.Unix() < k.UsedAt {
			out += "\n" + t("Warning: no handshake since the script was fetched. If you did not run it, provision again: this makes a new key and cancels the old one")
		}
		return out
	case time.Now().Unix() > k.ExpiresAt:
		return t("The provisioning line expired without being used. Provision again to get a new one")
	}
	return fmt.Sprintf(t("The provisioning line is valid until %s and has not been used"), s.ts(k.ExpiresAt))
}

// routerWGFields is the WireGuard card of the router page: the stages that can be read without calling
// the router, the last full check, the buttons, and, right after provisioning, the line to paste. port is the tunnel port.
func (s *Server) routerWGFields(ctx context.Context, rt db.Router, once, port string) []field {
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	sec := t("WireGuard tunnel")
	var out []field
	if once != "" {
		out = append(out, field{Name: "wg_cmd", Label: t("Paste this one line into the router terminal (RouterOS 7). It works once and expires in 15 minutes"), Type: "pre", Section: sec, Value: once,
			Hint: t("The key and the RADIUS secret are not in the line: the router fetches them. Provision again to get a new line")})
	}
	if off := wgOff(); off != "" {
		return append(out, field{Name: "wg_status", Label: t("Status"), Type: "pre", Section: sec, Value: t(off)})
	}
	btn := func(name, label, path, confirm string) field {
		return field{Name: name, Type: "formbtn", Section: sec, Value: fmt.Sprintf("/admin/routers/%d/wg/%s", rt.ID, path), Btn: label, Confirm: confirm}
	}
	p, err := s.queries.GetWgPeer(ctx, rt.ID)
	if err != nil {
		return append(out, field{Name: "wg_status", Label: t("Status"), Type: "pre", Section: sec, Value: t("Not provisioned")},
			btn("wg_provision", "Provision", "provision", ""))
	}
	lines := []string{fmt.Sprintf(t("Tunnel address: %s"), p.Ip)}
	if p.Stale != 0 {
		lines = append(lines, t("Needs provisioning again: the port, endpoint or subnet changed"))
	}
	hs := s.wgHandshake(p, port)
	lines = append(lines, hs.String(), s.wgRadius(p).String(), t("API login and CoA: use Check tunnel"))
	var hsTime time.Time
	if s.WG != nil {
		hsTime = s.WG.Stats()[p.PublicKey].Handshake
	}
	if tok := s.wgToken(ctx, rt.ID, hsTime); tok != "" {
		lines = append(lines, tok)
	}
	out = append(out, field{Name: "wg_status", Label: t("Status"), Type: "pre", Section: sec, Value: strings.Join(lines, "\n")})
	if stages := s.sessions.PopString(ctx, "wg_stages"); stages != "" {
		out = append(out, field{Name: "wg_stages", Label: t("Check result"), Type: "pre", Section: sec, Value: stages})
	}
	return append(out,
		btn("wg_check", "Check tunnel", "check", ""),
		btn("wg_provision", "Provision again", "provision", "A new key is made and the old one stops working. The router must be given the new line"),
		btn("wg_reset", "Reset peer session", "reset", ""),
		btn("wg_revoke", "Revoke", "revoke", "Revoke the tunnel? The router is cut off from gobill at once"))
}
