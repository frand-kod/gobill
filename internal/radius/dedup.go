package radius

import (
	"fmt"
	"sync"
	"time"

	"layeh.com/radius"
)

const (
	dupTTL = 30 * time.Second
	dupMax = 10000
)

// dupCache implements RFC 5080 section 2.2.2: a retransmitted request (same NAS address and
// port, Identifier and Request Authenticator) gets the cached response again instead of being
// processed twice (double accounting, double voucher redemption).
// ponytail: in-memory, per process; a restart forgets, and at dupMax entries new ones are not
// cached (still processed). Use a shared store if several instances answer one NAS.
type dupCache struct {
	mu sync.Mutex
	m  map[string]dupEntry
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

func (d *dupCache) serve(now time.Time, w radius.ResponseWriter, r *radius.Request, h func(radius.ResponseWriter, *radius.Request)) {
	key := fmt.Sprintf("%v|%d|%x", r.RemoteAddr, r.Identifier, r.Authenticator)
	d.mu.Lock()
	if e, ok := d.m[key]; ok && now.Before(e.exp) {
		d.mu.Unlock()
		if e.resp != nil { // nil = first copy still in flight: drop, the NAS will retry
			w.Write(e.resp)
		}
		return
	}
	if d.m == nil {
		d.m = map[string]dupEntry{}
	}
	if len(d.m) >= dupMax {
		for k, e := range d.m {
			if !now.Before(e.exp) {
				delete(d.m, k)
			}
		}
	}
	tracked := len(d.m) < dupMax
	if tracked {
		d.m[key] = dupEntry{exp: now.Add(dupTTL)}
	}
	d.mu.Unlock()

	c := &capture{ResponseWriter: w}
	h(c, r)
	if !tracked {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if c.p == nil { // no answer (dropped or failed): let a retry be processed
		delete(d.m, key)
		return
	}
	d.m[key] = dupEntry{resp: c.p, exp: now.Add(dupTTL)}
}
