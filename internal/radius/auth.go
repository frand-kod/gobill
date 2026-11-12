package radius

import (
	"context"
	"database/sql"
	"log/slog"
	"math"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"
	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2869"
	"layeh.com/radius/vendors/microsoft"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/secret"
)

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
	NASType  string // nas.type of the NAS, "" = mikrotik
	PortID   string // NAS-Port-Id as the NAS reports it, "" = not sent
	PortType string // NAS-Port-Type by name ("Virtual", "Wireless-802.11"...), "" = the NAS did not send one
	DryRun   bool   // read-only decision (router migration test): no pending start, no session kick
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
	Interim     int64 // Acct-Interim-Interval seconds, 0 = not sent
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
	if d.Reject == "" && d.AddressList != "" && rq.NASType != "" && rq.NASType != NASMikrotik {
		d.Reject = msgIsolated // only MikroTik has the address list that sends isolated customers to the isolir page
	}
	s.logAuth(ctx, rq, d)
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

// SharedUsersFullMsg is the Reply-Message when shared_users is full and shared_users_policy is reject.
const SharedUsersFullMsg = "Akun sedang dipakai perangkat lain. Coba lagi beberapa menit lagi atau hubungi admin."

// rejectReason maps a reject message to the metric label reason.
func rejectReason(msg string) string {
	switch {
	case strings.Contains(msg, "Voucher") || strings.Contains(msg, "voucher"):
		return "voucher"
	case strings.Contains(msg, "xpired"):
		return "expired"
	case strings.HasPrefix(msg, "Port type"):
		return "port_type"
	case msg == msgIsolated:
		return "isolated"
	case strings.HasPrefix(msg, msgOutsideWindow):
		return "window"
	case strings.HasPrefix(msg, "Account locked"):
		return "binding"
	case strings.Contains(msg, "limit") || msg == SharedUsersFullMsg:
		return "limit"
	}
	return "other"
}

var lastSprayWarn atomic.Int64

// warnNASSpray counts the event and logs at most once a minute.
func warnNASSpray(nas string, now int64) {
	metrics.Inc("radius_voucher_nas_spray_total")
	if last := lastSprayWarn.Load(); now-last >= 60 && lastSprayWarn.CompareAndSwap(last, now) {
		slog.Warn("radius: many failed voucher logins on one NAS, possible guessing spray", "nas", nas)
	}
}

func (s *Server) decide(ctx context.Context, rq AuthRequest) Decision {
	// Old radius.php: password == username (or empty, or CHAP of either) means "voucher login".
	vl := rq.User != "" && (func() bool { ok, _ := rq.Check([]byte(rq.User)); return ok }() ||
		func() bool { ok, _ := rq.Check(nil); return ok }())
	if !vl {
		return s.authorize(ctx, rq, false)
	}
	key, nasKey, now := rq.NAS+"|"+rq.MAC, "nas|"+rq.NAS, s.now().Unix()
	if voucherFails.blocked(key, now, voucherMaxFails) {
		return Decision{Reject: "Too many attempts, try again later"}
	}
	// The MAC is client-chosen, so one client can spray random MACs. The per-NAS count only
	// alerts: rejecting on it would let that client lock every voucher login on the NAS.
	if voucherFails.blocked(nasKey, now, voucherMaxFails*10) {
		warnNASSpray(rq.NAS, now)
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
	if s.store().Get(ctx, "shared_users_policy") == "reject" {
		return "reject"
	}
	return "kick_oldest"
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

func (s *Server) authorize(ctx context.Context, rq AuthRequest, vl bool) Decision {
	user := rq.User
	// one query for an active customer with an active plan; the other cases fall back to two
	var c db.Customer
	var pl db.GetRadiusPlanRow
	lr, err := s.Q.GetRadiusLogin(ctx, user)
	havePlan := err == nil
	if havePlan {
		c, pl = lr.Customer, db.GetRadiusPlanRow{StartedAt: lr.StartedAt, QuotaCarryBytes: lr.QuotaCarryBytes, QuotaCarrySeconds: lr.QuotaCarrySeconds, ExpiresAt: lr.ExpiresAt, PendingStart: lr.PendingStart,
			UsedInOctets: lr.UsedInOctets, UsedOutOctets: lr.UsedOutOctets, UsedSeconds: lr.UsedSeconds,
			PlanID: lr.PlanID, PlanName: lr.PlanName, PlanType: lr.PlanType, Limited: lr.Limited, LimitType: lr.LimitType,
			TimeLimit: lr.TimeLimit, TimeUnit: lr.TimeUnit, DataLimit: lr.DataLimit, DataUnit: lr.DataUnit, SharedUsers: lr.SharedUsers, IdleTimeout: lr.IdleTimeout, LoginWindow: lr.LoginWindow, BoostWindow: lr.BoostWindow,
			RateDown: lr.RateDown, RateDownUnit: lr.RateDownUnit, RateUp: lr.RateUp, RateUpUnit: lr.RateUpUnit, Burst: lr.Burst, PoolName: lr.PoolName, BindMac: lr.BindMac, BindPortID: lr.BindPortID,
			FupRateDown: lr.FupRateDown, FupRateDownUnit: lr.FupRateDownUnit, FupRateUp: lr.FupRateUp, FupRateUpUnit: lr.FupRateUpUnit, FupBurst: lr.FupBurst,
			BoostRateDown: lr.BoostRateDown, BoostRateDownUnit: lr.BoostRateDownUnit, BoostRateUp: lr.BoostRateUp, BoostRateUpUnit: lr.BoostRateUpUnit, BoostBurst: lr.BoostBurst}
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
			iso, isolated := isolir.PlanFor(ctx, s.Q, s.settings(ctx), c.ID, s.now())
			if !isolated {
				if vl {
					return Decision{Reject: "Voucher Expired..."}
				}
				return Decision{Reject: "Internet Plan Expired.."}
			}
			pl = iso
		}
	}
	if msg := portMismatch(pl.PlanType, rq.PortType); msg != "" {
		return Decision{Reject: msg} // before Start: a refused login must not start a pending subscription
	}
	if msg := bindingMismatch(pl.BindMac, pl.BindPortID, rq); msg != "" {
		return Decision{Reject: msg}
	}
	var winLeft int64 // seconds to the end of the login window, 0 = no window
	if ok, left := windowLeft(pl.LoginWindow, s.now().In(s.loc())); !ok {
		return Decision{Reject: msgOutsideWindow + " (" + pl.LoginWindow + ")"}
	} else {
		winLeft = left
	}
	if pl.PendingStart == 1 && rq.DryRun {
		pl.ExpiresAt = math.MaxInt32 // not started, not touched: the provisional expiry says nothing
	} else if pl.PendingStart == 1 && s.Start != nil { // before the expiry check: a pending row's provisional expiry is not the real one
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
	// Old PHP: Hotspot only (PPPoE too when shared_users_pppoe = yes), count of logged-in sessions >= shared_users is refused (reject policy),
	// or the oldest ones are dropped so that this login fits (kick_oldest, the default).
	if (pl.PlanType == "Hotspot" || (pl.PlanType == "PPPoE" && s.settings(ctx)["shared_users_pppoe"] == "yes")) && pl.SharedUsers.Valid {
		others, err := s.Q.ListOtherOpenRadiusSessions(ctx, db.ListOtherOpenRadiusSessionsParams{
			Username: user, UpdatedAt: s.now().Unix() - StaleFor(rq.NAS), FramedIp: rq.FramedIP, Column4: rq.FramedIP, Mac: rq.MAC, Column6: rq.MAC})
		if err != nil { // fail closed: unknown sessions must not count as zero
			slog.Error("radius: list open sessions", "user", user, "err", err)
			return Decision{Reject: "Login invalid......"}
		}
		if n := int64(len(others)); n >= pl.SharedUsers.Int64 {
			if s.sharedPolicy(ctx) == "reject" {
				return Decision{Reject: SharedUsersFullMsg}
			}
			if !rq.DryRun {
				s.kickOldest(ctx, user, others[:min(n, max(0, n-pl.SharedUsers.Int64+1))])
			}
		}
	}
	d := Decision{Success: success, Cust: c, Plan: pl, Expires: pl.ExpiresAt, Interim: s.interimInterval(ctx), AddressList: isolir.AddressListFor(s.settings(ctx), pl.PlanID)}
	if left > 0 {
		d.Timeout = left
	}
	d.Rate = rateLimit(pl.RateUp, pl.RateDown, pl.RateUpUnit, pl.RateDownUnit, pl.Burst)
	if boostOn(pl.BoostWindow, s.now().In(s.loc())) { // before FUP: past the data limit the FUP rate wins
		if b := rateLimit(pl.BoostRateUp, pl.BoostRateDown, pl.BoostRateUpUnit, pl.BoostRateDownUnit, pl.BoostBurst); b != "" {
			d.Rate = b
		}
	}
	if pl.Limited == 1 {
		lt := pl.LimitType.String
		hasTime := (lt == "Time_Limit" || lt == "Both_Limit") && pl.TimeLimit.Valid
		hasData := (lt == "Data_Limit" || lt == "Both_Limit") && pl.DataLimit.Valid
		if hasTime || hasData {
			// the subscription's counters: both login names, sessions started inside the usage window
			bytes, secs := pl.UsedInOctets+pl.UsedOutOctets, pl.UsedSeconds
			if hasTime {
				// Max-All-Session: the limit is the total online time since activation, not per login.
				t := db.QuotaSeconds(pl.TimeLimit.Int64, pl.TimeUnit.String, pl.QuotaCarrySeconds) - secs
				if t <= 0 {
					return Decision{Reject: "You have exceeded your time limit."}
				}
				if d.Timeout == 0 || t < d.Timeout {
					d.Timeout = t
				}
			}
			if hasData {
				left := db.QuotaBytes(pl.DataLimit.Int64, pl.DataUnit.String, pl.QuotaCarryBytes) - bytes
				if left > 0 {
					d.TotalLeft, d.HasTotal = left, true
				} else if fup := rateLimit(pl.FupRateUp, pl.FupRateDown, pl.FupRateUpUnit, pl.FupRateDownUnit, pl.FupBurst); fup != "" {
					d.Rate = fup // FUP: slowed down for the rest of the period, no Total-Limit
				} else {
					return Decision{Reject: "You have exceeded your data limit."}
				}
			}
		}
	}
	if winLeft > 0 && (d.Timeout == 0 || winLeft < d.Timeout) {
		d.Timeout = winLeft // the NAS drops the session at the end of the login window
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

// HandleAuth is Access-Request over UDP: a thin packet adapter over Authorize.
func (s *Server) HandleAuth(w radius.ResponseWriter, r *radius.Request) {
	if s.shed() {
		return
	}
	defer s.busy.Add(-1)
	s.dups.serve(s.now(), w, r, s.handleAuth)
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
	rq := AuthRequest{User: user, NAS: addrIP(r.RemoteAddr).String(), NASType: n.Type, MAC: rfc2865.CallingStationID_GetString(r.Packet),
		Check: func(pw []byte) (bool, string) { return checkPassword(r.Packet, user, pw) }}
	if ip := rfc2865.FramedIPAddress_Get(r.Packet); ip != nil {
		rq.FramedIP = ip.String()
	}
	rq.PortID = rfc2869.NASPortID_GetString(r.Packet)
	if pt, err := rfc2865.NASPortType_Lookup(r.Packet); err == nil {
		rq.PortType = pt.String()
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
		setRate(p, n.Type, d.Rate)
	}
	if d.AddressList != "" {
		mikrotik.MikrotikAddressList_AddString(p, d.AddressList)
	}
	if d.HasTotal {
		setTotal(p, n.Type, uint64(d.TotalLeft))
	}
	if d.Timeout > 0 {
		rfc2865.SessionTimeout_Set(p, rfc2865.SessionTimeout(d.Timeout))
	}
	if d.Plan.IdleTimeout.Valid && d.Plan.IdleTimeout.Int64 > 0 {
		rfc2865.IdleTimeout_Set(p, rfc2865.IdleTimeout(min(d.Plan.IdleTimeout.Int64, 1<<32-1)))
	}
	if d.Interim > 0 {
		rfc2869.AcctInterimInterval_Set(p, rfc2869.AcctInterimInterval(d.Interim))
	}
	send(w, p)
}
