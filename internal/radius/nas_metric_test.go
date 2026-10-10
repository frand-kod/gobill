package radius

import (
	"net"
	"testing"
	"time"

	"layeh.com/radius"

	"github.com/frand-kod/gobill/internal/metrics"
)

// Only validated NAS sources create radius_nas_last_packet_timestamp series.
func TestUnknownUDPSourceCreatesNoNASSeries(t *testing.T) {
	metrics.Reset()
	e := setup(t)
	metrics.Now = func() time.Time { return e.now } // the 24 h sweep uses the same clock as the packet times
	t.Cleanup(func() { metrics.Now = time.Now; metrics.Reset() })
	stranger := &rec{}
	e.s.HandleAuth(stranger, &radius.Request{Packet: pap("alice", "pw"), RemoteAddr: &net.UDPAddr{IP: net.ParseIP("192.168.9.9"), Port: 5000}})
	if n := countNASSeries(); n != 0 {
		t.Fatalf("unknown source created %d NAS series", n)
	}
	e.auth(t, pap("alice", "pw"))
	if n := countNASSeries(); n != 1 {
		t.Fatalf("known NAS: %d NAS series, want 1", n)
	}
}

func countNASSeries() int {
	n := 0
	metrics.Each("radius_nas_last_packet_timestamp", func([]string, float64) { n++ })
	return n
}
