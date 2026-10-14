// Command radius is a RADIUS load generator for gobill: it seeds a database and then drives the UDP
// auth/acct listeners open-loop (requests leave on schedule whether or not earlier ones answered, so a
// slow server shows up as latency instead of hiding behind a slower client). See bench/README.md.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2869"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

var (
	nasSecret = []byte("bench-secret")

	dbPath   = flag.String("db", "", "seed: gobill database to create (the server must not be running)")
	users    = flag.Int("users", 5000, "customers user0..userN-1, password pw-userN")
	nases    = flag.Int("nas", 1, "NAS rows; NAS i sends from 127.10.x.y")
	addr     = flag.String("addr", "127.0.0.1:1812", "auth address; accounting is port+1")
	scenario = flag.String("scenario", "ramp", "ramp | spike | mixed")
	timeout  = flag.Duration("timeout", 3*time.Second, "NAS reply timeout (MikroTik default 3s)")
	retry    = flag.Duration("retry", 0, "NAS retransmit interval inside the timeout (0 = none)")
	slo      = flag.Duration("slo", time.Second, "ramp: a step fails when p99 exceeds this")
	rampAcct = flag.Bool("acct", false, "ramp: send Accounting interim updates instead of logins")
	start    = flag.Int("start", 200, "ramp: first rate, requests/s")
	step     = flag.Int("step", 200, "ramp: rate added per step")
	stepDur  = flag.Duration("stepdur", 10*time.Second, "ramp: length of a step")
	maxRate  = flag.Int("max", 100000, "ramp: stop at this rate")
	window   = flag.Duration("window", 5*time.Second, "spike: every user logs in within this window")
	rate     = flag.Int("rate", 500, "mixed: legitimate logins/s")
	attack   = flag.Int("attack", 2000, "mixed: hostile packets/s (wrong password, unknown user, wrong secret)")
	interim  = flag.Duration("interim", 5*time.Minute, "mixed: NAS interim-update interval")
	dur      = flag.Duration("dur", time.Minute, "mixed: duration")
	pid      = flag.Int("pid", 0, "server pid, to report its RSS and threads")
)

func main() {
	flag.Parse()
	if *dbPath != "" {
		if err := seed(); err != nil {
			log.Fatal(err)
		}
		return
	}
	switch *scenario {
	case "ramp":
		ramp()
	case "spike":
		spike()
	case "mixed":
		mixed()
	default:
		log.Fatalf("unknown scenario %q", *scenario)
	}
}

// ---- seed ----

func seed() error {
	ctx := context.Background()
	conn, err := db.Open(*dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		return err
	}
	key, _, err := secret.LoadKey(os.Getenv("GOBILL_SECRET_KEY"), *dbPath+".key")
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := db.New(tx)
	sealed, err := secret.Seal(key, nasSecret)
	if err != nil {
		return err
	}
	for i := range *nases {
		if _, err := q.CreateNAS(ctx, db.CreateNASParams{Name: "nas" + strconv.Itoa(i), Ip: nasIP(i).String(), SecretEnc: sealed}); err != nil {
			return err
		}
	}
	bw, err := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "10M", RateDown: 10, RateDownUnit: "Mbps", RateUp: 10, RateUpUnit: "Mbps"})
	if err != nil {
		return err
	}
	plan, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: "Hotspot 30 Hari", Type: "Hotspot", Billing: "prepaid", Validity: 30, ValidityUnit: "Days",
		BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}, Device: "Radius", Enabled: 1, SharedUsers: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	for i := range *users {
		u := "user" + strconv.Itoa(i)
		pw, err := secret.Seal(key, []byte("pw-"+u)) // not the username: that is a voucher login
		if err != nil {
			return err
		}
		c, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: u, PasswordHash: "x", Fullname: u, ServiceType: "Hotspot", SecretEnc: pw, Status: "Active"})
		if err != nil {
			return err
		}
		if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: plan.ID, Type: "Hotspot",
			StartedAt: now, ExpiresAt: now + 30*86400}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Printf("seeded %d users, %d NAS into %s\n", *users, *nases, *dbPath)
	return nil
}

// nasIP is the loopback source address of NAS i; Linux answers on all of 127.0.0.0/8.
func nasIP(i int) net.IP { return net.IPv4(127, 10, byte(i>>8), byte(i&0xff)+1) }

// ---- packets ----

type kind int

const (
	okAuth kind = iota
	badPass
	unknownUser
	badSecret
	acctStart
	acctInterim
	acctStop
	nKinds
)

var kindName = [...]string{"auth-ok", "bad-pass", "unknown-user", "bad-secret", "acct-start", "acct-interim", "acct-stop"}

// want is the reply that counts as correct; badSecret wants silence (the server must drop it).
func (k kind) want() radius.Code {
	switch k {
	case okAuth:
		return radius.CodeAccessAccept
	case badPass, unknownUser:
		return radius.CodeAccessReject
	case badSecret:
		return 0
	}
	return radius.CodeAccountingResponse
}

func packet(k kind, u int) (*radius.Packet, string) {
	sec := nasSecret
	if k == badSecret {
		sec = []byte("wrong-secret")
	}
	name := "user" + strconv.Itoa(u)
	if k == unknownUser {
		name = "ghost" + strconv.Itoa(u)
	}
	dst := *addr
	var p *radius.Packet
	if k <= badSecret {
		p = radius.New(radius.CodeAccessRequest, sec)
		rfc2865.UserName_SetString(p, name)
		pw := "pw-" + name
		if k == badPass {
			pw = "wrong"
		}
		rfc2865.UserPassword_SetString(p, pw)
		rfc2865.CallingStationID_SetString(p, mac(u))
		addMA(p)
	} else {
		p = radius.New(radius.CodeAccountingRequest, sec)
		rfc2865.UserName_SetString(p, name)
		rfc2866.AcctSessionID_SetString(p, "s"+strconv.Itoa(u))
		rfc2865.CallingStationID_SetString(p, mac(u))
		rfc2865.FramedIPAddress_Set(p, net.IPv4(10, byte(u>>16), byte(u>>8), byte(u)))
		st := map[kind]rfc2866.AcctStatusType{acctStart: rfc2866.AcctStatusType_Value_Start,
			acctInterim: rfc2866.AcctStatusType_Value_InterimUpdate, acctStop: rfc2866.AcctStatusType_Value_Stop}[k]
		rfc2866.AcctStatusType_Set(p, st)
		if k != acctStart {
			rfc2866.AcctSessionTime_Set(p, 300)
			rfc2866.AcctInputOctets_Set(p, rfc2866.AcctInputOctets(rand.Uint32()))
			rfc2866.AcctOutputOctets_Set(p, rfc2866.AcctOutputOctets(rand.Uint32()))
		}
		dst = acctAddr()
	}
	return p, dst
}

func mac(u int) string {
	return fmt.Sprintf("02:00:%02X:%02X:%02X:%02X", byte(u>>24), byte(u>>16), byte(u>>8), byte(u))
}

// addMA adds a valid Message-Authenticator, as RouterOS 7.15+ does (BlastRADIUS).
func addMA(p *radius.Packet) {
	rfc2869.MessageAuthenticator_Set(p, make([]byte, 16))
	b, err := p.MarshalBinary()
	if err != nil {
		panic(err)
	}
	m := hmac.New(md5.New, p.Secret)
	m.Write(b)
	rfc2869.MessageAuthenticator_Set(p, m.Sum(nil))
}

func acctAddr() string {
	h, port, _ := net.SplitHostPort(*addr)
	n, _ := strconv.Atoi(port)
	return net.JoinHostPort(h, strconv.Itoa(n+1))
}

// ---- recording ----

type stats struct {
	mu       sync.Mutex
	lat      [nKinds][]time.Duration
	ok, bad  [nKinds]int64 // right / wrong reply code
	lost     [nKinds]int64 // no reply within the timeout
	sendErr  [nKinds]int64
	inflight atomic.Int64
	peak     atomic.Int64
}

func (s *stats) fire(k kind, u int) {
	n := s.inflight.Add(1)
	for p := s.peak.Load(); n > p && !s.peak.CompareAndSwap(p, n); p = s.peak.Load() {
	}
	go func() {
		defer s.inflight.Add(-1)
		p, dst := packet(k, u)
		cl := &radius.Client{Retry: *retry, Dialer: net.Dialer{LocalAddr: &net.UDPAddr{IP: nasIP(u % *nases)}}}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		t0 := time.Now()
		r, err := cl.Exchange(ctx, p, dst)
		d := time.Since(t0)
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case err != nil && ctx.Err() != nil:
			if k.want() == 0 {
				s.ok[k]++ // dropped, as it should be
			} else {
				s.lost[k]++
			}
		case err != nil:
			s.sendErr[k]++
		case r.Code == k.want():
			s.ok[k]++
			s.lat[k] = append(s.lat[k], d)
		default:
			s.bad[k]++
		}
	}()
}

func (s *stats) wait() {
	for s.inflight.Load() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
}

func pct(l []time.Duration, p float64) time.Duration {
	if len(l) == 0 {
		return 0
	}
	slices.Sort(l)
	return l[min(len(l)-1, int(float64(len(l))*p))]
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f", float64(d.Microseconds())/1000) }

func (s *stats) table(title string) {
	fmt.Printf("\n%s\n\n| Packet | Sent | Correct | Wrong reply | No reply | Send error | p50 ms | p99 ms | max ms |\n|---|---|---|---|---|---|---|---|---|\n", title)
	for k := range nKinds {
		n := s.ok[k] + s.bad[k] + s.lost[k] + s.sendErr[k]
		if n == 0 {
			continue
		}
		l := s.lat[k]
		fmt.Printf("| %s | %d | %d | %d | %d | %d | %s | %s | %s |\n", kindName[k], n, s.ok[k], s.bad[k], s.lost[k], s.sendErr[k],
			ms(pct(l, .5)), ms(pct(l, .99)), ms(pct(l, 1)))
	}
	fmt.Printf("\nPeak in flight: %d. %s\n", s.peak.Load(), server())
}

// server reports the server's resident memory and thread count from /proc.
func server() string {
	if *pid == 0 {
		return ""
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", *pid))
	if err != nil {
		return "server gone: " + err.Error()
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS") || strings.HasPrefix(l, "VmHWM") || strings.HasPrefix(l, "Threads") {
			out = append(out, strings.Join(strings.Fields(l), " "))
		}
	}
	return "Server: " + strings.Join(out, ", ")
}

// pace calls f(i) n times spread evenly over d, open-loop.
func pace(n int, d time.Duration, f func(i int)) {
	t0 := time.Now()
	for i := range n {
		if wait := time.Until(t0.Add(time.Duration(int64(d) * int64(i) / int64(n)))); wait > 0 {
			time.Sleep(wait)
		}
		f(i)
	}
}

// ---- scenarios ----

// ramp raises the valid-login (or, with -acct, interim-update) rate step by step until timeouts pass 0.1% or p99 passes -slo.
func ramp() {
	fmt.Printf("| Rate/s | Sent | Correct | No reply | p50 ms | p99 ms | max ms | %s |\n|---|---|---|---|---|---|---|---|\n", "Server")
	last := 0
	for r := *start; r <= *maxRate; r += *step {
		s := &stats{}
		n := int(float64(r) * stepDur.Seconds())
		k := okAuth
		if *rampAcct {
			k = acctInterim
		}
		pace(n, *stepDur, func(i int) { s.fire(k, rand.IntN(*users)) })
		s.wait()
		l := s.lat[k]
		lost := s.lost[k] + s.bad[k] + s.sendErr[k]
		p99 := pct(l, .99)
		fmt.Printf("| %d | %d | %d | %d | %s | %s | %s | %s |\n", r, n, s.ok[k], lost, ms(pct(l, .5)), ms(p99), ms(pct(l, 1)), server())
		if float64(lost) > float64(n)*0.001 || p99 > *slo {
			fmt.Printf("\nBreakpoint: %d/s fails (no reply %d, p99 %s ms). Last good rate: %d/s.\n", r, lost, ms(p99), last)
			return
		}
		last = r
	}
	fmt.Printf("\nNo breakpoint up to %d/s.\n", *maxRate)
}

// spike: every user logs in and starts accounting within -window, as after a power cut when all
// routers come back at once. Then all sessions stop.
func spike() {
	s := &stats{}
	t0 := time.Now()
	pace(*users, *window, func(i int) { s.fire(okAuth, i) })
	s.wait()
	pace(*users, *window, func(i int) { s.fire(acctStart, i) })
	s.wait()
	took := time.Since(t0)
	s.table(fmt.Sprintf("Spike: %d logins then %d Accounting-Start, each in %s (%d/s); finished in %s",
		*users, *users, *window, int(float64(*users)/window.Seconds()), took.Round(time.Millisecond)))
	s = &stats{}
	pace(*users, *window, func(i int) { s.fire(acctStop, i) })
	s.wait()
	s.table("Then every session stops")
}

// mixed: the steady state of a busy network (interim updates for every user, login churn) while an
// attacker floods wrong passwords, unknown users and packets with a wrong secret. The question is
// whether legitimate traffic still gets through.
func mixed() {
	s := &stats{}
	var wg sync.WaitGroup
	run := func(perSec int, f func(i int)) {
		if perSec <= 0 {
			return
		}
		wg.Add(1)
		go func() { defer wg.Done(); pace(int(float64(perSec)*dur.Seconds()), *dur, f) }()
	}
	run(int(float64(*users)/interim.Seconds()), func(i int) { s.fire(acctInterim, i%*users) })
	run(*rate, func(i int) { s.fire(okAuth, rand.IntN(*users)) })
	hostile := []kind{badPass, unknownUser, badSecret}
	run(*attack, func(i int) { s.fire(hostile[i%3], rand.IntN(*users)) })
	wg.Wait()
	s.wait()
	s.table(fmt.Sprintf("Mixed for %s: %d users interim every %s, %d logins/s, %d hostile packets/s", *dur, *users, *interim, *rate, *attack))
}
