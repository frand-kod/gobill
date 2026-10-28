//go:build !nowg

package wg

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/frand-kod/gobill/internal/db"
	devpkg "github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/radius"
)

const built = true

// mtu matches the RouterOS WireGuard default.
const mtu = 1420

type tunnel struct {
	dev  *device.Device
	tnet *netstack.Net
	stop context.CancelFunc // ends the RADIUS servers
	wg   sync.WaitGroup
}

func (t *tunnel) running() bool { return t.dev != nil }

// seenConn notes the time of the last packet from each source IP, for the RADIUS stage of a router.
type seenConn struct {
	net.PacketConn
	m *Manager
}

func (c seenConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(b)
	if u, ok := addr.(*net.UDPAddr); ok && err == nil {
		c.m.seen.Store(u.IP.String(), time.Now())
	}
	return n, addr, err
}

func hexKey(b64 string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != 32 {
		return "", errors.New("bad WireGuard key")
	}
	return hex.EncodeToString(b), nil
}

func (t *tunnel) up(m *Manager, cfg Config) error {
	priv, err := hexKey(cfg.Private)
	if err != nil {
		return err
	}
	self := cfg.Self()
	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{self}, nil, mtu)
	if err != nil {
		return err
	}
	dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "wg: "))
	if err := dev.IpcSet("private_key=" + priv + "\nlisten_port=" + strconv.Itoa(cfg.Port) + "\n"); err != nil {
		dev.Close()
		return err
	}
	// Up before the peers are added: a peer with a stored endpoint and a keepalive sends its handshake the
	// moment it is added to a running device.
	if err := dev.Up(); err != nil {
		dev.Close()
		return err
	}
	sub := cfg.Subnet
	t.dev, t.tnet = dev, tnet
	var ctx context.Context
	ctx, t.stop = context.WithCancel(context.Background())
	if m.Radius != nil {
		for i, port := range []int{m.AuthPort, m.AcctPort} {
			pc, err := tnet.ListenUDPAddrPort(netip.AddrPortFrom(self, uint16(port)))
			if err != nil {
				return err
			}
			t.wg.Add(1)
			go func() {
				defer t.wg.Done()
				if err := m.Radius.ServeConn(ctx, seenConn{pc, m}, i == 1); err != nil && !errors.Is(err, net.ErrClosed) {
					slog.Warn("wireguard: radius inside the tunnel stopped", "err", err)
				}
			}()
		}
	}
	// Hooks are set last; they only answer for addresses inside the tunnel and dial the rest as usual.
	devpkg.DialTCP = func(ctx context.Context, addr string) (net.Conn, error) {
		if ap, ok := inside(sub, addr); ok {
			return tnet.DialContextTCPAddrPort(ctx, ap)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	radius.DialUDP = func(ctx context.Context, addr string) (net.Conn, error) {
		if ap, ok := inside(sub, addr); ok {
			return tnet.DialUDPAddrPort(netip.AddrPortFrom(self, 0), ap)
		}
		return (&net.Dialer{}).DialContext(ctx, "udp", addr)
	}
	return nil
}

// inside parses addr and reports whether it is a host in the tunnel subnet.
func inside(sub netip.Prefix, addr string) (netip.AddrPort, bool) {
	ap, err := netip.ParseAddrPort(addr)
	return ap, err == nil && sub.Contains(ap.Addr())
}

func (t *tunnel) down() {
	if t.dev == nil {
		return
	}
	devpkg.DialTCP, radius.DialUDP = nil, nil
	t.stop()
	t.wg.Wait()
	t.dev.Close()
	*t = tunnel{}
}

// peerConfig is the IpcSet text of one peer. A stored endpoint comes with a keepalive so gobill dials first.
func peerConfig(p db.WgPeer) (string, error) {
	pub, err := hexKey(p.PublicKey)
	if err != nil {
		return "", err
	}
	s := "public_key=" + pub + "\nallowed_ip=" + p.Ip + "/32\n"
	if _, err := netip.ParseAddrPort(p.Endpoint); err == nil {
		s += "endpoint=" + p.Endpoint + "\npersistent_keepalive_interval=25\n"
	}
	return s, nil
}

func (t *tunnel) addPeer(_ Config, p db.WgPeer) error {
	if t.dev == nil {
		return nil
	}
	s, err := peerConfig(p)
	if err != nil {
		return err
	}
	return t.dev.IpcSet(s)
}

func (t *tunnel) removePeer(pub string) error {
	if t.dev == nil {
		return nil
	}
	h, err := hexKey(pub)
	if err != nil {
		return err
	}
	return t.dev.IpcSet("public_key=" + h + "\nremove=true\n")
}

func (t *tunnel) stats() map[string]Stat {
	if t.dev == nil {
		return nil
	}
	dump, err := t.dev.IpcGet()
	if err != nil {
		return nil
	}
	out := map[string]Stat{}
	var pub string
	var s Stat
	flush := func() {
		if pub != "" {
			out[pub] = s
		}
	}
	for _, line := range strings.Split(dump, "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "public_key":
			flush()
			b, _ := hex.DecodeString(v)
			pub, s = base64.StdEncoding.EncodeToString(b), Stat{}
		case "endpoint":
			s.Endpoint = v
		case "last_handshake_time_sec":
			if n, _ := strconv.ParseInt(v, 10, 64); n > 0 {
				s.Handshake = time.Unix(n, 0)
			}
		case "rx_bytes":
			s.Rx, _ = strconv.ParseInt(v, 10, 64)
		case "tx_bytes":
			s.Tx, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	flush()
	return out
}
