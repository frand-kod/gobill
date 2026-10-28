//go:build !nowg

package wg

import (
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	rad "layeh.com/radius"
	"layeh.com/radius/rfc2865"

	"github.com/frand-kod/gobill/internal/db"
	devpkg "github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
)

func freeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

// A router is a second userspace device on loopback. These tests run the real tunnel: the first
// handshake, the kill-switch, a reset, and the restart that must not wait for the router.
func TestTunnelHandshakeResetAndRestart(t *testing.T) {
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
	gobillPort, routerPort := freeUDPPort(t), freeUDPPort(t)
	for k, v := range map[string]string{"wg_enabled": "yes", "wg_port": strconv.Itoa(gobillPort), "wg_subnet": "10.99.0.0/24"} {
		q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v})
	}
	rt, err := q.CreateRouter(ctx, db.CreateRouterParams{Name: "r", Host: "10.99.0.2", Port: 8728, Username: "u", PasswordEnc: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	rpriv, rpub := GenKey()
	key := make([]byte, 32)
	sealed, _ := secret.Seal(key, []byte(rpriv))
	if err := q.UpsertWgPeer(ctx, db.UpsertWgPeerParams{RouterID: rt.ID, Ip: "10.99.0.2", PublicKey: rpub, PrivateEnc: sealed, ProvisionedAt: 1}); err != nil {
		t.Fatal(err)
	}

	const nasSecret = "s3cret-for-r"
	enc, _ := secret.Seal(key, []byte(nasSecret))
	if _, err := q.CreateNAS(ctx, db.CreateNASParams{Name: "wg-r", Ip: "10.99.0.2", SecretEnc: enc}); err != nil {
		t.Fatal(err)
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
	spub, err := PublicKey(spriv)
	if err != nil {
		t.Fatal(err)
	}

	// the router: no keepalive, so only traffic from it (or from gobill) makes a handshake
	rtun, rnet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.99.0.2")}, nil, 1420)
	if err != nil {
		t.Fatal(err)
	}
	rdev := device.NewDevice(rtun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
	defer rdev.Close()
	rpk, _ := hexKey(rpriv)
	spk, _ := hexKey(spub)
	if err := rdev.IpcSet("private_key=" + rpk + "\nlisten_port=" + strconv.Itoa(routerPort) + "\npublic_key=" + spk +
		"\nendpoint=127.0.0.1:" + strconv.Itoa(gobillPort) + "\nallowed_ip=10.99.0.1/32\n"); err != nil {
		t.Fatal(err)
	}
	if err := rdev.Up(); err != nil {
		t.Fatal(err)
	}
	c, err := rnet.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort("10.99.0.1:9"))
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("hi"))
	c.Close()
	handshaken := func() bool { return !m.Stats()[rpub].Handshake.IsZero() }
	waitFor(t, "first handshake", handshaken)

	// RADIUS inside the tunnel: the router asks, gobill answers (an unknown user is rejected, which still
	// proves the path and the secret), and gobill sees the packet came from the router's tunnel address
	rc, err := rnet.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort("10.99.0.1:1812"))
	if err != nil {
		t.Fatal(err)
	}
	req := rad.New(rad.CodeAccessRequest, []byte(nasSecret))
	rfc2865.UserName_SetString(req, "nobody")
	rfc2865.UserPassword_SetString(req, "x")
	wire, _ := req.Encode()
	rc.SetDeadline(time.Now().Add(5 * time.Second))
	rc.Write(wire)
	buf := make([]byte, 4096)
	n, err := rc.Read(buf)
	if err != nil {
		t.Fatalf("no RADIUS answer through the tunnel: %v", err)
	}
	resp, err := rad.Parse(buf[:n], []byte(nasSecret))
	if err != nil || resp.Code != rad.CodeAccessReject || !rad.IsAuthenticResponse(buf[:n], wire, []byte(nasSecret)) {
		t.Fatalf("RADIUS answer: %v %v", resp, err)
	}
	rc.Close()
	if m.RadiusSeen("10.99.0.2").IsZero() {
		t.Error("the RADIUS packet was not noted for the router")
	}

	// CoA: gobill dials the router's tunnel address on 3799 and any reply counts (the router answers NAK)
	coa, err := rnet.ListenUDPAddrPort(netip.MustParseAddrPort("10.99.0.2:3799"))
	if err != nil {
		t.Fatal(err)
	}
	defer coa.Close()
	go func() {
		b := make([]byte, 4096)
		for {
			n, from, err := coa.ReadFrom(b)
			if err != nil {
				return
			}
			pk, err := rad.Parse(b[:n], []byte(nasSecret))
			if err != nil {
				continue
			}
			out, _ := pk.Response(rad.CodeDisconnectNAK).Encode()
			coa.WriteTo(out, from)
		}
	}()
	if err := radius.ProbeCoA(ctx, q, key, "10.99.0.2"); err != nil {
		t.Fatalf("CoA probe through the tunnel: %v", err)
	}

	// the RouterOS API: gobill's TCP dial reaches a listener on the router's tunnel address
	api, err := rnet.ListenTCPAddrPort(netip.MustParseAddrPort("10.99.0.2:8728"))
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	go func() {
		if c, err := api.Accept(); err == nil {
			c.Close()
		}
	}()
	tc, err := devpkg.DialTCP(ctx, "10.99.0.2:8728")
	if err != nil {
		t.Fatalf("API dial through the tunnel: %v", err)
	}
	tc.Close()

	// the address the router was seen at is stored, then used after a restart
	if err := m.SaveEndpoints(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := q.GetWgPeer(ctx, rt.ID)
	if p.Endpoint != "127.0.0.1:"+strconv.Itoa(routerPort) {
		t.Fatalf("stored endpoint %q", p.Endpoint)
	}
	// control: without the stored endpoint a restart leaves the tunnel down until the router speaks
	q.SetWgPeerEndpoint(ctx, db.SetWgPeerEndpointParams{Endpoint: "", RouterID: rt.ID})
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if handshaken() {
		t.Fatal("handshake without a stored endpoint and without traffic from the router")
	}
	q.SetWgPeerEndpoint(ctx, db.SetWgPeerEndpointParams{Endpoint: p.Endpoint, RouterID: rt.ID})
	if err := m.Reload(ctx); err != nil { // a restart of the app: the router sends nothing
		t.Fatal(err)
	}
	waitFor(t, "handshake started by gobill after a restart", handshaken)

	// reset: the peer is dropped and added again, so the handshake is new
	if err := m.ResetPeer(p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "handshake after reset", handshaken)

	// remove: the peer is gone from the device
	if err := m.RemovePeer(rpub); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Stats()[rpub]; ok {
		t.Fatal("peer still on the device after remove")
	}

	// kill-switch: GOBILL_WG=off stops the tunnel whatever the setting says
	t.Setenv("GOBILL_WG", "off")
	if err := m.Reload(ctx); err != nil || m.Running() {
		t.Fatalf("kill-switch: running=%v err=%v", m.Running(), err)
	}
}
