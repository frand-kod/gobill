package radius

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

// RADIUSSecret finds the NAS by source IP (exact or CIDR) and returns its secret.
// An error makes the library drop the packet.
func (s *Server) RADIUSSecret(ctx context.Context, addr net.Addr) ([]byte, error) {
	ip := addrIP(addr)
	n, ok, err := s.nasByIP(ctx, ip)
	if err != nil {
		return nil, err
	}
	if ok {
		return n.sec, n.secErr
	}
	s.warnUnknown(ip)
	return nil, fmt.Errorf("unknown NAS %v", ip)
}

// nasTTL is how long the nas table is cached. Every packet looks up its NAS up to three times, and
// reading the table each time was over half the CPU of a login at 50 NAS. An edit in the admin UI
// takes effect within this time.
const nasTTL = 2 * time.Second

// nasEntry is a nas row with its address parsed and its secret decrypted once per cache fill,
// not on each of the three lookups of every packet.
type nasEntry struct {
	db.Na
	pfx    netip.Prefix // a single address is a full-length prefix; invalid = matches nothing
	sec    []byte       // shared: callers must not modify it
	secErr error
}

// listNAS is ListNAS cached for nasTTL.
func (s *Server) listNAS(ctx context.Context) ([]nasEntry, error) {
	s.nasMu.Lock()
	defer s.nasMu.Unlock()
	if time.Since(s.nasAt) < nasTTL {
		return s.nasRows, nil
	}
	rows, err := s.Q.ListNAS(ctx)
	if err != nil {
		return nil, err
	}
	es := make([]nasEntry, len(rows))
	for i, n := range rows {
		es[i] = nasEntry{Na: n, pfx: parsePrefix(n.Ip)}
		es[i].sec, es[i].secErr = secret.Open(s.Key, n.SecretEnc)
	}
	s.nasRows, s.nasAt = es, time.Now()
	return es, nil
}

// parsePrefix reads a nas ip column: an address or a CIDR.
func parsePrefix(spec string) netip.Prefix {
	if p, err := netip.ParsePrefix(spec); err == nil {
		return p.Masked()
	}
	if a, err := netip.ParseAddr(spec); err == nil {
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen())
	}
	return netip.Prefix{}
}

// forgetNAS drops the listNAS cache and the settings cache.
func (s *Server) forgetNAS() {
	s.nasMu.Lock()
	s.nasAt = time.Time{}
	s.nasMu.Unlock()
	s.store().Invalidate()
}

// nasByIP returns the nas whose ip (single address or CIDR) matches ip.
func (s *Server) nasByIP(ctx context.Context, ip net.IP) (nasEntry, bool, error) {
	rows, err := s.listNAS(ctx)
	if err != nil {
		return nasEntry{}, false, err
	}
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return nasEntry{}, false, nil
	}
	a = a.Unmap()
	for _, n := range rows {
		if n.pfx.IsValid() && n.pfx.Contains(a) {
			return n, true, nil
		}
	}
	return nasEntry{}, false, nil
}

// allowLog reports whether a warning for key may be logged now: at most one per d. Keys are only
// known NAS addresses, so m stays bounded by the nas table.
func (s *Server) allowLog(m *sync.Map, key string, d time.Duration) bool {
	now := s.now()
	if t, ok := m.Load(key); ok && now.Sub(t.(time.Time)) < d {
		return false
	}
	m.Store(key, now)
	return true
}

// unknownSeenMax bounds unknownSeen: spoofed source IPs must not grow it forever.
const unknownSeenMax = 1024

// warnUnknown logs a packet from an unregistered NAS, once per IP per 5 minutes. When unknownSeen
// is full of recent IPs, new IPs are not remembered and all of them share one warning per 5 minutes.
func (s *Server) warnUnknown(ip net.IP) {
	now := s.now()
	key := ip.String()
	s.unknownMu.Lock()
	if t, ok := s.unknownSeen[key]; ok && now.Sub(t) < 5*time.Minute {
		s.unknownMu.Unlock()
		return
	}
	if s.unknownSeen == nil {
		s.unknownSeen = map[string]time.Time{}
	}
	if len(s.unknownSeen) >= unknownSeenMax {
		for k, t := range s.unknownSeen {
			if now.Sub(t) >= 5*time.Minute {
				delete(s.unknownSeen, k)
			}
		}
	}
	if len(s.unknownSeen) < unknownSeenMax {
		s.unknownSeen[key] = now
	} else {
		if now.Sub(s.unknownLast) < 5*time.Minute {
			s.unknownMu.Unlock()
			return
		}
		s.unknownLast = now
	}
	s.unknownMu.Unlock()
	slog.Warn("radius: packet from unknown NAS, add it under Network > NAS", "ip", key)
}

func addrIP(a net.Addr) net.IP {
	if u, ok := a.(*net.UDPAddr); ok {
		return u.IP
	}
	h, _, _ := net.SplitHostPort(a.String())
	return net.ParseIP(h)
}

func matchIP(spec string, ip net.IP) bool {
	if _, n, err := net.ParseCIDR(spec); err == nil {
		return n.Contains(ip)
	}
	return net.ParseIP(spec).Equal(ip)
}
