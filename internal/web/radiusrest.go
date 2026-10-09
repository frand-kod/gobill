package web

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"layeh.com/radius/rfc2866"

	"github.com/frand-kod/gobill/internal/radius"
)

// radiusRestRoutes serves the FreeRADIUS rlm_rest endpoint of old phpnuxbill (radius.php), so
// an existing FreeRADIUS only needs a new connect_uri. Decisions come from radius.Server.
func (s *Server) radiusRestRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /radius.php", s.radiusRest)
	mux.HandleFunc("POST /radius/rest", s.radiusRest)
	if m, err := s.loadSettings(context.Background()); err == nil && strings.TrimSpace(m["radius_rest_allow"]) == "" {
		slog.Warn("radius.php endpoint is open to every client; set radius_rest_allow to the FreeRADIUS IP (comma-separated IPs/CIDRs)")
	}
}

func isRadiusRest(path string) bool { return path == "/radius.php" || path == "/radius/rest" }

// radiusRestAllowed: empty allow-list = everybody. X-Forwarded-For (rightmost = added by our proxy) only with trust_proxy=yes.
func radiusRestAllowed(r *http.Request, m map[string]string) bool {
	allow := strings.TrimSpace(m["radius_rest_allow"])
	if allow == "" {
		return true
	}
	ip := clientIP(r)
	if xf := r.Header.Get("X-Forwarded-For"); m["trust_proxy"] == "yes" && xf != "" {
		parts := strings.Split(xf, ",")
		ip = strings.TrimSpace(parts[len(parts)-1])
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, e := range strings.Split(allow, ",") {
		e = strings.TrimSpace(e)
		if pf, err := netip.ParsePrefix(e); err == nil {
			if pf.Contains(a) {
				return true
			}
		} else if x, err := netip.ParseAddr(e); err == nil && x.Unmap() == a {
			return true
		}
	}
	return false
}

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

type kv = map[string]any

// rejectBody mirrors old radius.php: the wrong-password reject has only Reply-Message, the
// limit rejects carry Auth-Type Accept (still 401), everything else is Auth-Type Reject.
func rejectBody(msg string) kv {
	switch {
	case msg == "Username or Password is wrong":
		return kv{"Reply-Message": msg}
	case msg == "You have exceeded your data limit." || strings.HasPrefix(msg, "You are already logged in"):
		return kv{"control:Auth-Type": "Accept", "Reply-Message": msg}
	}
	return kv{"control:Auth-Type": "Reject", "reply:Reply-Message": msg}
}

func bps(n int64, unit string) string {
	if unit == "Kbps" {
		return strconv.FormatInt(n*1000, 10)
	}
	return strconv.FormatInt(n*1000000, 10)
}

func (s *Server) radiusRest(w http.ResponseWriter, r *http.Request) {
	m, err := s.loadSettings(r.Context())
	if err != nil {
		jsonOut(w, 500, nil)
		return
	}
	if !radiusRestAllowed(r, m) {
		jsonOut(w, http.StatusForbidden, kv{"Reply-Message": "forbidden"})
		return
	}
	r.ParseForm() // body + query; credentials are never logged
	action := r.Header.Get("X-FreeRADIUS-Section")
	if action == "" {
		action = r.FormValue("action")
	}
	rs := s.radiusServer()
	user := r.FormValue("username")
	switch action {
	case "authorize", "authenticate":
		if user == "" {
			jsonOut(w, 401, rejectBody("Login invalid......"))
			return
		}
		pass := r.FormValue("password")
		chapResp, chapCh := r.FormValue("CHAPassword"), r.FormValue("CHAPchallenge")
		check := func(pw []byte) (bool, string) {
			if chapResp != "" {
				// FreeRADIUS sends "0x" + hex: CHAPassword = id(1) || md5(16), CHAPchallenge = challenge
				resp, e1 := hex.DecodeString(strings.TrimPrefix(chapResp, "0x"))
				ch, e2 := hex.DecodeString(strings.TrimPrefix(chapCh, "0x"))
				return e1 == nil && e2 == nil && len(resp) == 17 && radius.CHAPOK(pw, resp[0], ch, resp[1:]), ""
			}
			return subtle.ConstantTimeCompare([]byte(pass), pw) == 1, ""
		}
		// Voucher login (user == password, or empty password) is handled inside Authorize.
		d := rs.Authorize(r.Context(), radius.AuthRequest{User: user, Check: check,
			FramedIP: r.FormValue("framedIPAddress"), MAC: r.FormValue("macAddr"), NAS: r.FormValue("nasIpAddress")})
		if d.Reject != "" {
			jsonOut(w, 401, rejectBody(d.Reject))
			return
		}
		if action == "authenticate" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		jsonOut(w, 200, s.acceptBody(d))
	case "accounting":
		if user == "" {
			jsonOut(w, 200, kv{"control:Auth-Type": "Reject", "reply:Reply-Message": "Username empty"})
			return
		}
		typ := rfc2866.AcctStatusType_Value_InterimUpdate
		for k, v := range rfc2866.AcctStatusType_Strings {
			if v == r.FormValue("acctStatusType") {
				typ = k
			}
		}
		n := func(f string) int64 { v, _ := strconv.ParseInt(r.FormValue(f), 10, 64); return v }
		nas := r.FormValue("nasIpAddress")
		if nas == "" {
			nas = clientIP(r)
		}
		err := rs.Account(r.Context(), radius.AcctRequest{Type: typ, NAS: nas, NASIPAttr: r.FormValue("nasIpAddress"), NASID: r.FormValue("nasid"), SessionID: r.FormValue("acctSessionId"), User: user,
			MAC: r.FormValue("macAddr"), FramedIP: r.FormValue("framedIPAddress"), SessionTime: n("acctSessionTime"),
			InOctets:  n("acctInputGigawords")<<32 | n("acctInputOctets"),
			OutOctets: n("acctOutputGigawords")<<32 | n("acctOutputOctets")})
		if err != nil {
			slog.Error("radius rest: accounting", "err", err)
			jsonOut(w, 500, nil)
			return
		}
		jsonOut(w, 200, kv{"control:Auth-Type": "Accept", "reply:Reply-Message": "Saved"})
	default:
		// post-auth and unknown sections: old radius.php falls out of the switch with an empty 200
		jsonOut(w, 200, nil)
	}
}

// acceptBody is the old process_radiust_rest() attribute list.
func (s *Server) acceptBody(d radius.Decision) kv {
	pl := d.Plan
	b := kv{
		"control:Auth-Type":   "Accept",
		"reply":               kv{"Reply-Message": kv{"value": "success"}},
		"reply:Reply-Message": "success",
	}
	if pl.SharedUsers.Valid {
		b["Simultaneous-Use"] = pl.SharedUsers.Int64
	}
	if d.Rate != "" {
		b["reply:Mikrotik-Rate-Limit"] = d.Rate
		up, dn := bps(pl.RateUp.Int64, pl.RateUpUnit.String), bps(pl.RateDown.Int64, pl.RateDownUnit.String)
		b["reply:Ascend-Xmit-Rate"], b["reply:WISPr-Bandwidth-Max-Up"] = up, up
		b["reply:Ascend-Data-Rate"], b["reply:WISPr-Bandwidth-Max-Down"] = dn, dn
	}
	t := time.Unix(d.Expires, 0).In(s.location())
	b["reply:Mikrotik-Wireless-Comment"] = pl.PlanName + " | " + t.Format("2006-01-02 15:04:05")
	b["reply:expiration"] = t.Format("02 Jan 2006 15:04:05")
	b["reply:WISPr-Session-Terminate-Time"] = t.Format("2006-01-02T15:04:05-07:00")
	if d.Timeout > 0 {
		b["reply:Session-Timeout"] = d.Timeout // not in old PHP; makes the NAS drop the user at expiry
	}
	if pl.PlanType == "PPPoE" && pl.PoolName.Valid {
		b["reply:Framed-Pool"] = pl.PoolName.String
	}
	if d.HasTotal {
		b["reply:Mikrotik-Total-Limit"] = d.TotalLeft & 0xffffffff
		b["reply:Mikrotik-Total-Limit-Gigawords"] = d.TotalLeft >> 32
	}
	if pl.Limited == 1 && (pl.LimitType.String == "Time_Limit" || pl.LimitType.String == "Both_Limit") && pl.TimeLimit.Valid {
		tl := pl.TimeLimit.Int64 * 60
		if pl.TimeUnit.String == "Hrs" {
			tl *= 60
		}
		b["reply:Max-All-Session"] = tl
		if pl.LimitType.String == "Time_Limit" {
			b["reply:Expire-After"] = tl
		}
	}
	return b
}

// radiusServer returns the shared instance (so throttles persist across requests);
// tests without one get a single lazily built instance.
func (s *Server) radiusServer() *radius.Server {
	s.radiusOnce.Do(func() {
		if s.Radius != nil {
			return
		}
		s.Radius = &radius.Server{Q: s.queries, Key: s.SecretKey, Trusted: func() bool { return s.ClockWarning == nil || s.ClockWarning() == "" }}
		if s.Billing != nil {
			s.Radius.Redeem = s.Billing.RedeemVoucher
		}
	})
	return s.Radius
}
