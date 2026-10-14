package radius

// Bad-secret handling. layeh's PacketServer drops packets whose signature does not match the
// secret, but logs that without the source address, once per retransmit. The socket is wrapped
// here so the check runs first and the drop is logged, counted and alerted with the NAS name.

import (
	"context"
	"log/slog"
	"net"
	"time"

	"layeh.com/radius"

	"github.com/frand-kod/gobill/internal/metrics"
)

// nasConn is the RADIUS socket: it drops packets that fail the NAS signature check.
type nasConn struct {
	net.PacketConn
	s *Server
}

// ReadFrom returns the next packet whose signature matches its NAS secret, or one from an unknown
// NAS (the library refuses those itself).
func (c *nasConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		n, addr, err := c.PacketConn.ReadFrom(b)
		if err != nil || c.s.verifySource(b[:n], addr) {
			return n, addr, err
		}
	}
}

// udpReadBuffer is the receive buffer asked for each RADIUS socket.
const udpReadBuffer = 4 << 20

// serve is PacketServer.ListenAndServe with the socket wrapped in nasConn.
func (s *Server) serve(ps *radius.PacketServer) error {
	pc, err := net.ListenPacket("udp", ps.Addr)
	if err != nil {
		return err
	}
	defer pc.Close()
	// The default buffer (about 200 KB) holds a few hundred packets. On one core the read loop waits
	// behind the handlers each commit wakes, and the kernel dropped packets meanwhile (RcvbufErrors).
	// Linux caps this at net.core.rmem_max.
	if u, ok := pc.(*net.UDPConn); ok {
		if err := u.SetReadBuffer(udpReadBuffer); err != nil {
			slog.Warn("radius: set socket read buffer", "err", err)
		}
	}
	return ps.Serve(&nasConn{PacketConn: pc, s: s})
}

// verifySource reports whether pkt may be handed to the library. Access-Request has no signature
// the library can check; handleAuth checks its Message-Authenticator instead.
// It runs on the read loop; the nas lookup is served from the listNAS cache.
func (s *Server) verifySource(pkt []byte, addr net.Addr) bool {
	ip := addrIP(addr)
	if len(pkt) > 0 && radius.Code(pkt[0]) == radius.CodeAccessRequest {
		return true
	}
	n, ok, err := s.nasByIP(context.Background(), ip)
	if err != nil || !ok {
		return true // unknown NAS: RADIUSSecret reports it
	}
	sec := n.sec
	if n.secErr != nil || len(sec) == 0 {
		return true // the library reports these
	}
	if !radius.IsAuthenticRequest(pkt, sec) {
		s.badSecret(ip, n.Name)
		return false
	}
	s.secretOK(ip)
	return true
}

// badSecret counts a packet from a known NAS whose signature does not match its secret, and logs it
// at most once per NAS per minute.
func (s *Server) badSecret(ip net.IP, name string) {
	key := ip.String()
	metrics.Inc("radius_bad_secret_total", "nas", key)
	metrics.Set("radius_bad_secret_last_timestamp", float64(s.now().Unix()), "nas", key)
	metrics.Set("radius_bad_secret_streak", metrics.Value("radius_bad_secret_streak", "nas", key)+1, "nas", key)
	if s.allowLog(&s.badSecretWarned, key, time.Minute) {
		slog.Warn("radius: packet dropped, bad secret", "ip", key, "nas", name,
			"hint", "secret in gobill NAS does not match /radius on the router")
	}
}

// secretOK ends a run of bad-secret packets from a NAS: its secret checked out.
func (s *Server) secretOK(ip net.IP) {
	key := ip.String()
	if metrics.Value("radius_bad_secret_streak", "nas", key) != 0 {
		metrics.Set("radius_bad_secret_streak", 0, "nas", key)
	}
}
