package core

import (
	"sync"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
)

// liveSample is the octet counters of a session at one interim update.
type liveSample struct{ at, in, out int64 }

// liveRate keeps the last two interim updates of an open session. Its rate is the change between them.
type liveRate struct {
	n          int // samples seen, at most 2
	prev, last liveSample
}

// add records an interim update. An update that is not newer than the last one is not a new sample.
func (r *liveRate) add(s liveSample) {
	switch {
	case r.n == 0:
		r.last, r.n = s, 1
	case s.at > r.last.at:
		r.prev, r.last, r.n = r.last, s, 2
	}
}

// bps is the download and upload rate in bits per second between the last two updates. ok is false
// until two updates exist, or when the counters went backwards.
func (r *liveRate) bps() (down, up float64, ok bool) {
	if r.n < 2 {
		return 0, 0, false
	}
	dt := r.last.at - r.prev.at
	dOut, dIn := r.last.out-r.prev.out, r.last.in-r.prev.in
	if dt <= 0 || dOut < 0 || dIn < 0 {
		return 0, 0, false
	}
	return float64(dOut) * 8 / float64(dt), float64(dIn) * 8 / float64(dt), true
}

// LiveMonitor is the in-memory state of the live view: the rate samples of the open sessions. The zero
// value is ready to use.
type LiveMonitor struct {
	mu    sync.Mutex
	rates map[int64]*liveRate // open sessions only: refresh drops the rest
	swept int64               // when observe last swept stale rate entries
}

// Refresh records the counters of the open sessions and forgets the sessions that are no longer open.
func (m *LiveMonitor) Refresh(open []db.RadiusSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[int64]*liveRate, len(open))
	for _, x := range open {
		r := m.rates[x.ID]
		if r == nil {
			r = &liveRate{}
		}
		r.add(liveSample{x.UpdatedAt, x.InputOctets, x.OutputOctets})
		keep[x.ID] = r
	}
	m.rates = keep
}

// Observe records the counters of one open session, for the customer's live card. The other sessions keep
// their samples. Entries of sessions silent for radius.StaleMax() are swept at most once a minute; the admin
// Refresh drops the rest.
func (m *LiveMonitor) Observe(x db.RadiusSession, now int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rates == nil {
		m.rates = map[int64]*liveRate{}
	}
	if now-m.swept >= 60 {
		m.swept = now
		for id, r := range m.rates {
			if now-r.last.at > radius.StaleMax() {
				delete(m.rates, id)
			}
		}
	}
	r := m.rates[x.ID]
	if r == nil {
		r = &liveRate{}
		m.rates[x.ID] = r
	}
	r.add(liveSample{x.UpdatedAt, x.InputOctets, x.OutputOctets})
}

// Rate is the current rate of an open session, see liveRate.bps.
func (m *LiveMonitor) Rate(id int64) (down, up float64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rates[id]
	if r == nil {
		return 0, 0, false
	}
	return r.bps()
}
