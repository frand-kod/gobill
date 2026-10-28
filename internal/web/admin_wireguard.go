package web

// WireGuard: the Settings tab (/admin/settings/wireguard) and the per-router peer actions (internal/wg).
// Every action here is SuperAdmin only: it hands out a private key and a RADIUS secret.

import (
	"context"
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

// wgBlock holds what the RouterOS commands of one peer name.
type wgBlock struct {
	Private, ServerPub, Endpoint, Secret string
	Port                                 int
	Self, Peer                           netip.Addr
	Bits                                 int
}

// routerBlock is the RouterOS 7 commands for one router: the tunnel, NTP (a router whose clock restarts
// at its factory date is refused by WireGuard), RADIUS from the tunnel address, and the firewall holes
// for CoA and the API from gobill's tunnel address only.
func (b wgBlock) routerBlock() string {
	ep := b.Endpoint
	if ep == "" {
		ep = "<DOMAIN-OR-IP-GOBILL>"
	}
	var w strings.Builder
	fmt.Fprintf(&w, "/interface wireguard add name=wg-gobill private-key=%q mtu=1420\n", b.Private)
	fmt.Fprintf(&w, "/interface wireguard peers add interface=wg-gobill public-key=%q endpoint-address=%s endpoint-port=%d allowed-address=%s/32 persistent-keepalive=25s\n", b.ServerPub, ep, b.Port, b.Self)
	fmt.Fprintf(&w, "/ip address add address=%s/%d interface=wg-gobill\n", b.Peer, b.Bits)
	w.WriteString("/system ntp client set enabled=yes servers=pool.ntp.org\n")
	fmt.Fprintf(&w, "/radius add service=hotspot,ppp address=%s secret=%s authentication-port=1812 accounting-port=1813 timeout=1s src-address=%s\n", b.Self, b.Secret, b.Peer)
	w.WriteString("/radius incoming set accept=yes\n")
	fmt.Fprintf(&w, "/ip firewall filter add chain=input in-interface=wg-gobill src-address=%s protocol=udp dst-port=3799 action=accept place-before=0\n", b.Self)
	fmt.Fprintf(&w, "/ip firewall filter add chain=input in-interface=wg-gobill src-address=%s protocol=tcp dst-port=8728 action=accept place-before=0", b.Self)
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
	cfg, st, err := s.wgUsable(r.Context())
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
	serverPub, err := wg.PublicKey(cfg.Private)
	if err != nil {
		s.fail(w, "wireguard server key", err)
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
	secretText, err := s.wgNAS(r.Context(), q, rt, ip.String())
	if err != nil {
		s.fail(w, "wireguard nas", err)
		return
	}
	if err := q.UpsertWgPeer(r.Context(), db.UpsertWgPeerParams{RouterID: id, Ip: ip.String(), PublicKey: pub, PrivateEnc: sealed, ProvisionedAt: time.Now().Unix()}); err != nil {
		s.fail(w, "save wireguard peer", err)
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
	d := wgDefaults(st)
	s.sessions.Put(r.Context(), "wg_once", wgBlock{Private: priv, ServerPub: serverPub, Endpoint: d["wg_endpoint"], Secret: secretText,
		Port: cfg.Port, Self: cfg.Self(), Peer: ip, Bits: cfg.Subnet.Bits()}.routerBlock())
	s.done(w, r, back, "WireGuard peer is ready. Paste the commands into the router", "router.wg.provision", rt.Name)
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

// routerWGCheck is POST /admin/routers/{id}/wg/check: the stages that need a connection to the router.
func (s *Server) routerWGCheck(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	back := fmt.Sprint("/admin/routers/", id, "/edit")
	rt, err := s.queries.GetRouter(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.queries.GetWgPeer(r.Context(), id)
	if err != nil || s.WG == nil || s.Billing == nil {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	var parts []string
	bad := false
	if st := s.WG.Stats()[p.PublicKey]; st.Handshake.IsZero() {
		parts = append(parts, t("Handshake: none yet, the tunnel is not up. The API and CoA tests below cannot work until it is"))
		bad = true
	}
	if rt.Host != p.Ip {
		parts = append(parts, fmt.Sprintf(t("The router Host is %s, not the tunnel address %s: the API does not use the tunnel"), rt.Host, p.Ip))
		bad = true
	}
	if ident, err := s.Billing.Ping(r.Context(), rt); err != nil {
		parts = append(parts, t("API login failed")+": "+t(routerErr(err, msgRouterOther)))
		bad = true
	} else {
		parts = append(parts, t("API login OK")+" ("+ident+")")
	}
	if err := radius.ProbeCoA(r.Context(), s.queries, s.SecretKey, p.Ip); err != nil {
		parts = append(parts, t("CoA failed")+": "+err.Error())
		bad = true
	} else {
		parts = append(parts, t("CoA reply received"))
	}
	if bad {
		s.sessions.Put(r.Context(), "error", strings.Join(parts, ". "))
	} else {
		s.sessions.Put(r.Context(), "flash", strings.Join(parts, ". "))
	}
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

// routerWGFields is the WireGuard card of the router page: the stages that can be read without calling
// the router, the buttons, and, right after provisioning, the commands to paste. port is the tunnel port.
func (s *Server) routerWGFields(ctx context.Context, rt db.Router, once, port string) []field {
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	sec := t("WireGuard tunnel")
	var out []field
	if once != "" {
		out = append(out, field{Name: "wg_cmd", Label: t("Paste into the router terminal (RouterOS 7). The private key and the RADIUS secret are shown only now"), Type: "pre", Section: sec, Value: once,
			Hint: t("Then set the Host of this router to the tunnel address. Provision again to get a new block")})
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
	var hs time.Time
	if s.WG != nil {
		hs = s.WG.Stats()[p.PublicKey].Handshake
	}
	switch {
	case hs.IsZero() && p.Endpoint != "":
		lines = append(lines, fmt.Sprintf(t("Handshake: none since gobill started. Last seen at %s"), p.Endpoint))
	case hs.IsZero():
		lines = append(lines, fmt.Sprintf(t("Handshake: never. Check that UDP port %s reaches this server, and the router clock"), port))
	case time.Since(hs) > 3*time.Minute:
		lines = append(lines, fmt.Sprintf(t("Handshake: last %s ago, the tunnel looks down"), ago(hs)))
	default:
		lines = append(lines, fmt.Sprintf(t("Handshake: OK, %s ago"), ago(hs)))
	}
	if s.WG != nil {
		if seen := s.WG.RadiusSeen(p.Ip); seen.IsZero() {
			lines = append(lines, t("RADIUS: no packet from this router yet"))
		} else {
			lines = append(lines, fmt.Sprintf(t("RADIUS: last packet %s ago"), ago(seen)))
		}
	}
	lines = append(lines, t("API login and CoA: use Check tunnel"))
	return append(out,
		field{Name: "wg_status", Label: t("Status"), Type: "pre", Section: sec, Value: strings.Join(lines, "\n")},
		btn("wg_check", "Check tunnel", "check", ""),
		btn("wg_provision", "Provision again", "provision", "A new key is made and the old one stops working. The router must be given the new commands"),
		btn("wg_reset", "Reset peer session", "reset", ""),
		btn("wg_revoke", "Revoke", "revoke", "Revoke the tunnel? The router is cut off from gobill at once"))
}
