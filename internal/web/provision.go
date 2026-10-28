package web

// GET /provision/{token}: the RouterOS script a router fetches with the one-line command. The router is not
// a logged-in admin; the token is the credential. It is "<router id>-<64 hex>", valid provisionTTL, used
// once, and only its sha256 is stored. The script carries the private key and the secrets, so a refusal
// never says why (the log does).

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/wg"
)

const provisionTTL = 15 * time.Minute

func tokenHash(token string) string {
	_, rnd, _ := strings.Cut(token, "-")
	sum := sha256.Sum256([]byte(rnd))
	return hex.EncodeToString(sum[:])
}

func (s *Server) provisionScript(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	refuse := func(why string, id int64) {
		slog.Warn("provision link refused", "why", why, "router", id, "ip", clientIP(r))
		http.Error(w, "This provisioning link is expired or already used. Provision the router again in gobill.", http.StatusGone)
	}
	token := r.PathValue("token")
	idText, _, _ := strings.Cut(token, "-")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		refuse("malformed", 0)
		return
	}
	k, err := s.queries.GetWgToken(ctx, id)
	if err != nil {
		refuse("unknown", id)
		return
	}
	if subtle.ConstantTimeCompare([]byte(tokenHash(token)), []byte(k.TokenHash)) != 1 {
		refuse("wrong token", id)
		return
	}
	if k.UsedAt != 0 { // a second use from another address is the case to notice
		if k.UsedIp != clientIP(r) {
			slog.Warn("provision link used again from another address", "router", id, "first", k.UsedIp, "now", clientIP(r))
		}
		refuse("used", id)
		return
	}
	if time.Now().Unix() >= k.ExpiresAt {
		refuse("expired", id)
		return
	}
	rt, err := s.queries.GetRouter(ctx, id)
	if err != nil {
		refuse("no router", id)
		return
	}
	p, err := s.queries.GetWgPeer(ctx, id)
	if err == sql.ErrNoRows {
		refuse("no peer", id)
		return
	} else if err != nil {
		s.fail(w, "get wireguard peer", err)
		return
	}
	cfg, st, err := s.wgUsable(ctx)
	if err != nil {
		http.Error(w, "gobill WireGuard is not available", http.StatusServiceUnavailable)
		return
	}
	priv, err := secret.Open(s.SecretKey, p.PrivateEnc)
	if err != nil {
		s.fail(w, "open wireguard key", err)
		return
	}
	serverPub, err := wg.PublicKey(cfg.Private)
	if err != nil {
		s.fail(w, "wireguard server key", err)
		return
	}
	radiusSecret, err := s.wgNAS(ctx, s.queries, rt, p.Ip)
	if err != nil {
		s.fail(w, "wireguard nas", err)
		return
	}
	apiPass, err := secret.Open(s.SecretKey, rt.PasswordEnc)
	if err != nil {
		s.fail(w, "open router api password", err)
		return
	}
	script := wgBlock{Private: string(priv), ServerPub: serverPub, Endpoint: wgDefaults(st)["wg_endpoint"], Secret: radiusSecret, APIPass: string(apiPass),
		Port: cfg.Port, Self: cfg.Self(), Peer: netip.MustParseAddr(p.Ip), Bits: cfg.Subnet.Bits()}.script()
	// the claim is atomic: of two requests at once only one gets the script
	now := time.Now().Unix()
	n, err := s.queries.UseWgToken(ctx, db.UseWgTokenParams{UsedAt: now, UsedIp: clientIP(r), RouterID: id, ExpiresAt: now})
	if err != nil {
		s.fail(w, "use provision token", err)
		return
	}
	if n != 1 {
		refuse("claimed", id)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(script))
}
