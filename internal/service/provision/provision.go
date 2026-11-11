// Package provision is the WireGuard provisioning of a router: the peer, its NAS row, the one-time token and
// the pending API password. No HTTP here; internal/web parses, calls and renders.
package provision

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/settings"
	"github.com/frand-kod/gobill/internal/wg"
)

// TTL is how long a provision token works.
const TTL = 15 * time.Minute

type Service struct {
	DB  *sql.DB
	Q   *db.Queries
	Key []byte
	Now func() time.Time // nil = time.Now
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// TokenHash is what is stored of a token "<router id>-<64 hex>": the sha256 of the random part.
func TokenHash(token string) string {
	_, rnd, _ := strings.Cut(token, "-")
	sum := sha256.Sum256([]byte(rnd))
	return hex.EncodeToString(sum[:])
}

// Creds is what a router's API credentials were before the tunnel took them over (setting wg_prev_<id>).
type Creds struct {
	Host, User string
	Port       int64
	TLS        int64
	Pass       []byte
}

func PendingKey(id int64) string { return fmt.Sprint("wg_pending_", id) } // base64 sealed API password, until the script is fetched
func PrevKey(id int64) string    { return fmt.Sprint("wg_prev_", id) }

// Result of Provision. Old is the peer it replaced, nil on a first run; the caller removes it from the
// live tunnel and adds the new one after the commit.
type Result struct {
	Token string
	Old   *db.WgPeer
}

// AddressError says why no tunnel address could be given (the subnet is full); its text is shown to the admin.
type AddressError struct{ Err error }

func (e *AddressError) Error() string { return e.Err.Error() }
func (e *AddressError) Unwrap() error { return e.Err }

// Provision makes a new keypair, address, NAS row, pending API password and token for a router in one
// transaction. The old key stops working once the caller drops the old peer. A full subnet is an *AddressError.
func (s *Service) Provision(ctx context.Context, rt db.Router, cfg wg.Config) (Result, error) {
	priv, pub := wg.GenKey()
	sealed, err := secret.Seal(s.Key, []byte(priv))
	if err != nil {
		return Result{}, fmt.Errorf("seal wireguard key: %w", err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	q := s.Q.WithTx(tx)
	peers, err := q.ListWgPeers(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list wireguard peers: %w", err)
	}
	var old *db.WgPeer
	var used []string
	for i, p := range peers {
		if p.RouterID == rt.ID {
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
			return Result{}, &AddressError{err}
		}
	}
	if old != nil && old.Ip != ip.String() { // the old address and its secret are not kept
		if err := DropNAS(ctx, q, old.Ip); err != nil {
			return Result{}, fmt.Errorf("wireguard nas: %w", err)
		}
	}
	if _, err := s.NAS(ctx, q, rt, ip.String()); err != nil {
		return Result{}, fmt.Errorf("wireguard nas: %w", err)
	}
	if err := q.UpsertWgPeer(ctx, db.UpsertWgPeerParams{RouterID: rt.ID, Ip: ip.String(), PublicKey: pub, PrivateEnc: sealed, ProvisionedAt: s.now().Unix()}); err != nil {
		return Result{}, fmt.Errorf("save wireguard peer: %w", err)
	}
	// the router is reached through the tunnel as the API user the script creates, with a new password
	apiEnc, err := secret.Seal(s.Key, []byte(secret.RandomPassword(24)))
	if err != nil {
		return Result{}, fmt.Errorf("seal api password: %w", err)
	}
	// The router keeps its stored credentials until the script is fetched (SwitchCreds runs then).
	if err := q.UpsertSettingRaw(ctx, db.UpsertSettingRawParams{Key: PendingKey(rt.ID), Value: base64.StdEncoding.EncodeToString(apiEnc)}); err != nil {
		return Result{}, fmt.Errorf("save pending router api user: %w", err)
	}
	rnd := make([]byte, 32)
	rand.Read(rnd) // never returns an error in Go 1.24+
	token := fmt.Sprintf("%d-%x", rt.ID, rnd)
	if err := q.SetWgToken(ctx, db.SetWgTokenParams{RouterID: rt.ID, TokenHash: TokenHash(token), ExpiresAt: s.now().Add(TTL).Unix()}); err != nil {
		return Result{}, fmt.Errorf("save provision token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return Result{Token: token, Old: old}, nil
}

// NAS makes sure a NAS row covers the tunnel address of a router and returns its RADIUS secret.
// An existing row for that address keeps its secret; a new one gets its own.
func (s *Service) NAS(ctx context.Context, q *db.Queries, rt db.Router, ip string) (string, error) {
	rows, err := q.ListNAS(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range rows {
		if n.Ip == ip {
			b, err := secret.Open(s.Key, n.SecretEnc)
			return string(b), err
		}
	}
	sec := secret.RandomPassword(24)
	enc, err := secret.Seal(s.Key, []byte(sec))
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

// DropNAS deletes the NAS row of a tunnel address.
func DropNAS(ctx context.Context, q *db.Queries, ip string) error {
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

// Pending is the sealed API password waiting for the script to be fetched.
func (s *Service) Pending(ctx context.Context, id int64) ([]byte, bool) {
	v := settings.Get(ctx, s.Q, PendingKey(id))
	enc, err := base64.StdEncoding.DecodeString(v)
	return enc, v != "" && err == nil
}

// SwitchCreds points the router at its tunnel address and new API user, remembering the previous
// credentials (first switch only) so revoking can give them back.
func (s *Service) SwitchCreds(ctx context.Context, rt db.Router, ip string, pass []byte) error {
	if prev := settings.Get(ctx, s.Q, PrevKey(rt.ID)); prev == "" {
		b, _ := json.Marshal(Creds{Host: rt.Host, Port: rt.Port, TLS: rt.Tls, User: rt.Username, Pass: rt.PasswordEnc})
		if err := s.Q.UpsertSettingRaw(ctx, db.UpsertSettingRawParams{Key: PrevKey(rt.ID), Value: string(b)}); err != nil {
			return err
		}
	}
	if err := s.Q.UpdateRouter(ctx, db.UpdateRouterParams{Name: rt.Name, Host: ip, Port: 8728, Username: User, PasswordEnc: pass,
		Description: rt.Description, Enabled: rt.Enabled, Coordinates: rt.Coordinates, Coverage: rt.Coverage, CertPin: rt.CertPin, ID: rt.ID}); err != nil {
		return err
	}
	// the tunnel address is reached over the plain API on 8728
	if err := s.Q.SetRouterTLS(ctx, db.SetRouterTLSParams{Tls: 0, ID: rt.ID}); err != nil {
		return err
	}
	return s.Q.UpsertSettingRaw(ctx, db.UpsertSettingRawParams{Key: PendingKey(rt.ID)})
}
