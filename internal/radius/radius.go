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
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"layeh.com/radius"
	"layeh.com/radius/rfc2759"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2869"
	"layeh.com/radius/vendors/microsoft"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

// staleAfter: an open session not updated for this long no longer counts toward shared_users.
// ponytail: fixed 2x a 5-minute interim; make it a setting if NAS interim differs.
const staleAfter = 600

type Server struct {
	Q        *db.Queries
	Key      []byte
	Trusted  func() bool      // clock guard; false = do not reject for expiry
	AuthAddr string           // e.g. ":1812"
	AcctAddr string           // e.g. ":1813"
	Now      func() time.Time // default time.Now
	// Redeem activates an unused voucher for a customer (billing.Service.RedeemVoucher).
	// nil disables hotspot voucher login.
	Redeem func(ctx context.Context, code string, customerID int64) error
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// RADIUSSecret finds the NAS by source IP (exact or CIDR) and decrypts its secret.
// An error makes the library drop the packet.
func (s *Server) RADIUSSecret(ctx context.Context, addr net.Addr) ([]byte, error) {
	ip := addrIP(addr)
	rows, err := s.Q.ListNAS(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range rows {
		if matchIP(n.Ip, ip) {
			return secret.Open(s.Key, n.SecretEnc)
		}
	}
	return nil, fmt.Errorf("unknown NAS %v", ip)
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
			if err := ps.ListenAndServe(); err != nil && !errors.Is(err, radius.ErrServerShutdown) {
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
	Reject    string
	Success   string // MS-CHAP2-Success blob, UDP only
	Cust      db.Customer
	Plan      db.GetRadiusPlanRow
	Rate      string // Mikrotik-Rate-Limit, "" = none
	Timeout   int64  // Session-Timeout seconds, 0 = none
	Expires   int64
	TotalLeft int64 // remaining bytes when a data limit applies (HasTotal)
	HasTotal  bool
}

// Authorize is the shared decision: customer lookup, password, active and unexpired plan
// (clock-guarded), shared_users, time and data limits. Old PHP radius.php semantics.
func (s *Server) Authorize(ctx context.Context, rq AuthRequest) Decision {
	// Old radius.php: password == username (or empty, or CHAP of either) means "voucher login".
	vl := rq.User != "" && (func() bool { ok, _ := rq.Check([]byte(rq.User)); return ok }() ||
		func() bool { ok, _ := rq.Check(nil); return ok }())
	if !vl {
		return s.authorize(ctx, rq, false)
	}
	key, now := rq.NAS+"|"+rq.MAC, s.now().Unix()
	if voucherFails.blocked(key, now) {
		return Decision{Reject: "Too many attempts, try again later"}
	}
	d := s.authorize(ctx, rq, true)
	if d.Reject != "" {
		voucherFails.fail(key, now)
	}
	return d
}

func (s *Server) authorize(ctx context.Context, rq AuthRequest, vl bool) Decision {
	user := rq.User
	c, err := s.Q.GetCustomerForRadius(ctx, user)
	found := err == nil
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
				msg = "Username or Password is wrong"
			}
			return Decision{Reject: msg}
		}
		ok = true
	} else if !ok {
		return Decision{Reject: "Username or Password is wrong"}
	}
	pl, err := s.Q.GetRadiusPlan(ctx, c.ID)
	if err != nil {
		if vl {
			return Decision{Reject: "Voucher Expired..."}
		}
		return Decision{Reject: "No active plan"}
	}
	left := pl.ExpiresAt - s.now().Unix()
	if left <= 0 {
		// Old PHP rejects expired accounts. A wrong clock must not lock everybody out.
		if s.Trusted == nil || s.Trusted() {
			return Decision{Reject: "Sorry, your account's active period has expired (" + time.Unix(pl.ExpiresAt, 0).UTC().Format("2006-01-02 15:04:05") + ")"}
		}
		slog.Warn("radius: clock untrusted, accepting expired subscription", "user", user)
	}
	// Old PHP: Hotspot only, count of logged-in sessions >= shared_users is refused.
	if pl.PlanType == "Hotspot" && pl.SharedUsers.Valid {
		n, _ := s.Q.CountOtherOpenRadiusSessions(ctx, db.CountOtherOpenRadiusSessionsParams{
			Username: user, UpdatedAt: s.now().Unix() - staleAfter, FramedIp: rq.FramedIP, Column4: rq.FramedIP, Mac: rq.MAC, Column6: rq.MAC})
		if n >= pl.SharedUsers.Int64 {
			return Decision{Reject: "You are already logged in - access denied"}
		}
	}
	d := Decision{Success: success, Cust: c, Plan: pl, Expires: pl.ExpiresAt}
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
			t := pl.TimeLimit.Int64 * 60
			if pl.TimeUnit.String == "Hrs" {
				t *= 60
			}
			if d.Timeout == 0 || t < d.Timeout {
				d.Timeout = t
			}
		}
		if (lt == "Data_Limit" || lt == "Both_Limit") && pl.DataLimit.Valid {
			total := pl.DataLimit.Int64 * 1048576
			if pl.DataUnit.String == "GB" {
				total *= 1024
			}
			used, _ := s.Q.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: user, StartedAt: pl.StartedAt})
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
// ponytail: in-memory, per process, empty MAC shares one bucket per NAS; use a DB table if
// several instances or restarts must share the count.
type failLimiter struct {
	mu sync.Mutex
	m  map[string][]int64
}

const (
	voucherMaxFails = 10
	voucherWindow   = 15 * 60
)

var voucherFails = &failLimiter{m: map[string][]int64{}}

func (l *failLimiter) recent(key string, now int64) []int64 {
	t := l.m[key]
	i := 0
	for i < len(t) && t[i] <= now-voucherWindow {
		i++
	}
	return t[i:]
}

func (l *failLimiter) blocked(key string, now int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, now)) >= voucherMaxFails
}

func (l *failLimiter) fail(key string, now int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 10000 { // bound memory: drop idle keys
		for k := range l.m {
			if len(l.recent(k, now)) == 0 {
				delete(l.m, k)
			}
		}
	}
	l.m[key] = append(l.recent(key, now), now)
}

// HandleAuth is Access-Request over UDP: a thin packet adapter over Authorize.
func (s *Server) HandleAuth(w radius.ResponseWriter, r *radius.Request) {
	required := false
	ip := addrIP(r.RemoteAddr)
	if rows, err := s.Q.ListNAS(r.Context()); err == nil {
		for _, n := range rows {
			if matchIP(n.Ip, ip) {
				required = n.RequireMessageAuth == 1
				break
			}
		}
	}
	if !checkMA(r.Packet, required) {
		slog.Warn("radius: dropping Access-Request, bad or missing Message-Authenticator", "nas", ip)
		return
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
	MAC, FramedIP        string
	SessionTime          int64
	InOctets, OutOctets  int64 // gigawords already folded in
}

// Account applies Start, Interim-Update, Stop and Accounting-On/Off to radius_sessions.
func (s *Server) Account(ctx context.Context, a AcctRequest) error {
	now := s.now().Unix()
	switch a.Type {
	case rfc2866.AcctStatusType_Value_AccountingOn, rfc2866.AcctStatusType_Value_AccountingOff:
		// NAS rebooted: its open sessions are gone.
		return s.Q.CloseRadiusSessionsByNAS(ctx, db.CloseRadiusSessionsByNASParams{StoppedAt: sql.NullInt64{Int64: now, Valid: true}, NasIp: a.NAS})
	case rfc2866.AcctStatusType_Value_Start, rfc2866.AcctStatusType_Value_InterimUpdate, rfc2866.AcctStatusType_Value_Stop:
		arg := db.UpsertRadiusSessionParams{SessionID: a.SessionID, Username: a.User, NasIp: a.NAS, FramedIp: a.FramedIP,
			Mac: a.MAC, StartedAt: now - a.SessionTime, UpdatedAt: now, InputOctets: a.InOctets, OutputOctets: a.OutOctets}
		if a.Type == rfc2866.AcctStatusType_Value_Stop {
			arg.StoppedAt = sql.NullInt64{Int64: now, Valid: true}
		}
		return s.Q.UpsertRadiusSession(ctx, arg)
	}
	return nil
}

// HandleAcct is Accounting-Request over UDP.
func (s *Server) HandleAcct(w radius.ResponseWriter, r *radius.Request) {
	p := r.Packet
	a := AcctRequest{Type: rfc2866.AcctStatusType_Get(p), NAS: addrIP(r.RemoteAddr).String(),
		SessionID: rfc2866.AcctSessionID_GetString(p), User: rfc2865.UserName_GetString(p),
		MAC: rfc2865.CallingStationID_GetString(p), SessionTime: int64(rfc2866.AcctSessionTime_Get(p)),
		InOctets:  int64(rfc2869.AcctInputGigawords_Get(p))<<32 | int64(rfc2866.AcctInputOctets_Get(p)),
		OutOctets: int64(rfc2869.AcctOutputGigawords_Get(p))<<32 | int64(rfc2866.AcctOutputOctets_Get(p))}
	if ip := rfc2865.FramedIPAddress_Get(p); ip != nil {
		a.FramedIP = ip.String()
	}
	if err := s.Account(r.Context(), a); err != nil {
		slog.Error("radius: accounting", "err", err)
		return // no response: NAS retries
	}
	w.Write(r.Response(radius.CodeAccountingResponse))
}
