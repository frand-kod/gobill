// Package radius is the built-in RADIUS auth (rlm_rest replacement) and accounting server.
// Behaviour follows phpnuxbill radius.php.
package radius

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
	"layeh.com/radius"
	"layeh.com/radius/rfc2759"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2869"
	"layeh.com/radius/vendors/microsoft"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/secret"
)

// StaleAfter is how long (seconds) an open session may go without an update before it no longer
// counts toward shared_users and shows as stale. The default is 2x a 5-minute interim update;
// GOBILL_STALE_AFTER overrides it at startup (see cmd/gobill).
var StaleAfter int64 = 600

type Server struct {
	Q        *db.Queries
	DB       *sql.DB // set: accounting writes are group-committed (acctbatch.go); nil: written one by one
	Key      []byte
	Trusted  func() bool      // clock guard; false = do not reject for expiry
	AuthAddr string           // e.g. ":1812"
	AcctAddr string           // e.g. ":1813"
	Now      func() time.Time // default time.Now
	// Redeem activates an unused voucher for a customer (billing.Service.RedeemVoucher).
	// nil disables hotspot voucher login.
	Redeem func(ctx context.Context, code string, customerID int64) error
	// Start begins a start_on_first_login subscription at the customer's first login (billing.StartPending).
	Start func(ctx context.Context, customerID int64) error
	// Kick drops one session on its NAS when shared_users_policy is kick_oldest. nil = Disconnect-Request (CoA).
	Kick func(ctx context.Context, sess db.RadiusSession) error

	userFails       failLimiter // failed password checks per username
	dups            dupCache    // answered requests, for retransmits
	unknownMu       sync.Mutex
	unknownSeen     map[string]time.Time // ip -> last unknown-NAS warning
	unknownLast     time.Time            // last warning for any IP while unknownSeen is full
	maWarned        sync.Map             // ip string -> time.Time of last Message-Authenticator warning
	badSecretWarned sync.Map             // ip string -> time.Time of last bad-secret warning
	busy            atomic.Int64         // packets being handled, see shed
	acctOnce        sync.Once            // starts acctWriter
	acctQ           chan acctJob         // accounting writes waiting for the next commit
	nasMu           sync.Mutex
	nasRows         []nasEntry        // listNAS cache
	nasAt           time.Time         // when nasRows was read; zero = never
	st              map[string]string // settings cache, under nasMu
	stAt            time.Time
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// RADIUSSecret finds the NAS by source IP (exact or CIDR) and returns its secret.
// An error makes the library drop the packet.
func (s *Server) RADIUSSecret(ctx context.Context, addr net.Addr) ([]byte, error) {
	ip := addrIP(addr)
	n, ok, err := s.nasByIP(ctx, ip)
	if err != nil {
		return nil, err
	}
	if ok {
		return n.sec, n.secErr
	}
	s.warnUnknown(ip)
	return nil, fmt.Errorf("unknown NAS %v", ip)
}

// nasTTL is how long the nas table is cached. Every packet looks up its NAS up to three times, and
// reading the table each time was over half the CPU of a login at 50 NAS. An edit in the admin UI
// takes effect within this time.
const nasTTL = 2 * time.Second

// nasEntry is a nas row with its address parsed and its secret decrypted once per cache fill,
// not on each of the three lookups of every packet.
type nasEntry struct {
	db.Na
	pfx    netip.Prefix // a single address is a full-length prefix; invalid = matches nothing
	sec    []byte       // shared: callers must not modify it
	secErr error
}

// listNAS is ListNAS cached for nasTTL.
func (s *Server) listNAS(ctx context.Context) ([]nasEntry, error) {
	s.nasMu.Lock()
	defer s.nasMu.Unlock()
	if time.Since(s.nasAt) < nasTTL {
		return s.nasRows, nil
	}
	rows, err := s.Q.ListNAS(ctx)
	if err != nil {
		return nil, err
	}
	es := make([]nasEntry, len(rows))
	for i, n := range rows {
		es[i] = nasEntry{Na: n, pfx: parsePrefix(n.Ip)}
		es[i].sec, es[i].secErr = secret.Open(s.Key, n.SecretEnc)
	}
	s.nasRows, s.nasAt = es, time.Now()
	return es, nil
}

// parsePrefix reads a nas ip column: an address or a CIDR.
func parsePrefix(spec string) netip.Prefix {
	if p, err := netip.ParsePrefix(spec); err == nil {
		return p.Masked()
	}
	if a, err := netip.ParseAddr(spec); err == nil {
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen())
	}
	return netip.Prefix{}
}

// forgetNAS drops the listNAS cache.
func (s *Server) forgetNAS() {
	s.nasMu.Lock()
	s.nasAt, s.stAt = time.Time{}, time.Time{}
	s.nasMu.Unlock()
}

// nasByIP returns the nas whose ip (single address or CIDR) matches ip.
func (s *Server) nasByIP(ctx context.Context, ip net.IP) (nasEntry, bool, error) {
	rows, err := s.listNAS(ctx)
	if err != nil {
		return nasEntry{}, false, err
	}
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return nasEntry{}, false, nil
	}
	a = a.Unmap()
	for _, n := range rows {
		if n.pfx.IsValid() && n.pfx.Contains(a) {
			return n, true, nil
		}
	}
	return nasEntry{}, false, nil
}

// allowLog reports whether a warning for key may be logged now: at most one per d. Keys are only
// known NAS addresses, so m stays bounded by the nas table.
func (s *Server) allowLog(m *sync.Map, key string, d time.Duration) bool {
	now := s.now()
	if t, ok := m.Load(key); ok && now.Sub(t.(time.Time)) < d {
		return false
	}
	m.Store(key, now)
	return true
}

// unknownSeenMax bounds unknownSeen: spoofed source IPs must not grow it forever.
const unknownSeenMax = 1024

// warnUnknown logs a packet from an unregistered NAS, once per IP per 5 minutes. When unknownSeen
// is full of recent IPs, new IPs are not remembered and all of them share one warning per 5 minutes.
func (s *Server) warnUnknown(ip net.IP) {
	now := s.now()
	key := ip.String()
	s.unknownMu.Lock()
	if t, ok := s.unknownSeen[key]; ok && now.Sub(t) < 5*time.Minute {
		s.unknownMu.Unlock()
		return
	}
	if s.unknownSeen == nil {
		s.unknownSeen = map[string]time.Time{}
	}
	if len(s.unknownSeen) >= unknownSeenMax {
		for k, t := range s.unknownSeen {
			if now.Sub(t) >= 5*time.Minute {
				delete(s.unknownSeen, k)
			}
		}
	}
	if len(s.unknownSeen) < unknownSeenMax {
		s.unknownSeen[key] = now
	} else {
		if now.Sub(s.unknownLast) < 5*time.Minute {
			s.unknownMu.Unlock()
			return
		}
		s.unknownLast = now
	}
	s.unknownMu.Unlock()
	slog.Warn("radius: packet from unknown NAS, add it under Network > NAS", "ip", key)
}

func addrIP(a net.Addr) net.IP {
	if u, ok := a.(*net.UDPAddr); ok {
		return u.IP
	}
	h, _, _ := net.SplitHostPort(a.String())
	return net.ParseIP(h)
}

func matchIP(spec string, ip net.IP) bool {
	if _, n, err := net.ParseCIDR(spec); err == nil {
		return n.Contains(ip)
	}
	return net.ParseIP(spec).Equal(ip)
}

// ListenAndServe runs both servers until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	auth := &radius.PacketServer{Addr: s.AuthAddr, SecretSource: s, Handler: radius.HandlerFunc(s.HandleAuth)}
	acct := &radius.PacketServer{Addr: s.AcctAddr, SecretSource: s, Handler: radius.HandlerFunc(s.HandleAcct)}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, ps := range []*radius.PacketServer{auth, acct} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.serve(ps); err != nil && !errors.Is(err, radius.ErrServerShutdown) {
				errs <- err
			}
		}()
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	auth.Shutdown(sh)
	acct.Shutdown(sh)
	wg.Wait()
	return err
}

func reject(w radius.ResponseWriter, r *radius.Request, msg string) {
	p := r.Response(radius.CodeAccessReject)
	rfc2865.ReplyMessage_SetString(p, msg)
	send(w, p)
}

// AuthRequest is the transport-neutral input of Authorize (UDP packet or rlm_rest form).
type AuthRequest struct {
	User     string
	Check    func(pw []byte) (ok bool, success string) // verifies the offered credential against the stored password
	FramedIP string
	MAC      string
	NAS      string // NAS address, for the voucher-guess limiter
}

// Decision is the outcome of Authorize. Reject != "" means Access-Reject with that message.
type Decision struct {
	Reject  string
	Success string // MS-CHAP2-Success blob, UDP only
	Cust    db.Customer
	Plan    db.GetRadiusPlanRow
	Rate    string // Mikrotik-Rate-Limit, "" = none
	// AddressList is the Mikrotik-Address-List of an isolated customer (isolir plan), "" = none.
	AddressList string
	Timeout     int64 // Session-Timeout seconds, 0 = none
	Expires     int64
	TotalLeft   int64 // remaining bytes when a data limit applies (HasTotal)
	HasTotal    bool
}

// Authorize is the shared decision: customer lookup, password, active and unexpired plan
// (clock-guarded), shared_users, time and data limits. Old PHP radius.php semantics.
func (s *Server) Authorize(ctx context.Context, rq AuthRequest) Decision {
	start := time.Now()
	defer func() {
		metrics.Add("radius_auth_duration_seconds_sum", time.Since(start).Seconds())
		metrics.Inc("radius_auth_duration_seconds_count")
	}()
	// ponytail: per-user throttle is in-memory, per process (see failLimiter). A retransmit never
	// gets here: dupCache absorbs it, so retries cannot lock a user out.
	now := s.now().Unix()
	if rq.User != "" && s.userFails.blocked(rq.User, now, voucherMaxFails) {
		metrics.Inc("radius_auth_rejected_total", "reason", "other")
		return Decision{Reject: "Too many attempts, try again later"}
	}
	d := s.decide(ctx, rq)
	switch d.Reject {
	case "":
		s.userFails.reset(rq.User)
		metrics.Inc("radius_auth_accepted_total")
	case badPassword:
		s.userFails.fail(rq.User, now)
		metrics.Inc("radius_auth_rejected_total", "reason", "bad_password")
	default:
		metrics.Inc("radius_auth_rejected_total", "reason", rejectReason(d.Reject))
	}
	return d
}

const badPassword = "Username or Password is wrong"

// SharedUsersFullMsg is the Reply-Message when shared_users is full and shared_users_policy is reject.
const SharedUsersFullMsg = "Akun sedang dipakai perangkat lain. Coba lagi beberapa menit lagi atau hubungi admin."

// rejectReason maps a reject message to the metric label reason.
func rejectReason(msg string) string {
	switch {
	case strings.Contains(msg, "Voucher") || strings.Contains(msg, "voucher"):
		return "voucher"
	case strings.Contains(msg, "xpired"):
		return "expired"
	case strings.Contains(msg, "limit") || msg == SharedUsersFullMsg:
		return "limit"
	}
	return "other"
}

func (s *Server) decide(ctx context.Context, rq AuthRequest) Decision {
	// Old radius.php: password == username (or empty, or CHAP of either) means "voucher login".
	vl := rq.User != "" && (func() bool { ok, _ := rq.Check([]byte(rq.User)); return ok }() ||
		func() bool { ok, _ := rq.Check(nil); return ok }())
	if !vl {
		return s.authorize(ctx, rq, false)
	}
	key, nasKey, now := rq.NAS+"|"+rq.MAC, "nas|"+rq.NAS, s.now().Unix()
	// the MAC is client-chosen, so also cap failures per NAS
	if voucherFails.blocked(key, now, voucherMaxFails) || voucherFails.blocked(nasKey, now, voucherMaxFails*10) {
		return Decision{Reject: "Too many attempts, try again later"}
	}
	d := s.authorize(ctx, rq, true)
	if d.Reject != "" {
		voucherFails.fail(key, now)
		voucherFails.fail(nasKey, now)
	}
	return d
}

// sharedPolicy is the shared_users_policy setting: "reject" or "kick_oldest" (default, also when unset).
func (s *Server) sharedPolicy(ctx context.Context) string {
	if s.settings(ctx)["shared_users_policy"] == "reject" {
		return "reject"
	}
	return "kick_oldest"
}

// settings is the settings table cached for nasTTL, like the nas list: a login read two or three
// settings, each a query. A read error gives an empty map (defaults) and is retried next packet.
func (s *Server) settings(ctx context.Context) map[string]string {
	s.nasMu.Lock()
	defer s.nasMu.Unlock()
	if time.Since(s.stAt) < nasTTL {
		return s.st
	}
	rows, err := s.Q.ListSettings(ctx)
	if err != nil {
		slog.Error("radius: read settings", "err", err)
		return map[string]string{}
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	s.st, s.stAt = m, time.Now()
	return m
}

// kickOldest stops the given sessions now and drops them on the NAS in the background, so the
// Access-Accept does not wait for the NAS. The stop is recorded even when the NAS does not answer.
func (s *Server) kickOldest(ctx context.Context, user string, sessions []db.RadiusSession) {
	kick := s.Kick
	if kick == nil {
		kick = func(ctx context.Context, sess db.RadiusSession) error {
			return Disconnect(ctx, s.Q, s.Key, CoAPort, sess)
		}
	}
	for _, sess := range sessions {
		if err := s.Q.StopRadiusSession(ctx, db.StopRadiusSessionParams{StoppedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true}, ID: sess.ID}); err != nil {
			slog.Error("radius: stop replaced session", "user", user, "session_id", sess.ID, "err", err)
		}
		slog.Info("radius: shared_users full, dropping oldest session", "user", user, "session_id", sess.ID)
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := kick(cctx, sess); err != nil {
				slog.Warn("radius: disconnect of replaced session failed", "user", user, "session_id", sess.ID, "err", err)
			}
		}()
	}
}

// LimitSeconds converts a plan time limit (Mins or Hrs) to seconds.
func LimitSeconds(n int64, unit string) int64 {
	t := n * 60
	if unit == "Hrs" {
		t *= 60
	}
	return t
}

// LimitBytes converts a plan data limit (MB or GB) to bytes.
func LimitBytes(n int64, unit string) int64 {
	b := n * 1048576
	if unit == "GB" {
		b *= 1024
	}
	return b
}

func (s *Server) authorize(ctx context.Context, rq AuthRequest, vl bool) Decision {
	user := rq.User
	// one query for an active customer with an active plan; the other cases fall back to two
	var c db.Customer
	var pl db.GetRadiusPlanRow
	lr, err := s.Q.GetRadiusLogin(ctx, user)
	havePlan := err == nil
	if havePlan {
		c, pl = lr.Customer, db.GetRadiusPlanRow{StartedAt: lr.StartedAt, ExpiresAt: lr.ExpiresAt, PendingStart: lr.PendingStart,
			PlanID: lr.PlanID, PlanName: lr.PlanName, PlanType: lr.PlanType, Limited: lr.Limited, LimitType: lr.LimitType,
			TimeLimit: lr.TimeLimit, TimeUnit: lr.TimeUnit, DataLimit: lr.DataLimit, DataUnit: lr.DataUnit, SharedUsers: lr.SharedUsers,
			RateDown: lr.RateDown, RateDownUnit: lr.RateDownUnit, RateUp: lr.RateUp, RateUpUnit: lr.RateUpUnit, Burst: lr.Burst, PoolName: lr.PoolName}
	} else {
		c, err = s.Q.GetCustomerForRadius(ctx, user)
	}
	found := havePlan || err == nil
	if user == "" || (!found && !vl) || (found && len(c.SecretEnc) == 0) {
		return Decision{Reject: "Login invalid......"}
	}
	var ok bool
	var success string
	if found {
		pw, err := secret.Open(s.Key, c.SecretEnc)
		if err != nil {
			slog.Error("radius: decrypt customer secret", "user", user, "err", err)
			return Decision{Reject: "Login invalid......"}
		}
		if len(pw) == 0 {
			return Decision{Reject: "Login invalid......"} // never accept an empty router password
		}
		ok, success = rq.Check(pw)
	}
	if !ok && vl {
		var msg string
		if c, msg = s.voucherLogin(ctx, user, c, found); msg != "" {
			if found && msg == "Invalid Voucher.." {
				msg = badPassword
			}
			return Decision{Reject: msg}
		}
		ok, havePlan = true, false // the voucher may have created or recharged the customer
	} else if !ok {
		return Decision{Reject: badPassword}
	}
	if !havePlan {
		if pl, err = s.Q.GetRadiusPlan(ctx, c.ID); err != nil {
			if vl {
				return Decision{Reject: "Voucher Expired..."}
			}
			return Decision{Reject: "Internet Plan Expired.."}
		}
	}
	if pl.PendingStart == 1 && s.Start != nil { // before the expiry check: a pending row's provisional expiry is not the real one
		if err := s.Start(ctx, c.ID); err != nil {
			slog.Error("radius: start pending subscription", "user", user, "err", err)
		} else if p, err := s.Q.GetRadiusPlan(ctx, c.ID); err == nil {
			pl = p
		}
	}
	left := pl.ExpiresAt - s.now().Unix()
	if left <= 0 {
		// Old PHP rejects expired accounts. A wrong clock must not lock everybody out.
		if s.Trusted == nil || s.Trusted() {
			return Decision{Reject: "Sorry, your account's active period has expired (" + time.Unix(pl.ExpiresAt, 0).UTC().Format("2006-01-02 15:04:05") + ")"}
		}
		slog.Warn("radius: clock untrusted, accepting expired subscription", "user", user)
	}
	// Old PHP: Hotspot only, count of logged-in sessions >= shared_users is refused (reject policy),
	// or the oldest ones are dropped so that this login fits (kick_oldest, the default).
	if pl.PlanType == "Hotspot" && pl.SharedUsers.Valid {
		others, err := s.Q.ListOtherOpenRadiusSessions(ctx, db.ListOtherOpenRadiusSessionsParams{
			Username: user, UpdatedAt: s.now().Unix() - StaleAfter, FramedIp: rq.FramedIP, Column4: rq.FramedIP, Mac: rq.MAC, Column6: rq.MAC})
		if err != nil { // fail closed: unknown sessions must not count as zero
			slog.Error("radius: list open sessions", "user", user, "err", err)
			return Decision{Reject: "Login invalid......"}
		}
		if n := int64(len(others)); n >= pl.SharedUsers.Int64 {
			if s.sharedPolicy(ctx) == "reject" {
				return Decision{Reject: SharedUsersFullMsg}
			}
			s.kickOldest(ctx, user, others[:min(n, max(0, n-pl.SharedUsers.Int64+1))])
		}
	}
	d := Decision{Success: success, Cust: c, Plan: pl, Expires: pl.ExpiresAt, AddressList: isolir.AddressListFor(s.settings(ctx), pl.PlanID)}
	if left > 0 {
		d.Timeout = left
	}
	if pl.RateUp.Valid && pl.RateDown.Valid {
		d.Rate = fmt.Sprintf("%d%s/%d%s", pl.RateUp.Int64, unit(pl.RateUpUnit.String), pl.RateDown.Int64, unit(pl.RateDownUnit.String))
		if b := strings.TrimSpace(pl.Burst.String); b != "" {
			d.Rate += " " + b
		}
	}
	if pl.Limited == 1 {
		lt := pl.LimitType.String
		if (lt == "Time_Limit" || lt == "Both_Limit") && pl.TimeLimit.Valid {
			t := LimitSeconds(pl.TimeLimit.Int64, pl.TimeUnit.String)
			// Max-All-Session: the limit is the total online time since activation, not per login.
			used, err := s.Q.SumRadiusSessionTime(ctx, db.SumRadiusSessionTimeParams{Username: user, StartedAt: pl.StartedAt})
			if err != nil { // fail closed, as for the data limit
				slog.Error("radius: sum time usage", "user", user, "err", err)
				return Decision{Reject: "Login invalid......"}
			}
			if t -= used; t <= 0 {
				return Decision{Reject: "You have exceeded your time limit."}
			}
			if d.Timeout == 0 || t < d.Timeout {
				d.Timeout = t
			}
		}
		if (lt == "Data_Limit" || lt == "Both_Limit") && pl.DataLimit.Valid {
			total := LimitBytes(pl.DataLimit.Int64, pl.DataUnit.String)
			used, err := s.Q.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: user, StartedAt: pl.StartedAt})
			if err != nil { // fail closed: an unknown usage must not count as zero
				slog.Error("radius: sum data usage", "user", user, "err", err)
				return Decision{Reject: "Login invalid......"}
			}
			if total-used <= 0 {
				return Decision{Reject: "You have exceeded your data limit."}
			}
			d.TotalLeft, d.HasTotal = total-used, true
		}
	}
	if d.Timeout > 1<<32-1 {
		d.Timeout = 1<<32 - 1
	}
	return d
}

// voucherLogin is the old radius.php voucher branch for a username that is a voucher code.
// Unused: create the customer (secret = code) if missing and redeem atomically; a concurrent
// loser of the UseVoucher race still gets in when the winner used the same customer.
// Used: only the redeeming customer passes. Returns a reject message, "" = authenticated.
func (s *Server) voucherLogin(ctx context.Context, code string, c db.Customer, found bool) (db.Customer, string) {
	v, err := s.Q.GetVoucherByCode(ctx, code)
	if err != nil {
		return c, "Invalid Voucher.."
	}
	if v.Status == "used" {
		if found && v.UsedBy.Valid && v.UsedBy.Int64 == c.ID {
			return c, ""
		}
		return c, "Voucher Expired..."
	}
	fail := "Voucher activation failed"
	pl, err := s.Q.GetPlan(ctx, v.PlanID)
	if err != nil || s.Redeem == nil || pl.Type == "Balance" || (found && c.Username != code) {
		return c, fail
	}
	if !found {
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		enc, err2 := secret.Seal(s.Key, []byte(code))
		if err != nil || err2 != nil {
			return c, fail
		}
		c, err = s.Q.CreateCustomer(ctx, db.CreateCustomerParams{Username: code, PasswordHash: string(hash),
			ServiceType: pl.Type, SecretEnc: enc, AutoRenewal: 1, Status: "Active"})
		if err != nil { // lost the creation race
			if c, err = s.Q.GetCustomerByUsername(ctx, code); err != nil {
				return c, fail
			}
		}
	}
	if err := s.Redeem(ctx, code, c.ID); err != nil {
		if v2, e := s.Q.GetVoucherByCode(ctx, code); e != nil || v2.Status != "used" || !v2.UsedBy.Valid || v2.UsedBy.Int64 != c.ID {
			return c, fail
		}
		return c, "" // concurrent first login by the same code: already activated
	}
	slog.Info("radius: voucher activated", "voucher_id", v.ID, "customer_id", c.ID) // never log the code
	return c, ""
}

// failLimiter counts failed voucher-like attempts per key inside a sliding window.
// Keys sit in two generations: cur is replaced every voucherWindow, or early once it holds
// failMaxKeys, and the previous one is kept for lookups. Memory stays bounded and nothing is
// scanned per attempt; the old version scanned every key on each failure once it held 10000,
// which a spray of unknown usernames turned into most of the server's CPU.
// ponytail: an early rotation forgets the counts in old, so a spray of failMaxKeys usernames
// buys an attacker another voucherMaxFails tries on one account. In-memory, per process, empty MAC
// shares one bucket per NAS; use a DB table if several instances or restarts must share the count.
type failLimiter struct {
	mu       sync.Mutex
	cur, old map[string][]int64
	rotated  int64 // unix time cur was started
}

const (
	voucherMaxFails = 10
	voucherWindow   = 15 * 60
	failMaxKeys     = 50000
)

var voucherFails = &failLimiter{}

// recent returns the failures of key inside the window, oldest first. Callers hold mu.
func (l *failLimiter) recent(key string, now int64) []int64 {
	var t []int64
	for _, x := range append(l.old[key], l.cur[key]...) {
		if x > now-voucherWindow {
			t = append(t, x)
		}
	}
	return t
}

func (l *failLimiter) blocked(key string, now int64, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, now)) >= max
}

func (l *failLimiter) fail(key string, now int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := append(l.recent(key, now), now)
	if l.cur == nil || now-l.rotated >= voucherWindow || now < l.rotated || len(l.cur) >= failMaxKeys {
		l.old, l.cur, l.rotated = l.cur, map[string][]int64{}, now
	}
	delete(l.old, key)
	l.cur[key] = t
}

func (l *failLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.old, key)
	delete(l.cur, key)
}

// HandleAuth is Access-Request over UDP: a thin packet adapter over Authorize.
func (s *Server) HandleAuth(w radius.ResponseWriter, r *radius.Request) {
	if s.shed() {
		return
	}
	defer s.busy.Add(-1)
	s.dups.serve(s.now(), w, r, s.handleAuth)
}

// maxBusy caps the packets handled at once (auth and accounting together). The library starts a
// goroutine per packet; past the server's capacity they queued without bound, memory grew to
// hundreds of MB, and every answer came after the NAS had given up (3 s), so nobody got in.
// Dropping the excess at once keeps the rest fast; the NAS retransmits what was dropped.
// ponytail: fixed; about half a second of work at the measured 1750 logins/s on two cores. 256 gave
// lower latency but served fewer packets under overload (bench/radius, mixed at 8000/s).
const maxBusy = 1000

// shed reports whether this packet must be dropped; when it returns false the caller holds a busy slot.
func (s *Server) shed() bool {
	if s.busy.Add(1) > maxBusy {
		s.busy.Add(-1)
		metrics.Inc("radius_packets_shed_total")
		return true
	}
	return false
}

func (s *Server) handleAuth(w radius.ResponseWriter, r *radius.Request) {
	ip := addrIP(r.RemoteAddr)
	n, known, _ := s.nasByIP(r.Context(), ip) // a lookup error counts as unknown, as before
	ma := checkMA(r.Packet)
	switch {
	case ma == maInvalid && known:
		s.badSecret(ip, n.Name) // with the wrong secret the Message-Authenticator never verifies
		return
	case ma == maInvalid || (ma == maMissing && maRequired(n.RequireMessageAuth, n.MaSeen)):
		if s.allowLog(&s.maWarned, ip.String(), time.Minute) {
			slog.Warn("radius: dropping Access-Request, Message-Authenticator missing or invalid", "nas", ip.String())
		}
		return
	}
	if ma == maValid && known {
		s.secretOK(ip)
		if n.MaSeen == 0 { // NAS speaks Message-Authenticator: auto mode requires it from now on
			if err := s.Q.MarkNASMASeen(r.Context(), n.ID); err != nil {
				slog.Error("radius: mark Message-Authenticator seen", "nas", ip.String(), "err", err)
			}
			s.forgetNAS() // the requirement holds from the next packet, not after nasTTL
		}
	}
	if known { // unknown sources must not create metric series
		s.NoteNAS(ip.String())
	}
	user := rfc2865.UserName_GetString(r.Packet)
	rq := AuthRequest{User: user, NAS: addrIP(r.RemoteAddr).String(), MAC: rfc2865.CallingStationID_GetString(r.Packet),
		Check: func(pw []byte) (bool, string) { return checkPassword(r.Packet, user, pw) }}
	if ip := rfc2865.FramedIPAddress_Get(r.Packet); ip != nil {
		rq.FramedIP = ip.String()
	}
	d := s.Authorize(r.Context(), rq)
	if d.Reject != "" {
		reject(w, r, d.Reject)
		return
	}
	p := r.Response(radius.CodeAccessAccept)
	rfc2865.ReplyMessage_SetString(p, "success")
	if d.Success != "" {
		microsoft.MSCHAP2Success_Add(p, []byte(d.Success))
	}
	if d.Plan.PlanType == "PPPoE" {
		if d.Plan.PoolName.Valid && d.Plan.PoolName.String != "" {
			rfc2869.FramedPool_SetString(p, d.Plan.PoolName.String)
		}
		if ip := net.ParseIP(d.Cust.PppoeIp).To4(); ip != nil {
			rfc2865.FramedIPAddress_Set(p, ip)
		}
	}
	if d.Rate != "" {
		mikrotik.MikrotikRateLimit_SetString(p, d.Rate)
	}
	if d.AddressList != "" {
		mikrotik.MikrotikAddressList_AddString(p, d.AddressList)
	}
	if d.HasTotal {
		rem := uint64(d.TotalLeft)
		mikrotik.MikrotikTotalLimit_Set(p, mikrotik.MikrotikTotalLimit(uint32(rem)))
		mikrotik.MikrotikTotalLimitGigawords_Set(p, mikrotik.MikrotikTotalLimitGigawords(rem>>32))
	}
	if d.Timeout > 0 {
		rfc2865.SessionTimeout_Set(p, rfc2865.SessionTimeout(d.Timeout))
	}
	send(w, p)
}

func unit(u string) string {
	if u == "Kbps" {
		return "K"
	}
	return "M"
}

// CHAPOK checks a CHAP response: md5(id || password || challenge) == resp.
func CHAPOK(pw []byte, id byte, challenge, resp []byte) bool {
	h := md5.New()
	h.Write([]byte{id})
	h.Write(pw)
	h.Write(challenge)
	return subtle.ConstantTimeCompare(h.Sum(nil), resp) == 1
}

// checkPassword tries CHAP, MS-CHAPv2, then PAP. success is the MS-CHAP2-Success blob when applicable.
func checkPassword(p *radius.Packet, user string, pw []byte) (ok bool, success string) {
	if chap := rfc2865.CHAPPassword_Get(p); len(chap) == 17 {
		ch := rfc2865.CHAPChallenge_Get(p)
		if len(ch) == 0 {
			ch = p.Authenticator[:]
		}
		return CHAPOK(pw, chap[0], ch, chap[1:]), ""
	}
	if resp := microsoft.MSCHAP2Response_Get(p); len(resp) == 50 {
		ch := microsoft.MSCHAPChallenge_Get(p)
		if len(ch) != 16 {
			return false, ""
		}
		nt, err := rfc2759.GenerateNTResponse(ch, resp[2:18], []byte(user), pw)
		if err != nil || !bytes.Equal(nt, resp[26:50]) {
			return false, ""
		}
		auth, err := rfc2759.GenerateAuthenticatorResponse(ch, resp[2:18], nt, []byte(user), pw)
		if err != nil {
			return false, ""
		}
		// ponytail: MPPE keys (MS-MPPE-Send/Recv) not sent; add if PPPoE needs encryption.
		return true, string(resp[:1]) + auth
	}
	if _, err := rfc2865.UserPassword_Lookup(p); err == nil {
		return subtle.ConstantTimeCompare([]byte(rfc2865.UserPassword_GetString(p)), pw) == 1, ""
	}
	return false, ""
}

// AcctRequest is the transport-neutral accounting input.
type AcctRequest struct {
	Type                 rfc2866.AcctStatusType
	NAS, SessionID, User string
	NASIPAttr, NASID     string // NAS-IP-Address / NAS-Identifier as the NAS reports them
	MAC, FramedIP        string
	SessionTime          int64
	InOctets, OutOctets  int64 // gigawords already folded in
}

// NoteNAS records a packet from NAS nas in radius_nas_last_packet_timestamp. Callers must only pass
// sources they have validated (nas table, or the allow-listed REST API).
func (s *Server) NoteNAS(nas string) {
	metrics.Set("radius_nas_last_packet_timestamp", float64(s.now().Unix()), "nas", nas)
}

// acctOnWindow is how many seconds before an Accounting-On/Off a session update must be to be closed by it.
const acctOnWindow = 5

// Account applies Start, Interim-Update, Stop and Accounting-On/Off to radius_sessions.
func (s *Server) Account(ctx context.Context, a AcctRequest) error {
	now := s.now().Unix()
	metrics.Inc("radius_acct_packets_total", "status", a.Type.String())
	metrics.Set("radius_nas_last_packet_timestamp", float64(now), "nas", a.NAS)
	switch a.Type {
	case rfc2866.AcctStatusType_Value_AccountingOn, rfc2866.AcctStatusType_Value_AccountingOff:
		// NAS rebooted: its open sessions are gone. A session updated in the last few seconds is the Start
		// that raced this packet (both are sent together), so it stays open.
		// ponytail: fixed window; a pre-reboot interim inside it is left to CloseStaleRadiusSessions.
		arg := db.CloseRadiusSessionsByNASParams{StoppedAt: sql.NullInt64{Int64: now, Valid: true}, NasIp: a.NAS, UpdatedAt: now - acctOnWindow}
		return s.commit(ctx, func(q *db.Queries) error { return q.CloseRadiusSessionsByNAS(ctx, arg) })
	case rfc2866.AcctStatusType_Value_Start, rfc2866.AcctStatusType_Value_InterimUpdate, rfc2866.AcctStatusType_Value_Stop:
		// gigawords << 32 goes negative from 2^31 GB up, and a REST value can be negative. Clamp, do not
		// refuse: a dropped Stop would leave the session open. MAX() in the upsert keeps the old value.
		if a.InOctets < 0 || a.OutOctets < 0 {
			slog.Warn("radius: negative octet counter clamped to 0", "user", a.User, "nas", a.NAS)
			a.InOctets, a.OutOctets = max(a.InOctets, 0), max(a.OutOctets, 0)
		}
		arg := db.UpsertRadiusSessionParams{SessionID: a.SessionID, Username: a.User, NasIp: a.NAS, NasIpAttr: a.NASIPAttr, NasIdentifier: a.NASID, FramedIp: a.FramedIP,
			Mac: a.MAC, StartedAt: now - a.SessionTime, UpdatedAt: now, InputOctets: a.InOctets, OutputOctets: a.OutOctets}
		if a.Type == rfc2866.AcctStatusType_Value_Stop {
			arg.StoppedAt = sql.NullInt64{Int64: now, Valid: true}
		}
		return s.commit(ctx, func(q *db.Queries) error { return q.UpsertRadiusSession(ctx, arg) })
	}
	return nil
}

// HandleAcct is Accounting-Request over UDP.
func (s *Server) HandleAcct(w radius.ResponseWriter, r *radius.Request) {
	if s.shed() {
		return
	}
	defer s.busy.Add(-1)
	s.dups.serve(s.now(), w, r, s.handleAcct)
}

func (s *Server) handleAcct(w radius.ResponseWriter, r *radius.Request) {
	p := r.Packet
	a := AcctRequest{Type: rfc2866.AcctStatusType_Get(p), NAS: addrIP(r.RemoteAddr).String(),
		SessionID: rfc2866.AcctSessionID_GetString(p), User: rfc2865.UserName_GetString(p),
		MAC: rfc2865.CallingStationID_GetString(p), SessionTime: int64(rfc2866.AcctSessionTime_Get(p)),
		InOctets:  int64(rfc2869.AcctInputGigawords_Get(p))<<32 | int64(rfc2866.AcctInputOctets_Get(p)),
		OutOctets: int64(rfc2869.AcctOutputGigawords_Get(p))<<32 | int64(rfc2866.AcctOutputOctets_Get(p))}
	if ip := rfc2865.FramedIPAddress_Get(p); ip != nil {
		a.FramedIP = ip.String()
	}
	if ip := rfc2865.NASIPAddress_Get(p); ip != nil {
		a.NASIPAttr = ip.String()
	}
	a.NASID = rfc2865.NASIdentifier_GetString(p)
	if err := s.Account(r.Context(), a); err != nil {
		slog.Error("radius: accounting", "err", err)
		return // no response: NAS retries
	}
	w.Write(r.Response(radius.CodeAccountingResponse))
}
