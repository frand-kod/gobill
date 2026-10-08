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
	w.Write(p)
}

// HandleAuth is Access-Request: authorize (customer, active plan, expiry) + authenticate (password).
func (s *Server) HandleAuth(w radius.ResponseWriter, r *radius.Request) {
	ctx := r.Context()
	user := rfc2865.UserName_GetString(r.Packet)
	c, err := s.Q.GetCustomerForRadius(ctx, user)
	if err != nil || user == "" || len(c.SecretEnc) == 0 {
		reject(w, r, "Login invalid......")
		return
	}
	pw, err := secret.Open(s.Key, c.SecretEnc)
	if err != nil {
		slog.Error("radius: decrypt customer secret", "user", user, "err", err)
		reject(w, r, "Login invalid......")
		return
	}
	ok, success := checkPassword(r.Packet, user, pw)
	if !ok {
		reject(w, r, "Username or Password is wrong")
		return
	}
	pl, err := s.Q.GetRadiusPlan(ctx, c.ID)
	if err != nil {
		reject(w, r, "No active plan")
		return
	}
	left := pl.ExpiresAt - s.now().Unix()
	if left <= 0 {
		// Old PHP rejects expired accounts. A wrong clock must not lock everybody out.
		if s.Trusted == nil || s.Trusted() {
			reject(w, r, "Sorry, your account's active period has expired ("+time.Unix(pl.ExpiresAt, 0).UTC().Format("2006-01-02 15:04:05")+")")
			return
		}
		slog.Warn("radius: clock untrusted, accepting expired subscription", "user", user)
	}
	// Old PHP: Hotspot only, count of logged-in sessions >= shared_users is refused.
	if pl.PlanType == "Hotspot" && pl.SharedUsers.Valid {
		fip := ""
		if ip := rfc2865.FramedIPAddress_Get(r.Packet); ip != nil {
			fip = ip.String()
		}
		mac := rfc2865.CallingStationID_GetString(r.Packet)
		n, _ := s.Q.CountOtherOpenRadiusSessions(ctx, db.CountOtherOpenRadiusSessionsParams{
			Username: user, UpdatedAt: s.now().Unix() - staleAfter, FramedIp: fip, Column4: fip, Mac: mac, Column6: mac})
		if n >= pl.SharedUsers.Int64 {
			reject(w, r, "You are already logged in - access denied")
			return
		}
	}

	p := r.Response(radius.CodeAccessAccept)
	rfc2865.ReplyMessage_SetString(p, "success")
	if success != "" {
		microsoft.MSCHAP2Success_Add(p, []byte(success))
	}
	timeout := int64(0)
	if left > 0 {
		timeout = left
	}
	if pl.PlanType == "PPPoE" {
		if pl.PoolName.Valid && pl.PoolName.String != "" {
			rfc2869.FramedPool_SetString(p, pl.PoolName.String)
		}
		if ip := net.ParseIP(c.PppoeIp).To4(); ip != nil {
			rfc2865.FramedIPAddress_Set(p, ip)
		}
	}
	if pl.RateUp.Valid && pl.RateDown.Valid {
		rate := fmt.Sprintf("%d%s/%d%s", pl.RateUp.Int64, unit(pl.RateUpUnit.String), pl.RateDown.Int64, unit(pl.RateDownUnit.String))
		if b := strings.TrimSpace(pl.Burst.String); b != "" {
			rate += " " + b
		}
		mikrotik.MikrotikRateLimit_SetString(p, rate)
	}
	if pl.Limited == 1 {
		lt := pl.LimitType.String
		if (lt == "Time_Limit" || lt == "Both_Limit") && pl.TimeLimit.Valid {
			t := pl.TimeLimit.Int64 * 60
			if pl.TimeUnit.String == "Hrs" {
				t *= 60
			}
			if timeout == 0 || t < timeout {
				timeout = t
			}
		}
		if (lt == "Data_Limit" || lt == "Both_Limit") && pl.DataLimit.Valid {
			total := pl.DataLimit.Int64 * 1048576
			if pl.DataUnit.String == "GB" {
				total *= 1024
			}
			used, _ := s.Q.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: user, StartedAt: pl.StartedAt})
			if total-used <= 0 {
				reject(w, r, "You have exceeded your data limit.")
				return
			}
			rem := uint64(total - used)
			mikrotik.MikrotikTotalLimit_Set(p, mikrotik.MikrotikTotalLimit(uint32(rem)))
			mikrotik.MikrotikTotalLimitGigawords_Set(p, mikrotik.MikrotikTotalLimitGigawords(rem>>32))
		}
	}
	if timeout > 0 {
		if timeout > 1<<32-1 {
			timeout = 1<<32 - 1
		}
		rfc2865.SessionTimeout_Set(p, rfc2865.SessionTimeout(timeout))
	}
	w.Write(p)
}

func unit(u string) string {
	if u == "Kbps" {
		return "K"
	}
	return "M"
}

// checkPassword tries CHAP, MS-CHAPv2, then PAP. success is the MS-CHAP2-Success blob when applicable.
func checkPassword(p *radius.Packet, user string, pw []byte) (ok bool, success string) {
	if chap := rfc2865.CHAPPassword_Get(p); len(chap) == 17 {
		ch := rfc2865.CHAPChallenge_Get(p)
		if len(ch) == 0 {
			ch = p.Authenticator[:]
		}
		h := md5.New()
		h.Write(chap[:1])
		h.Write(pw)
		h.Write(ch)
		return bytes.Equal(h.Sum(nil), chap[1:]), ""
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

// HandleAcct is Accounting-Request. Interim only updates the session row.
func (s *Server) HandleAcct(w radius.ResponseWriter, r *radius.Request) {
	p := r.Packet
	nas := addrIP(r.RemoteAddr).String()
	now := s.now().Unix()
	typ := rfc2866.AcctStatusType_Get(p)
	switch typ {
	case rfc2866.AcctStatusType_Value_AccountingOn, rfc2866.AcctStatusType_Value_AccountingOff:
		// NAS rebooted: its open sessions are gone.
		s.Q.CloseRadiusSessionsByNAS(r.Context(), db.CloseRadiusSessionsByNASParams{StoppedAt: sql.NullInt64{Int64: now, Valid: true}, NasIp: nas})
	case rfc2866.AcctStatusType_Value_Start, rfc2866.AcctStatusType_Value_InterimUpdate, rfc2866.AcctStatusType_Value_Stop:
		arg := db.UpsertRadiusSessionParams{
			SessionID:    rfc2866.AcctSessionID_GetString(p),
			Username:     rfc2865.UserName_GetString(p),
			NasIp:        nas,
			Mac:          rfc2865.CallingStationID_GetString(p),
			StartedAt:    now - int64(rfc2866.AcctSessionTime_Get(p)),
			UpdatedAt:    now,
			InputOctets:  int64(rfc2869.AcctInputGigawords_Get(p))<<32 | int64(rfc2866.AcctInputOctets_Get(p)),
			OutputOctets: int64(rfc2869.AcctOutputGigawords_Get(p))<<32 | int64(rfc2866.AcctOutputOctets_Get(p)),
		}
		if ip := rfc2865.FramedIPAddress_Get(p); ip != nil {
			arg.FramedIp = ip.String()
		}
		if typ == rfc2866.AcctStatusType_Value_Stop {
			arg.StoppedAt = sql.NullInt64{Int64: now, Valid: true}
		}
		if err := s.Q.UpsertRadiusSession(r.Context(), arg); err != nil {
			slog.Error("radius: accounting", "err", err)
			return // no response: NAS retries
		}
	}
	w.Write(r.Response(radius.CodeAccountingResponse))
}
