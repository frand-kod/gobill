package web

// GET /provision/{token}: the RouterOS script a router fetches with the one-line command. The router is not
// a logged-in admin; the token is the credential. It is "<router id>-<64 hex>", valid provisionTTL, used
// once, and only its sha256 is stored. The script carries the private key and the secrets, so a refusal
// never says why (the log does).

import (
	"crypto/subtle"
	"database/sql"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/service/provision"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/wg"
)

func (s *Server) provisionScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	refuse := func(why string, id int64) {
		slog.Warn("provision link refused", "why", why, "router", id, "ip", core.ClientIP(r))
		http.Error(w, "This provisioning link is expired or already used. Provision the router again in gobill.", http.StatusGone)
	}
	token := r.PathValue("token")
	idText, _, _ := strings.Cut(token, "-")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		refuse("malformed", 0)
		return
	}
	k, err := s.Queries.GetWgToken(ctx, id)
	if err != nil {
		refuse("unknown", id)
		return
	}
	if subtle.ConstantTimeCompare([]byte(provision.TokenHash(token)), []byte(k.TokenHash)) != 1 {
		refuse("wrong token", id)
		return
	}
	if k.UsedAt != 0 { // a second use from another address is the case to notice
		if k.UsedIp != core.ClientIP(r) {
			slog.Warn("provision link used again from another address", "router", id, "first", k.UsedIp, "now", core.ClientIP(r))
		}
		refuse("used", id)
		return
	}
	if time.Now().Unix() >= k.ExpiresAt {
		refuse("expired", id)
		return
	}
	rt, err := s.Queries.GetRouter(ctx, id)
	if err != nil {
		refuse("no router", id)
		return
	}
	p, err := s.Queries.GetWgPeer(ctx, id)
	if err == sql.ErrNoRows {
		refuse("no peer", id)
		return
	} else if err != nil {
		s.Fail(w, "get wireguard peer", err)
		return
	}
	cfg, st, err := s.wgUsable(ctx)
	if err != nil {
		http.Error(w, "gobill WireGuard is not available", http.StatusServiceUnavailable)
		return
	}
	priv, err := secret.Open(s.SecretKey, p.PrivateEnc)
	if err != nil {
		s.Fail(w, "open wireguard key", err)
		return
	}
	serverPub, err := wg.PublicKey(cfg.Private)
	if err != nil {
		s.Fail(w, "wireguard server key", err)
		return
	}
	radiusSecret, err := s.prov().NAS(ctx, s.Queries, rt, p.Ip)
	if err != nil {
		s.Fail(w, "wireguard nas", err)
		return
	}
	// a pending password is switched in below, once the token is claimed; none = switched already
	pendingEnc, pending := s.prov().Pending(ctx, id)
	apiEnc := rt.PasswordEnc
	if pending {
		apiEnc = pendingEnc
	}
	apiPass, err := secret.Open(s.SecretKey, apiEnc)
	if err != nil {
		s.Fail(w, "open router api password", err)
		return
	}
	script, err := provision.Block{Private: string(priv), ServerPub: serverPub, Endpoint: wgDefaults(st)["wg_endpoint"], Secret: radiusSecret, APIPass: string(apiPass),
		Port: cfg.Port, Self: cfg.Self(), Peer: netip.MustParseAddr(p.Ip), Bits: cfg.Subnet.Bits()}.Script()
	if err != nil {
		s.Fail(w, "build provision script", err)
		return
	}
	// the claim is atomic: of two requests at once only one gets the script
	now := time.Now().Unix()
	n, err := s.Queries.UseWgToken(ctx, db.UseWgTokenParams{UsedAt: now, UsedIp: core.ClientIP(r), RouterID: id, ExpiresAt: now})
	if err != nil {
		s.Fail(w, "use provision token", err)
		return
	}
	if n != 1 {
		refuse("claimed", id)
		return
	}
	if pending {
		if err := s.prov().SwitchCreds(ctx, rt, p.Ip, pendingEnc); err != nil {
			s.Fail(w, "switch router api user", err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(script))
}

// prov is the provisioning service. It is built per call because SecretKey is set after New.
func (s *Server) prov() *provision.Service {
	return &provision.Service{DB: s.conn, Q: s.Queries, Key: s.SecretKey, Now: s.now}
}
