// Package metrics is a tiny in-process registry: counters and gauges keyed by name and labels,
// plus a 24 h ring of per-minute totals for each name. Stdlib only; Expose writes the Prometheus
// text format. Process-global, reset on restart.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Now is the clock for the per-minute ring; tests replace it.
var Now = time.Now

var started = time.Now()

// Started returns the process start time.
func Started() time.Time { return started }

const ringMinutes = 1440

// help holds the # HELP text of every counter and gauge name (without the gobill_ prefix).
var help = map[string]string{
	"radius_auth_accepted_total":         "RADIUS Access-Accept packets.",
	"radius_auth_rejected_total":         "RADIUS Access-Reject decisions, by reason.",
	"radius_auth_duration_seconds_sum":   "Total seconds spent deciding RADIUS auth requests.",
	"radius_auth_duration_seconds_count": "Number of RADIUS auth decisions.",
	"radius_acct_packets_total":          "RADIUS accounting packets, by status type.",
	"radius_nas_last_packet_timestamp":   "Unix time of the last RADIUS packet from each NAS.",
	"radius_rest_forbidden_total":        "Requests to /radius.php refused by the allow list.",
	"job_runs_total":                     "Background job runs, by job.",
	"notifications_sent_total":           "Notifications sent successfully, by channel.",
	"notifications_failed_total":         "Notifications that failed, by channel.",
	"payment_callbacks_total":            "Tripay callbacks, by result.",
	"payment_callback_last_timestamp":    "Unix time of the last Tripay callback.",
	"login_failures_total":               "Failed logins, by scope (admin, portal, 2fa).",
}

type entry struct {
	name string
	kv   []string // label name, value, name, value...
	val  float64
}

type ring struct {
	min [ringMinutes]int64
	val [ringMinutes]float64
}

var (
	mu       sync.Mutex
	counters = map[string]*entry{}
	gauges   = map[string]*entry{}
	rings    = map[string]*ring{}
)

func key(name string, kv []string) string { return name + "\x00" + strings.Join(kv, "\x00") }

// Add adds delta to the counter name{kv...} and to the per-minute ring of name.
func Add(name string, delta float64, kv ...string) {
	mu.Lock()
	defer mu.Unlock()
	k := key(name, kv)
	e := counters[k]
	if e == nil {
		e = &entry{name: name, kv: kv}
		counters[k] = e
	}
	e.val += delta
	r := rings[name]
	if r == nil {
		r = &ring{}
		rings[name] = r
	}
	m := Now().Unix() / 60
	i := m % ringMinutes
	if r.min[i] != m {
		r.min[i], r.val[i] = m, 0
	}
	r.val[i] += delta
}

// Inc adds 1 to the counter name{kv...}.
func Inc(name string, kv ...string) { Add(name, 1, kv...) }

// Set sets the gauge name{kv...}.
func Set(name string, v float64, kv ...string) {
	mu.Lock()
	defer mu.Unlock()
	k := key(name, kv)
	e := gauges[k]
	if e == nil {
		e = &entry{name: name, kv: kv}
		gauges[k] = e
	}
	e.val = v
}

// Value returns the counter or gauge name{kv...}, 0 when never set.
func Value(name string, kv ...string) float64 {
	mu.Lock()
	defer mu.Unlock()
	k := key(name, kv)
	if e := counters[k]; e != nil {
		return e.val
	}
	if e := gauges[k]; e != nil {
		return e.val
	}
	return 0
}

// Each calls fn for every series of name (counter or gauge) with its label pairs and value.
func Each(name string, fn func(kv []string, v float64)) {
	mu.Lock()
	defer mu.Unlock()
	for _, m := range []map[string]*entry{counters, gauges} {
		for _, e := range m {
			if e.name == name {
				fn(e.kv, e.val)
			}
		}
	}
}

// Recent returns the total of name over the last n minutes (n <= 1440), current minute included.
func Recent(name string, n int) float64 {
	mu.Lock()
	defer mu.Unlock()
	r := rings[name]
	if r == nil {
		return 0
	}
	now := Now().Unix() / 60
	var sum float64
	for m := now - int64(n) + 1; m <= now; m++ {
		if i := m % ringMinutes; r.min[i] == m {
			sum += r.val[i]
		}
	}
	return sum
}

// Series returns the per-minute totals of name for the last 1440 minutes, oldest first.
func Series(name string) []float64 {
	mu.Lock()
	defer mu.Unlock()
	out := make([]float64, ringMinutes)
	r := rings[name]
	if r == nil {
		return out
	}
	now := Now().Unix() / 60
	for j := range out {
		m := now - ringMinutes + 1 + int64(j)
		if i := m % ringMinutes; r.min[i] == m {
			out[j] = r.val[i]
		}
	}
	return out
}

// Label renders label pairs as the Prometheus label list: `k="v",k2="v2"`.
func Label(kv ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		v := strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(kv[i+1])
		fmt.Fprintf(&b, `%s="%s"`, kv[i], v)
	}
	return b.String()
}

// Sample is one series of a family: its rendered labels and value.
type Sample struct {
	Labels string
	Value  float64
}

// Family is one metric with its help and type ("counter" or "gauge").
type Family struct {
	Name, Type, Help string
	Samples          []Sample
}

// Families returns every counter and gauge, sorted by name then labels.
func Families() []Family {
	mu.Lock()
	defer mu.Unlock()
	byName := map[string]*Family{}
	add := func(m map[string]*entry, typ string) {
		for _, e := range m {
			f := byName[e.name]
			if f == nil {
				h := help[e.name]
				if h == "" {
					h = e.name
				}
				f = &Family{Name: e.name, Type: typ, Help: h}
				byName[e.name] = f
			}
			f.Samples = append(f.Samples, Sample{Labels: Label(e.kv...), Value: e.val})
		}
	}
	add(counters, "counter")
	add(gauges, "gauge")
	out := make([]Family, 0, len(byName))
	for _, f := range byName {
		sort.Slice(f.Samples, func(i, j int) bool { return f.Samples[i].Labels < f.Samples[j].Labels })
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Expose writes families in the Prometheus text format, with the gobill_ prefix.
func Expose(w io.Writer, fams []Family) {
	for _, f := range fams {
		name := "gobill_" + f.Name
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, f.Help, name, f.Type)
		for _, s := range f.Samples {
			if s.Labels == "" {
				fmt.Fprintf(w, "%s %g\n", name, s.Value)
			} else {
				fmt.Fprintf(w, "%s{%s} %g\n", name, s.Labels, s.Value)
			}
		}
	}
}

// Reset clears everything. For tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	counters, gauges, rings = map[string]*entry{}, map[string]*entry{}, map[string]*ring{}
}
