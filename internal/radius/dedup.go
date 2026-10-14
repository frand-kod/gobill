package radius

import (
	"fmt"
	"sync"
	"time"

	"layeh.com/radius"
)

const (
	dupTTL = 30 * time.Second
	// dupMax bounds memory (a few hundred bytes per entry). 1000 requests/s for dupTTL fit; past it
	// new requests are still processed, only not deduplicated.
	dupMax = 30000
)

// dupCache implements RFC 5080 section 2.2.2: a retransmitted request (same NAS address and
// port, Identifier and Request Authenticator) gets the cached response again instead of being
// processed twice (double accounting, double voucher redemption).
// Entries sit in two generations swapped every dupTTL, so expiry costs nothing per packet: the old
// map is dropped whole once everything in it has expired. A full scan per packet, as before, held
// the lock for milliseconds once the map was full, and the map was full from about 333 requests/s.
// ponytail: in-memory, per process; a restart forgets. Use a shared store if several instances answer one NAS.
type dupCache struct {
	mu       sync.Mutex
	cur, old map[string]dupEntry
	rotated  time.Time // when cur was started
}

type dupEntry struct {
	resp *radius.Packet // nil while the first copy is still being processed
	exp  time.Time
}

// capture remembers the response a handler writes. Encode is deterministic for a given request
// authenticator, so re-writing the same packet re-sends identical bytes.
type capture struct {
	radius.ResponseWriter
	p *radius.Packet
}

func (c *capture) Write(p *radius.Packet) error {
	c.p = p
	return c.ResponseWriter.Write(p)
}

// get returns the live entry for key. Callers hold mu.
func (d *dupCache) get(now time.Time, key string) (dupEntry, bool) {
	e, ok := d.cur[key]
	if !ok {
		e, ok = d.old[key]
	}
	return e, ok && now.Before(e.exp)
}

func (d *dupCache) serve(now time.Time, w radius.ResponseWriter, r *radius.Request, h func(radius.ResponseWriter, *radius.Request)) {
	key := fmt.Sprintf("%v|%d|%x", r.RemoteAddr, r.Identifier, r.Authenticator)
	d.mu.Lock()
	if e, ok := d.get(now, key); ok {
		d.mu.Unlock()
		if e.resp != nil { // nil = first copy still in flight: drop, the NAS will retry
			w.Write(e.resp)
		}
		return
	}
	// everything in old was added before rotated, so it has expired once rotated+dupTTL has passed
	if d.cur == nil || now.Sub(d.rotated) >= dupTTL || now.Before(d.rotated) {
		d.old, d.cur, d.rotated = d.cur, map[string]dupEntry{}, now
	}
	tracked := len(d.cur)+len(d.old) < dupMax
	if tracked {
		d.cur[key] = dupEntry{exp: now.Add(dupTTL)}
	}
	d.mu.Unlock()

	c := &capture{ResponseWriter: w}
	h(c, r)
	if !tracked {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.old, key) // rotated while the handler ran: the entry lives on in cur
	if c.p == nil {    // no answer (dropped or failed): let a retry be processed
		delete(d.cur, key)
		return
	}
	d.cur[key] = dupEntry{resp: c.p, exp: now.Add(dupTTL)}
}
