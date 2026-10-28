// Package wg is the built-in WireGuard server: userspace (wireguard-go on a netstack), so it needs no root,
// no /dev/net/tun and no host package. Routers open the tunnel to gobill; RADIUS (1812/1813) is served
// inside it, and CoA and the RouterOS API are dialed through it. The build tag nowg removes it from the binary.
package wg

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
)

const (
	DefaultPort   = 51820
	DefaultSubnet = "10.99.0.0/24"
)

// Disabled is the kill-switch: GOBILL_WG=off beats the setting in the UI.
func Disabled() bool { return os.Getenv("GOBILL_WG") == "off" }

// Config is the tunnel as the settings describe it.
type Config struct {
	Port    int
	Subnet  netip.Prefix
	Private string // base64
}

// Self is gobill's own tunnel address, the first host of the subnet.
func (c Config) Self() netip.Addr { return c.Subnet.Masked().Addr().Next() }

// LoadConfig reads the wg_* settings (defaults filled in). The private key may be empty.
func LoadConfig(st map[string]string) (Config, error) {
	c := Config{Port: DefaultPort, Private: st["wg_private"]}
	if v := st["wg_port"]; v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return c, fmt.Errorf("wg_port %q", v)
		}
		c.Port = p
	}
	sub := st["wg_subnet"]
	if sub == "" {
		sub = DefaultSubnet
	}
	var err error
	if c.Subnet, err = ParseSubnet(sub); err != nil {
		return c, err
	}
	return c, nil
}

// ParseSubnet accepts a private IPv4 range from /16 to /29, so there are addresses for gobill and a router.
func ParseSubnet(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() || p.Bits() < 16 || p.Bits() > 29 || !p.Addr().IsPrivate() {
		return p, errors.New("want a private IPv4 range from /16 to /29, e.g. 10.99.0.0/24")
	}
	return p.Masked(), nil
}

// GenKey makes a Curve25519 keypair, both base64.
func GenKey() (priv, pub string) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		panic(err) // the system random source failed
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())
}

// PublicKey is the public half of a base64 private key.
func PublicKey(priv string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(priv)
	if err != nil {
		return "", err
	}
	k, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// AllocIP is the lowest free host of the subnet after gobill's own address.
func AllocIP(sub netip.Prefix, used []string) (netip.Addr, error) {
	taken := map[netip.Addr]bool{}
	for _, u := range used {
		if a, err := netip.ParseAddr(u); err == nil {
			taken[a] = true
		}
	}
	for a := (Config{Subnet: sub}).Self().Next(); sub.Contains(a); a = a.Next() {
		if !taken[a] && sub.Contains(a.Next()) { // the last address is the broadcast
			return a, nil
		}
	}
	return netip.Addr{}, errors.New("no free address left in the tunnel subnet")
}

// Stat is what the device knows about one peer.
type Stat struct {
	Handshake time.Time // zero = never
	Endpoint  string
	Rx, Tx    int64
}

// Manager owns the tunnel. Reload reads the settings and peers from the database and brings it up, down
// or to a new config; AddPeer, RemovePeer and ResetPeer change one peer without touching the others.
type Manager struct {
	Q                  *db.Queries
	Key                []byte
	Radius             *radius.Server // serves the tunnel side of RADIUS; nil = none
	AuthPort, AcctPort int            // RADIUS ports inside the tunnel

	mu   sync.Mutex
	t    tunnel
	cfg  Config
	err  error
	seen sync.Map // router tunnel IP string -> time.Time of its last RADIUS packet
}

// Built reports whether this binary has WireGuard (false with the nowg tag).
const Built = built

// Running reports whether the tunnel is up.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t.running()
}

// Err is why the last Reload could not bring the tunnel up, nil when it did or was not asked to.
func (m *Manager) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// Stats is the device state per peer, keyed by the peer's public key (base64).
func (m *Manager) Stats() map[string]Stat {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t.stats()
}

// RadiusSeen is when the router at tunnel address ip last sent a RADIUS packet, zero = never.
func (m *Manager) RadiusSeen(ip string) time.Time {
	t, _ := m.seen.Load(ip)
	tt, _ := t.(time.Time)
	return tt
}

// Reload applies the settings: the tunnel runs when wg_enabled is yes, GOBILL_WG is not off and the build has it.
func (m *Manager) Reload(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = m.reload(ctx)
	if m.err != nil {
		slog.Error("wireguard", "err", m.err)
	}
	return m.err
}

func (m *Manager) reload(ctx context.Context) error {
	rows, err := m.Q.ListSettings(ctx)
	if err != nil {
		return err
	}
	st := make(map[string]string, len(rows))
	for _, r := range rows {
		st[r.Key] = r.Value
	}
	m.t.down() // a new port, subnet or key needs a new device; peers start again from their stored endpoint
	if st["wg_enabled"] != "yes" || Disabled() || !Built {
		return nil
	}
	cfg, err := LoadConfig(st)
	if err != nil {
		return err
	}
	if cfg.Private == "" { // the first time WireGuard is on
		cfg.Private, _ = GenKey()
		if err := m.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "wg_private", Value: cfg.Private}); err != nil {
			return err
		}
	}
	peers, err := m.Q.ListWgPeers(ctx)
	if err != nil {
		return err
	}
	m.cfg = cfg
	if err := m.t.up(m, cfg); err != nil {
		m.t.down()
		return err
	}
	for _, p := range peers {
		if err := m.t.addPeer(cfg, p); err != nil {
			slog.Error("wireguard: add peer", "router", p.RouterID, "err", err)
		}
	}
	slog.Info("wireguard: tunnel up", "addr", cfg.Self(), "port", cfg.Port, "peers", len(peers))
	return nil
}

// AddPeer adds a peer to the running tunnel; without a tunnel it does nothing.
func (m *Manager) AddPeer(p db.WgPeer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t.addPeer(m.cfg, p)
}

// RemovePeer drops a peer by public key (base64). The router can no longer reach gobill.
func (m *Manager) RemovePeer(pub string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t.removePeer(pub)
}

// ResetPeer forgets the handshake state of a peer, including the last timestamp it used. A router whose
// clock went back after a reboot is refused by WireGuard until then.
func (m *Manager) ResetPeer(p db.WgPeer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.t.removePeer(p.PublicKey); err != nil {
		return err
	}
	return m.t.addPeer(m.cfg, p)
}

// SaveEndpoints stores the address each peer was last seen at. After a restart gobill starts from it and
// sends the handshake itself, so a restart does not leave the router waiting for its next attempt.
func (m *Manager) SaveEndpoints(ctx context.Context) error {
	stats := m.Stats()
	if len(stats) == 0 {
		return nil
	}
	peers, err := m.Q.ListWgPeers(ctx)
	if err != nil {
		return err
	}
	for _, p := range peers {
		if s, ok := stats[p.PublicKey]; ok && s.Endpoint != "" && s.Endpoint != p.Endpoint {
			if err := m.Q.SetWgPeerEndpoint(ctx, db.SetWgPeerEndpointParams{Endpoint: s.Endpoint, RouterID: p.RouterID}); err != nil {
				return err
			}
		}
	}
	return nil
}

// PrivateKey opens a peer's sealed private key (base64).
func (m *Manager) PrivateKey(p db.WgPeer) (string, error) {
	b, err := secret.Open(m.Key, p.PrivateEnc)
	return string(b), err
}
