package network

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestWGAddress(t *testing.T) {
	local := []net.IP{net.ParseIP("203.0.113.5")}
	look := func(m map[string]string) func(string) ([]net.IP, error) {
		return func(h string) ([]net.IP, error) {
			if ip, ok := m[h]; ok {
				return []net.IP{net.ParseIP(ip)}, nil
			}
			return nil, errors.New("no such host")
		}
	}
	l := look(map[string]string{"here.test": "203.0.113.5", "nat.test": "198.51.100.7", "lan.test": "192.168.1.5"})
	for _, c := range []struct {
		host string
		ok   bool
		sub  string
	}{
		{"", false, "No public endpoint"},
		{"gone.test", false, "does not resolve"},
		{"lan.test", false, "private address"},
		{"here.test", true, "of this server"},
		{"nat.test", true, "not one of this server"},
	} {
		msg, ok := wgAddress(c.host, l, local)
		if ok != c.ok || !strings.Contains(msg, c.sub) {
			t.Errorf("%q: ok=%v %q", c.host, ok, msg)
		}
	}
}
