package radius

import (
	"net"
	"testing"

	"layeh.com/radius"

	"github.com/frand-kod/gobill/internal/db"
)

// BenchmarkHandleAuth is one valid PAP login through the whole UDP handler, the hot path when every
// router reconnects at once. bench/radius measures the same path over the network.
func BenchmarkHandleAuth(b *testing.B) {
	e := setup(b)
	e.s.Q = db.NewCached(e.conn) // as in main.go
	from := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5000}
	b.ReportAllocs()
	for b.Loop() {
		rc := &rec{}
		e.s.handleAuth(rc, &radius.Request{Packet: pap("alice", "pw"), RemoteAddr: from})
		if rc.p == nil || rc.p.Code != radius.CodeAccessAccept {
			b.Fatal("not accepted")
		}
	}
}
