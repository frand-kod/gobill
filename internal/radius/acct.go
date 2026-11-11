package radius

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"strings"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2869"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
)

// InterimDefault is the Acct-Interim-Interval sent when the radius_interim_interval setting is unset.
const InterimDefault = 300

// interimInterval is the radius_interim_interval setting: seconds, 0 = do not send, unset = InterimDefault.
func (s *Server) interimInterval(ctx context.Context) int64 {
	v := s.store().Get(ctx, "radius_interim_interval")
	if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n >= 0 {
		return min(n, 1<<32-1)
	}
	return InterimDefault
}

// AcctRequest is the transport-neutral accounting input.
type AcctRequest struct {
	Type                 rfc2866.AcctStatusType
	NAS, SessionID, User string
	NASIPAttr, NASID     string // NAS-IP-Address / NAS-Identifier as the NAS reports them
	MAC, FramedIP        string
	SessionTime          int64
	NASPortID            string // NAS-Port-Id as the NAS reports it
	TerminateCause       string // Acct-Terminate-Cause by name, Stop only
	InOctets, OutOctets  int64  // gigawords already folded in
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
		err := s.commit(ctx, func(q *db.Queries) error { return q.CloseRadiusSessionsByNAS(ctx, arg) })
		s.octs.reset() // the closed sessions changed behind the cache
		return err
	case rfc2866.AcctStatusType_Value_Start, rfc2866.AcctStatusType_Value_InterimUpdate, rfc2866.AcctStatusType_Value_Stop:
		// gigawords << 32 goes negative from 2^31 GB up, and a REST value can be negative. Clamp, do not
		// refuse: a dropped Stop would leave the session open. MAX() in the upsert keeps the old value.
		if a.InOctets < 0 || a.OutOctets < 0 {
			slog.Warn("radius: negative octet counter clamped to 0", "user", a.User, "nas", a.NAS)
			a.InOctets, a.OutOctets = max(a.InOctets, 0), max(a.OutOctets, 0)
		}
		arg := db.UpsertRadiusSessionParams{SessionID: a.SessionID, Username: a.User, NasIp: a.NAS, NasIpAttr: a.NASIPAttr, NasIdentifier: a.NASID, FramedIp: a.FramedIP,
			Mac: a.MAC, StartedAt: now - a.SessionTime, UpdatedAt: now, InputOctets: a.InOctets, OutputOctets: a.OutOctets, TerminateCause: a.TerminateCause, NasPortID: a.NASPortID}
		noteInterim(a.NAS, a.SessionID, a.Type, now)
		if a.Type == rfc2866.AcctStatusType_Value_Stop {
			arg.StoppedAt = sql.NullInt64{Int64: now, Valid: true}
		}
		day := s.now().In(s.loc()).Format("2006-01-02")
		return s.commit(ctx, func(q *db.Queries) error {
			// The counters only grow (MAX in the upsert), so the growth since the last packet is what
			// moved today and what the subscription used. A session the NAS restarted has a new
			// session_id and counts from 0.
			old, err := s.octs.get(ctx, q, arg.NasIp, arg.SessionID)
			if err != nil {
				return err
			}
			if err := q.UpsertRadiusSession(ctx, arg); err != nil {
				return err
			}
			cur := old.next(arg.InputOctets, arg.OutputOctets, arg.StartedAt, arg.UpdatedAt, arg.StoppedAt)
			s.octs.put(arg.NasIp, arg.SessionID, cur)
			in, out, secs := cur.in-old.in, cur.out-old.out, max(cur.secs-old.secs, 0)
			if in > 0 || out > 0 {
				if err := q.AddRadiusUsageDaily(ctx, db.AddRadiusUsageDailyParams{Username: arg.Username, Day: day, InputOctets: in, OutputOctets: out}); err != nil {
					return err
				}
			}
			if in == 0 && out == 0 && secs == 0 {
				return nil
			}
			return s.usage().AddUsage(ctx, q, arg.Username, in, out, secs, cur.started)
		})
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
	a.NASPortID = rfc2869.NASPortID_GetString(p)
	if c, err := rfc2866.AcctTerminateCause_Lookup(p); err == nil {
		a.TerminateCause = c.String()
	}
	if err := s.Account(r.Context(), a); err != nil {
		slog.Error("radius: accounting", "err", err)
		return // no response: NAS retries
	}
	w.Write(r.Response(radius.CodeAccountingResponse))
}
