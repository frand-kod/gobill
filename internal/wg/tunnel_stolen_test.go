//go:build !nowg

package wg

import (
	"context"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
)

// A stolen router must reach gobill's RADIUS and nothing else: not another router, not a TCP port of
// gobill (the admin HTTP among them), and not under another router's address. Revoking it cuts it at once.
func TestTunnelStolenRouter(t *testing.T) {
	if p, _ := peerConfig(db.WgPeer{Ip: "10.99.0.2", PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}); strings.Count(p, "allowed_ip=") != 1 || !strings.Contains(p, "allowed_ip=10.99.0.2/32\n") {
		t.Fatalf("a peer must have one /32 AllowedIPs entry:\n%s", p)
	}
	conn1, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()
	if err := db.Migrate(conn1); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn1)
	ctx := t.Context()
	gobillPort := freeUDPPort(t)
	for k, v := range map[string]string{"wg_enabled": "yes", "wg_port": strconv.Itoa(gobillPort), "wg_subnet": "10.99.0.0/24"} {
		q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v})
	}
	key := make([]byte, 32)
	type router struct {
		net  *netstack.Net
		priv string
		pub  string
		ip   string
		dev  *device.Device
	}
	rs := make([]*router, 2)
	for i := range rs {
		rt, err := q.CreateRouter(ctx, db.CreateRouterParams{Name: "r" + strconv.Itoa(i), Host: "x", Port: 8728, Username: "u", PasswordEnc: []byte("x")})
		if err != nil {
			t.Fatal(err)
		}
		r := &router{ip: "10.99.0." + strconv.Itoa(i+2)}
		r.priv, r.pub = GenKey()
		sealed, _ := secret.Seal(key, []byte(r.priv))
		if err := q.UpsertWgPeer(ctx, db.UpsertWgPeerParams{RouterID: rt.ID, Ip: r.ip, PublicKey: r.pub, PrivateEnc: sealed, ProvisionedAt: 1}); err != nil {
			t.Fatal(err)
		}
		rs[i] = r
	}
	m := &Manager{Q: q, Key: key, Radius: &radius.Server{Q: q, Key: key, Trusted: func() bool { return true }}, AuthPort: 1812, AcctPort: 1813}
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "wg_enabled", Value: "no"}); m.Reload(ctx) }()
	st, _ := q.ListSettings(ctx)
	var spriv string
	for _, s := range st {
		if s.Key == "wg_private" {
			spriv = s.Value
		}
	}
	spub, _ := PublicKey(spriv)
	spk, _ := hexKey(spub)
	for _, r := range rs { // each router is a userspace device
		tun, rnet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr(r.ip)}, nil, 1420)
		if err != nil {
			t.Fatal(err)
		}
		r.net = rnet
		r.dev = device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
		defer r.dev.Close()
		pk, _ := hexKey(r.priv)
		// the router side allows the whole subnet on purpose: it is what a leaked config could try
		if err := r.dev.IpcSet("private_key=" + pk + "\npublic_key=" + spk + "\nendpoint=127.0.0.1:" + strconv.Itoa(gobillPort) + "\nallowed_ip=10.99.0.0/24\n"); err != nil {
			t.Fatal(err)
		}
		if err := r.dev.Up(); err != nil {
			t.Fatal(err)
		}
	}
	a, b := rs[0], rs[1]
	for _, r := range rs {
		c, err := r.net.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort("10.99.0.1:9"))
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("hi"))
		c.Close()
	}
	waitFor(t, "both handshakes", func() bool { return !m.Stats()[a.pub].Handshake.IsZero() && !m.Stats()[b.pub].Handshake.IsZero() })

	// no routing between peers: B listens, A sends to B, nothing arrives
	bl, err := b.net.ListenUDPAddrPort(netip.MustParseAddrPort(b.ip + ":9000"))
	if err != nil {
		t.Fatal(err)
	}
	defer bl.Close()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		if n, _, err := bl.ReadFrom(buf); err == nil {
			got <- string(buf[:n])
		}
	}()
	ac, err := a.net.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort(b.ip+":9000"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		ac.Write([]byte("to-b"))
		time.Sleep(200 * time.Millisecond)
	}
	ac.Close()
	// a spoofed source (A sends as B) is dropped by AllowedIPs before it reaches RADIUS
	before := m.RadiusSeen(b.ip)
	if sp, err := a.net.DialUDPAddrPort(netip.MustParseAddrPort(b.ip+":0"), netip.MustParseAddrPort("10.99.0.1:1812")); err == nil {
		sp.Write([]byte("spoof"))
		sp.Close()
		time.Sleep(500 * time.Millisecond)
	}
	select {
	case s := <-got:
		t.Fatalf("router B received %q from router A: peers must not route to each other", s)
	default:
	}
	if m.RadiusSeen(b.ip) != before {
		t.Error("a packet sent by A as B reached RADIUS")
	}

	// gobill listens for UDP RADIUS only: TCP to 1812 or to the admin HTTP port is refused
	for _, port := range []string{"1812", "8080", "80", "443"} {
		cx, cancel := context.WithTimeout(ctx, 2*time.Second)
		c, err := a.net.DialContextTCPAddrPort(cx, netip.MustParseAddrPort("10.99.0.1:"+port))
		cancel()
		if err == nil {
			c.Close()
			t.Errorf("TCP port %s of gobill is reachable through the tunnel", port)
		}
	}

	// revoke: the peer is gone and its RADIUS packets no longer arrive (first, that they do arrive before)
	rc, err := a.net.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort("10.99.0.1:1812"))
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	rc.Write([]byte("early"))
	waitFor(t, "RADIUS packet of router A", func() bool { return !m.RadiusSeen(a.ip).IsZero() })
	if err := m.RemovePeer(a.pub); err != nil {
		t.Fatal(err)
	}
	seen := m.RadiusSeen(a.ip)
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 3; i++ {
		rc.Write([]byte("late"))
		time.Sleep(200 * time.Millisecond)
	}
	if m.RadiusSeen(a.ip) != seen {
		t.Error("a revoked router still reaches RADIUS")
	}
}
