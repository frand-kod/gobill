package core

import (
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// Two interim updates give the rate: the octets since the previous update over the seconds between them.
// Two interim updates give the rate: the octets since the previous update over the seconds between them.
func TestLiveRateFromTwoSamples(t *testing.T) {
	var r liveRate
	r.add(liveSample{at: 100})
	if _, _, ok := r.bps(); ok {
		t.Fatal("one sample gives a rate")
	}
	r.add(liveSample{at: 100, in: 9, out: 9}) // the same interim update again is not a sample
	if _, _, ok := r.bps(); ok {
		t.Fatal("a repeated update counted as a sample")
	}
	// 60 s later: 7.5 MB sent to the user = 1 Mbps down, 375 kB received = 50 kbps up
	r.add(liveSample{at: 160, in: 375000, out: 7500000})
	down, up, ok := r.bps()
	if !ok || down != 1e6 || up != 5e4 {
		t.Fatalf("rate %v %v %v, want 1e6 5e4 true", down, up, ok)
	}
	// counters that go backwards (a reset) give no rate
	r.add(liveSample{at: 220})
	if _, _, ok := r.bps(); ok {
		t.Fatal("a counter reset gave a rate")
	}
}

// The monitor forgets sessions that are no longer open.
func TestLiveMonitorPrunesClosedSessions(t *testing.T) {
	var m LiveMonitor
	m.Refresh([]db.RadiusSession{{ID: 1, UpdatedAt: 100}, {ID: 2, UpdatedAt: 100}})
	m.Refresh([]db.RadiusSession{{ID: 2, UpdatedAt: 100}})
	if len(m.rates) != 1 || m.rates[2] == nil {
		t.Fatalf("rates kept %d sessions, want only 2", len(m.rates))
	}
}
