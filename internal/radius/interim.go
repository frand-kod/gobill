package radius

import (
	"sync"

	"layeh.com/radius/rfc2866"
)

// interim learns each NAS's Interim-Update spacing from consecutive packets of one session, so a NAS
// with a long interim interval does not show its sessions as stale between two updates.
// ponytail: in memory, lost at restart (StaleAfter applies until the second interim of a session);
// last is reset when it outgrows maxInterimSessions, sessions without Stop would otherwise leak.
var interim = struct {
	sync.Mutex
	last  map[string]int64 // nas + "\x00" + session id -> unix time of its last Start/Interim
	every map[string]int64 // nas -> latest observed spacing, seconds
}{last: map[string]int64{}, every: map[string]int64{}}

const maxInterimSessions = 100000

// noteInterim records one accounting packet of a session at now.
func noteInterim(nas, sid string, t rfc2866.AcctStatusType, now int64) {
	k := nas + "\x00" + sid
	interim.Lock()
	defer interim.Unlock()
	if t == rfc2866.AcctStatusType_Value_Stop {
		delete(interim.last, k)
		return
	}
	if prev, ok := interim.last[k]; ok && t == rfc2866.AcctStatusType_Value_InterimUpdate && now-prev >= 10 {
		interim.every[nas] = now - prev // < 10s: a retransmit or a burst, not the interval
	}
	if len(interim.last) >= maxInterimSessions {
		clear(interim.last)
	}
	interim.last[k] = now
}

// StaleFor is how long (seconds) a session on NAS nas may go without an update before it counts as
// stale: two observed interim intervals plus a minute, never less than StaleAfter.
func StaleFor(nas string) int64 {
	interim.Lock()
	defer interim.Unlock()
	return max(StaleAfter, 2*interim.every[nas]+60)
}

// StaleMax is StaleFor of the slowest NAS, for counts over all sessions.
func StaleMax() int64 {
	interim.Lock()
	defer interim.Unlock()
	m := StaleAfter
	for _, e := range interim.every {
		m = max(m, 2*e+60)
	}
	return m
}
