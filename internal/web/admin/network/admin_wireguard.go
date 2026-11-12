package network

// WireGuard: the Settings tab (/admin/settings/wireguard) and the per-router peer actions (internal/wg).
// Every action here is SuperAdmin only: it hands out a private key and a RADIUS secret.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/settings"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/service/provision"
	"github.com/frand-kod/gobill/internal/wg"
)

// WGDefaults fills the tunnel settings and the switch with their defaults.
func WGDefaults(st map[string]string) map[string]string {
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

// WGImpact tells what saving v changes for the routers that already have a tunnel: n is how many, and
// rewrite means they must be provisioned again (port, endpoint or subnet changed). Switching off cuts
// them too, but leaves their keys valid.
func (s *Handlers) WGImpact(ctx context.Context, st, v map[string]string) (n int64, rewrite, off bool, err error) {
	old, nw := WGDefaults(st), WGDefaults(v)
	rewrite = old["wg_port"] != nw["wg_port"] || old["wg_endpoint"] != nw["wg_endpoint"] || old["wg_subnet"] != nw["wg_subnet"]
	off = old["wg_enabled"] == "yes" && nw["wg_enabled"] != "yes"
	n, err = s.Queries.CountWgPeers(ctx)
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

// WGStatusFields is the status card of the tab: the tunnel, the public address and UDP.
func (s *Handlers) WGStatusFields(ctx context.Context, st map[string]string) []field {
	d := WGDefaults(st)
	sec := s.T("Status")
	state := s.T("Off")
	switch {
	case wgOff() != "":
		state = s.T(wgOff())
	case s.WG != nil && s.WG.Err() != nil:
		state = s.T("Could not start") + ": " + s.WG.Err().Error()
	case s.WG != nil && s.WG.Running():
		state = fmt.Sprintf(s.T("Running, listening on UDP %s"), d["wg_port"])
	}
	n, _ := s.Queries.CountWgPeers(ctx)
	out := []field{
		{Name: "wg_state", Label: s.T("Tunnel"), Type: "pre", Section: sec, Value: state + "\n" + fmt.Sprintf(s.T("Routers with a tunnel: %d"), n)},
	}
	msg, ok := wgAddress(d["wg_endpoint"], wgLookup, localIPs())
	udp := fmt.Sprintf(s.T("Not confirmed yet. gobill cannot test UDP from inside: it is confirmed when the first router completes a handshake. Make sure UDP port %s is open on the server firewall and at the provider"), d["wg_port"])
	if s.WG != nil {
		for _, ps := range s.WG.Stats() {
			if !ps.Handshake.IsZero() {
				udp = fmt.Sprintf(s.T("Confirmed: a router completed a handshake on %s"), s.TS(ps.Handshake.Unix()))
				break
			}
		}
	}
	mark := "OK: "
	if !ok {
		mark = s.T("Warning") + ": "
	}
	return append(out,
		field{Name: "wg_addr", Label: s.T("Public address"), Type: "pre", Section: sec, Value: mark + s.T(msg)},
		field{Name: "wg_udp", Label: s.T("UDP reachability"), Type: "pre", Section: sec, Value: udp})
}

// WGWarning is the line the setup wizard shows when WireGuard is on but the server cannot be reached.
func (s *Handlers) WGWarning(ctx context.Context) string {
	st, err := s.LoadSettings(ctx)
	if err != nil || s.WG == nil || wgOff() != "" || st["wg_enabled"] != "yes" {
		return ""
	}
	msg, ok := wgAddress(st["wg_endpoint"], wgLookup, localIPs())
	if ok {
		return ""
	}
	return s.T("WireGuard: ") + s.T(msg)
}

// WGUsable loads the tunnel config, failing when WireGuard is not on for provisioning.
func (s *Handlers) WGUsable(ctx context.Context) (wg.Config, map[string]string, error) {
	if s.WG == nil || wgOff() != "" {
		return wg.Config{}, nil, errors.New("WireGuard is not available in this build or is switched off by GOBILL_WG")
	}
	st, err := s.LoadSettings(ctx)
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
func (s *Handlers) routerWGProvision(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	back := fmt.Sprint("/admin/routers/", id, "/edit")
	rt, err := s.Queries.GetRouter(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get router", err)
		return
	}
	cfg, _, err := s.WGUsable(r.Context())
	if err != nil {
		s.Sessions.Put(r.Context(), "error", s.T(err.Error()))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	res, err := s.Prov().Provision(r.Context(), rt, cfg)
	var ae *provision.AddressError
	if errors.As(err, &ae) {
		s.Sessions.Put(r.Context(), "error", s.T(ae.Error()))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	} else if err != nil {
		s.Fail(w, "provision wireguard peer", err)
		return
	}
	if res.Old != nil {
		if err := s.WG.RemovePeer(res.Old.PublicKey); err != nil {
			s.Fail(w, "wireguard remove old peer", err)
			return
		}
	}
	np, err := s.Queries.GetWgPeer(r.Context(), id)
	if err != nil {
		s.Fail(w, "get wireguard peer", err)
		return
	}
	if err := s.WG.AddPeer(np); err != nil {
		s.Fail(w, "wireguard add peer", err)
		return
	}
	s.Sessions.Put(r.Context(), "wg_once", fetchLine(core.BaseURL(r)+"/provision/"+res.Token))
	s.Done(w, r, back, "WireGuard peer is ready. Paste the line into the router", "router.wg.provision", rt.Name)
}

// wgRevoke drops the peer of a router at once, and the NAS row made for its tunnel address so the next
// router that gets the address does not inherit the secret. No peer = nothing to do.
func (s *Handlers) wgRevoke(ctx context.Context, id int64) error {
	p, err := s.Queries.GetWgPeer(ctx, id)
	if err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}
	if err := s.wgRelease(ctx, p); err != nil {
		return err
	}
	pending := settings.Get(ctx, s.Queries, provision.PendingKey(id))
	if pending != "" { // the script was never fetched: nothing was switched for it
		if err := s.Queries.UpsertSettingRaw(ctx, db.UpsertSettingRawParams{Key: provision.PendingKey(id)}); err != nil {
			return err
		}
	}
	rt, err := s.Queries.GetRouter(ctx, id)
	if err != nil {
		return s.Queries.DeleteWgPeer(ctx, id)
	}
	up := db.UpdateRouterParams{Name: rt.Name, Host: rt.Host, Port: rt.Port, Username: rt.Username, PasswordEnc: rt.PasswordEnc,
		Description: rt.Description, Enabled: rt.Enabled, Coordinates: rt.Coordinates, Coverage: rt.Coverage, CertPin: rt.CertPin, ID: id}
	var restoreTLS *int64
	if prev := settings.Get(ctx, s.Queries, provision.PrevKey(id)); prev != "" {
		var c provision.Creds // back to the credentials the router had before the tunnel
		if err := json.Unmarshal([]byte(prev), &c); err != nil {
			return err
		}
		up.Host, up.Port, up.Username, up.PasswordEnc = c.Host, c.Port, c.User, c.Pass
		restoreTLS = &c.TLS
		if err := s.Queries.UpsertSettingRaw(ctx, db.UpsertSettingRawParams{Key: provision.PrevKey(id)}); err != nil {
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
		return s.Queries.DeleteWgPeer(ctx, id)
	}
	if err := s.Queries.UpdateRouter(ctx, up); err != nil {
		return err
	}
	if restoreTLS != nil {
		if err := s.Queries.SetRouterTLS(ctx, db.SetRouterTLSParams{Tls: *restoreTLS, ID: id}); err != nil {
			return err
		}
	}
	return s.Queries.DeleteWgPeer(ctx, id)
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
func (s *Handlers) wgRelease(ctx context.Context, p db.WgPeer) error {
	if s.WG != nil {
		if err := s.WG.RemovePeer(p.PublicKey); err != nil {
			return err
		}
	}
	return provision.DropNAS(ctx, s.Queries, p.Ip)
}

func (s *Handlers) routerWGRevoke(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	if err := s.wgRevoke(r.Context(), id); err != nil {
		s.Fail(w, "revoke wireguard peer", err)
		return
	}
	s.Done(w, r, fmt.Sprint("/admin/routers/", id, "/edit"), "WireGuard peer revoked", "router.wg.revoke", fmt.Sprint(id))
}

// routerWGReset is POST /admin/routers/{id}/wg/reset: forget the handshake state of the peer.
func (s *Handlers) routerWGReset(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	back := fmt.Sprint("/admin/routers/", id, "/edit")
	p, err := s.Queries.GetWgPeer(r.Context(), id)
	if err == sql.ErrNoRows || s.WG == nil {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	} else if err != nil {
		s.Fail(w, "get wireguard peer", err)
		return
	}
	if err := s.WG.ResetPeer(p); err != nil {
		s.Fail(w, "reset wireguard peer", err)
		return
	}
	s.Done(w, r, back, "Peer session reset", "router.wg.reset", fmt.Sprint(id))
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
func (s *Handlers) wgHandshake(p db.WgPeer, port string) wgStage {
	var hs time.Time
	if s.WG != nil {
		hs = s.WG.Stats()[p.PublicKey].Handshake
	}
	switch {
	case hs.IsZero() && p.Endpoint != "":
		return wgStage{false, fmt.Sprintf(s.T("Handshake: none since gobill started. Last seen at %s"), p.Endpoint)}
	case hs.IsZero():
		return wgStage{false, fmt.Sprintf(s.T("Handshake: never. Check that UDP port %s reaches this server, and the router clock"), port)}
	case time.Since(hs) > 3*time.Minute:
		return wgStage{false, fmt.Sprintf(s.T("Handshake: last %s ago, the tunnel looks down"), ago(hs))}
	}
	return wgStage{true, fmt.Sprintf(s.T("Handshake: OK, %s ago"), ago(hs))}
}

func (s *Handlers) wgRadius(p db.WgPeer) wgStage {
	if s.WG == nil || s.WG.RadiusSeen(p.Ip).IsZero() {
		return wgStage{false, s.T("RADIUS: no packet from this router yet")}
	}
	return wgStage{true, fmt.Sprintf(s.T("RADIUS: last packet %s ago"), ago(s.WG.RadiusSeen(p.Ip)))}
}

// wgAPI and wgCoA call the router.
func (s *Handlers) wgAPI(ctx context.Context, rt db.Router, p db.WgPeer) wgStage {
	if rt.Host != p.Ip {
		return wgStage{false, fmt.Sprintf(s.T("The router Host is %s, not the tunnel address %s: the API does not use the tunnel"), rt.Host, p.Ip)}
	}
	if s.Billing == nil {
		return wgStage{false, s.T("API login failed") + ": " + s.T("Router connection is not configured")}
	}
	ident, err := s.Billing.Ping(ctx, rt)
	if err != nil {
		return wgStage{false, s.T("API login failed") + ": " + s.T(routerErr(err, msgRouterOther))}
	}
	return wgStage{true, s.T("API login OK") + " (" + ident + ")"}
}

func (s *Handlers) wgCoA(ctx context.Context, p db.WgPeer) wgStage {
	if err := radius.ProbeCoA(ctx, s.Queries, s.SecretKey, p.Ip); err != nil {
		return wgStage{false, s.T("CoA failed") + ": " + err.Error()}
	}
	return wgStage{true, s.T("CoA reply received")}
}

// routerWGCheck is POST /admin/routers/{id}/wg/check: all four stages, shown on the router page.
func (s *Handlers) routerWGCheck(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	back := fmt.Sprint("/admin/routers/", id, "/edit")
	rt, err := s.Queries.GetRouter(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.Queries.GetWgPeer(r.Context(), id)
	if err != nil || s.WG == nil {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	st, _ := s.LoadSettings(r.Context())
	var lines []string
	for _, x := range []wgStage{s.wgHandshake(p, WGDefaults(st)["wg_port"]), s.wgAPI(r.Context(), rt, p), s.wgRadius(p), s.wgCoA(r.Context(), p)} {
		lines = append(lines, x.String())
	}
	s.Sessions.Put(r.Context(), "wg_stages", strings.Join(lines, "\n"))
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
func (s *Handlers) wgToken(ctx context.Context, id int64, hs time.Time) string {
	k, err := s.Queries.GetWgToken(ctx, id)
	if err != nil {
		return ""
	}
	switch {
	case k.UsedAt != 0:
		out := fmt.Sprintf(s.T("Script fetched from %s at %s"), k.UsedIp, s.TS(k.UsedAt))
		if time.Since(time.Unix(k.UsedAt, 0)) > 3*time.Minute && hs.Unix() < k.UsedAt {
			out += "\n" + s.T("Warning: no handshake since the script was fetched. If you did not run it, provision again: this makes a new key and cancels the old one")
		}
		return out
	case time.Now().Unix() > k.ExpiresAt:
		return s.T("The provisioning line expired without being used. Provision again to get a new one")
	}
	return fmt.Sprintf(s.T("The provisioning line is valid until %s and has not been used"), s.TS(k.ExpiresAt))
}

// routerWGFields is the WireGuard card of the router page: the stages that can be read without calling
// the router, the last full check, the buttons, and, right after provisioning, the line to paste. port is the tunnel port.
func (s *Handlers) routerWGFields(ctx context.Context, rt db.Router, once, port string) []field {
	sec := s.T("WireGuard tunnel")
	var out []field
	if once != "" {
		out = append(out, field{Name: "wg_cmd", Label: s.T("Paste this one line into the router terminal (RouterOS 7). It works once and expires in 15 minutes"), Type: "pre", Section: sec, Value: once,
			Hint: s.T("The key and the RADIUS secret are not in the line: the router fetches them. Provision again to get a new line")})
	}
	if off := wgOff(); off != "" {
		return append(out, field{Name: "wg_status", Label: s.T("Status"), Type: "pre", Section: sec, Value: s.T(off)})
	}
	btn := func(name, label, path, confirm string) field {
		return field{Name: name, Type: "formbtn", Section: sec, Value: fmt.Sprintf("/admin/routers/%d/wg/%s", rt.ID, path), Btn: label, Confirm: confirm}
	}
	p, err := s.Queries.GetWgPeer(ctx, rt.ID)
	if err != nil {
		return append(out, field{Name: "wg_status", Label: s.T("Status"), Type: "pre", Section: sec, Value: s.T("Not provisioned")},
			btn("wg_provision", "Provision", "provision", ""))
	}
	lines := []string{fmt.Sprintf(s.T("Tunnel address: %s"), p.Ip)}
	if p.Stale != 0 {
		lines = append(lines, s.T("Needs provisioning again: the port, endpoint or subnet changed"))
	}
	hs := s.wgHandshake(p, port)
	lines = append(lines, hs.String(), s.wgRadius(p).String(), s.T("API login and CoA: use Check tunnel"))
	var hsTime time.Time
	if s.WG != nil {
		hsTime = s.WG.Stats()[p.PublicKey].Handshake
	}
	if tok := s.wgToken(ctx, rt.ID, hsTime); tok != "" {
		lines = append(lines, tok)
	}
	out = append(out, field{Name: "wg_status", Label: s.T("Status"), Type: "pre", Section: sec, Value: strings.Join(lines, "\n")})
	if stages := s.Sessions.PopString(ctx, "wg_stages"); stages != "" {
		out = append(out, field{Name: "wg_stages", Label: s.T("Check result"), Type: "pre", Section: sec, Value: stages})
	}
	return append(out,
		btn("wg_check", "Check tunnel", "check", ""),
		btn("wg_provision", "Provision again", "provision", "A new key is made and the old one stops working. The router must be given the new line"),
		btn("wg_reset", "Reset peer session", "reset", "Reset the peer session? The tunnel drops and reconnects."),
		btn("wg_revoke", "Revoke", "revoke", "Revoke the tunnel? The router is cut off from gobill at once"))
}
