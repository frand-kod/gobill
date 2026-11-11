package web

// WireGuard: the Settings tab (/admin/settings/wireguard) and the per-router peer actions (internal/wg).
// Every action here is SuperAdmin only: it hands out a private key and a RADIUS secret.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/settings"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/service/provision"
	"github.com/frand-kod/gobill/internal/wg"
)

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
	d := wgDefaults(st)
	sec := s.t("Status")
	state := s.t("Off")
	switch {
	case wgOff() != "":
		state = s.t(wgOff())
	case s.WG != nil && s.WG.Err() != nil:
		state = s.t("Could not start") + ": " + s.WG.Err().Error()
	case s.WG != nil && s.WG.Running():
		state = fmt.Sprintf(s.t("Running, listening on UDP %s"), d["wg_port"])
	}
	n, _ := s.queries.CountWgPeers(ctx)
	out := []field{
		{Name: "wg_state", Label: s.t("Tunnel"), Type: "pre", Section: sec, Value: state + "\n" + fmt.Sprintf(s.t("Routers with a tunnel: %d"), n)},
	}
	msg, ok := wgAddress(d["wg_endpoint"], wgLookup, localIPs())
	udp := fmt.Sprintf(s.t("Not confirmed yet. gobill cannot test UDP from inside: it is confirmed when the first router completes a handshake. Make sure UDP port %s is open on the server firewall and at the provider"), d["wg_port"])
	if s.WG != nil {
		for _, ps := range s.WG.Stats() {
			if !ps.Handshake.IsZero() {
				udp = fmt.Sprintf(s.t("Confirmed: a router completed a handshake on %s"), s.ts(ps.Handshake.Unix()))
				break
			}
		}
	}
	mark := "OK: "
	if !ok {
		mark = s.t("Warning") + ": "
	}
	return append(out,
		field{Name: "wg_addr", Label: s.t("Public address"), Type: "pre", Section: sec, Value: mark + s.t(msg)},
		field{Name: "wg_udp", Label: s.t("UDP reachability"), Type: "pre", Section: sec, Value: udp})
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
	return s.t("WireGuard: ") + s.t(msg)
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
		s.sessions.Put(r.Context(), "error", s.t(err.Error()))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	res, err := s.prov().Provision(r.Context(), rt, cfg)
	var ae *provision.AddressError
	if errors.As(err, &ae) {
		s.sessions.Put(r.Context(), "error", s.t(ae.Error()))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	} else if err != nil {
		s.fail(w, "provision wireguard peer", err)
		return
	}
	if res.Old != nil {
		if err := s.WG.RemovePeer(res.Old.PublicKey); err != nil {
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
	s.sessions.Put(r.Context(), "wg_once", fetchLine(baseURL(r)+"/provision/"+res.Token))
	s.done(w, r, back, "WireGuard peer is ready. Paste the line into the router", "router.wg.provision", rt.Name)
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
	pending := settings.Get(ctx, s.queries, provision.PendingKey(id))
	if pending != "" { // the script was never fetched: nothing was switched for it
		if err := s.queries.UpsertSettingRaw(ctx, db.UpsertSettingRawParams{Key: provision.PendingKey(id)}); err != nil {
			return err
		}
	}
	rt, err := s.queries.GetRouter(ctx, id)
	if err != nil {
		return s.queries.DeleteWgPeer(ctx, id)
	}
	up := db.UpdateRouterParams{Name: rt.Name, Host: rt.Host, Port: rt.Port, Username: rt.Username, PasswordEnc: rt.PasswordEnc,
		Description: rt.Description, Enabled: rt.Enabled, Coordinates: rt.Coordinates, Coverage: rt.Coverage, CertPin: rt.CertPin, ID: id}
	var restoreTLS *int64
	if prev := settings.Get(ctx, s.queries, provision.PrevKey(id)); prev != "" {
		var c provision.Creds // back to the credentials the router had before the tunnel
		if err := json.Unmarshal([]byte(prev), &c); err != nil {
			return err
		}
		up.Host, up.Port, up.Username, up.PasswordEnc = c.Host, c.Port, c.User, c.Pass
		restoreTLS = &c.TLS
		if err := s.queries.UpsertSettingRaw(ctx, db.UpsertSettingRawParams{Key: provision.PrevKey(id)}); err != nil {
			return err
		}
	} else if pending == "" {
		// Switched before wg_prev_<id> existed: the tunnel address is cut and the old password is gone, so
		// the stored one is replaced to not match the router.
		enc, err := secret.Seal(s.SecretKey, []byte(secret.RandomPassword(24)))
		if err != nil {
			return err
		}
		up.PasswordEnc = enc
	} else {
		return s.queries.DeleteWgPeer(ctx, id)
	}
	if err := s.queries.UpdateRouter(ctx, up); err != nil {
		return err
	}
	if restoreTLS != nil {
		if err := s.queries.SetRouterTLS(ctx, db.SetRouterTLSParams{Tls: *restoreTLS, ID: id}); err != nil {
			return err
		}
	}
	return s.queries.DeleteWgPeer(ctx, id)
}

// fetchLine is the RouterOS command that downloads and imports the provision script. Over https the
// router also verifies the server certificate, so the script cannot be swapped on the way.
func fetchLine(u string) string {
	opts := ""
	if strings.HasPrefix(u, "https://") {
		opts = " mode=https check-certificate=yes"
	}
	return fmt.Sprintf("/tool fetch url=%q%s dst-path=gobill.rsc; /import gobill.rsc", u, opts)
}

// wgRelease cuts the peer from the tunnel and deletes the NAS row of its address.
func (s *Server) wgRelease(ctx context.Context, p db.WgPeer) error {
	if s.WG != nil {
		if err := s.WG.RemovePeer(p.PublicKey); err != nil {
			return err
		}
	}
	return provision.DropNAS(ctx, s.queries, p.Ip)
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
	var hs time.Time
	if s.WG != nil {
		hs = s.WG.Stats()[p.PublicKey].Handshake
	}
	switch {
	case hs.IsZero() && p.Endpoint != "":
		return wgStage{false, fmt.Sprintf(s.t("Handshake: none since gobill started. Last seen at %s"), p.Endpoint)}
	case hs.IsZero():
		return wgStage{false, fmt.Sprintf(s.t("Handshake: never. Check that UDP port %s reaches this server, and the router clock"), port)}
	case time.Since(hs) > 3*time.Minute:
		return wgStage{false, fmt.Sprintf(s.t("Handshake: last %s ago, the tunnel looks down"), ago(hs))}
	}
	return wgStage{true, fmt.Sprintf(s.t("Handshake: OK, %s ago"), ago(hs))}
}

func (s *Server) wgRadius(p db.WgPeer) wgStage {
	if s.WG == nil || s.WG.RadiusSeen(p.Ip).IsZero() {
		return wgStage{false, s.t("RADIUS: no packet from this router yet")}
	}
	return wgStage{true, fmt.Sprintf(s.t("RADIUS: last packet %s ago"), ago(s.WG.RadiusSeen(p.Ip)))}
}

// wgAPI and wgCoA call the router.
func (s *Server) wgAPI(ctx context.Context, rt db.Router, p db.WgPeer) wgStage {
	if rt.Host != p.Ip {
		return wgStage{false, fmt.Sprintf(s.t("The router Host is %s, not the tunnel address %s: the API does not use the tunnel"), rt.Host, p.Ip)}
	}
	if s.Billing == nil {
		return wgStage{false, s.t("API login failed") + ": " + s.t("Router connection is not configured")}
	}
	ident, err := s.Billing.Ping(ctx, rt)
	if err != nil {
		return wgStage{false, s.t("API login failed") + ": " + s.t(routerErr(err, msgRouterOther))}
	}
	return wgStage{true, s.t("API login OK") + " (" + ident + ")"}
}

func (s *Server) wgCoA(ctx context.Context, p db.WgPeer) wgStage {
	if err := radius.ProbeCoA(ctx, s.queries, s.SecretKey, p.Ip); err != nil {
		return wgStage{false, s.t("CoA failed") + ": " + err.Error()}
	}
	return wgStage{true, s.t("CoA reply received")}
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
	k, err := s.queries.GetWgToken(ctx, id)
	if err != nil {
		return ""
	}
	switch {
	case k.UsedAt != 0:
		out := fmt.Sprintf(s.t("Script fetched from %s at %s"), k.UsedIp, s.ts(k.UsedAt))
		if time.Since(time.Unix(k.UsedAt, 0)) > 3*time.Minute && hs.Unix() < k.UsedAt {
			out += "\n" + s.t("Warning: no handshake since the script was fetched. If you did not run it, provision again: this makes a new key and cancels the old one")
		}
		return out
	case time.Now().Unix() > k.ExpiresAt:
		return s.t("The provisioning line expired without being used. Provision again to get a new one")
	}
	return fmt.Sprintf(s.t("The provisioning line is valid until %s and has not been used"), s.ts(k.ExpiresAt))
}

// routerWGFields is the WireGuard card of the router page: the stages that can be read without calling
// the router, the last full check, the buttons, and, right after provisioning, the line to paste. port is the tunnel port.
func (s *Server) routerWGFields(ctx context.Context, rt db.Router, once, port string) []field {
	sec := s.t("WireGuard tunnel")
	var out []field
	if once != "" {
		out = append(out, field{Name: "wg_cmd", Label: s.t("Paste this one line into the router terminal (RouterOS 7). It works once and expires in 15 minutes"), Type: "pre", Section: sec, Value: once,
			Hint: s.t("The key and the RADIUS secret are not in the line: the router fetches them. Provision again to get a new line")})
	}
	if off := wgOff(); off != "" {
		return append(out, field{Name: "wg_status", Label: s.t("Status"), Type: "pre", Section: sec, Value: s.t(off)})
	}
	btn := func(name, label, path, confirm string) field {
		return field{Name: name, Type: "formbtn", Section: sec, Value: fmt.Sprintf("/admin/routers/%d/wg/%s", rt.ID, path), Btn: label, Confirm: confirm}
	}
	p, err := s.queries.GetWgPeer(ctx, rt.ID)
	if err != nil {
		return append(out, field{Name: "wg_status", Label: s.t("Status"), Type: "pre", Section: sec, Value: s.t("Not provisioned")},
			btn("wg_provision", "Provision", "provision", ""))
	}
	lines := []string{fmt.Sprintf(s.t("Tunnel address: %s"), p.Ip)}
	if p.Stale != 0 {
		lines = append(lines, s.t("Needs provisioning again: the port, endpoint or subnet changed"))
	}
	hs := s.wgHandshake(p, port)
	lines = append(lines, hs.String(), s.wgRadius(p).String(), s.t("API login and CoA: use Check tunnel"))
	var hsTime time.Time
	if s.WG != nil {
		hsTime = s.WG.Stats()[p.PublicKey].Handshake
	}
	if tok := s.wgToken(ctx, rt.ID, hsTime); tok != "" {
		lines = append(lines, tok)
	}
	out = append(out, field{Name: "wg_status", Label: s.t("Status"), Type: "pre", Section: sec, Value: strings.Join(lines, "\n")})
	if stages := s.sessions.PopString(ctx, "wg_stages"); stages != "" {
		out = append(out, field{Name: "wg_stages", Label: s.t("Check result"), Type: "pre", Section: sec, Value: stages})
	}
	return append(out,
		btn("wg_check", "Check tunnel", "check", ""),
		btn("wg_provision", "Provision again", "provision", "A new key is made and the old one stops working. The router must be given the new line"),
		btn("wg_reset", "Reset peer session", "reset", "Reset the peer session? The tunnel drops and reconnects."),
		btn("wg_revoke", "Revoke", "revoke", "Revoke the tunnel? The router is cut off from gobill at once"))
}
