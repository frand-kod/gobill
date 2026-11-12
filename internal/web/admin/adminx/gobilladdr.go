package adminx

import (
	"net"

	"github.com/frand-kod/gobill/internal/isolir"
)

// GobillAddr is the LAN address and port gobill listens on (GOBILL_HTTP). A host that is not an IP,
// such as empty or 0.0.0.0, gives the first LAN IPv4 address of this host.
func (s *Base) GobillAddr() (host, port string) {
	addr := s.ListenAddr
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", "8080"
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = isolir.LanIPv4()
	}
	return host, port
}
