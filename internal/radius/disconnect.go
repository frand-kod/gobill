package radius

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

// CoAPort is the RFC 5176 Disconnect-/CoA-Request port on the NAS.
const CoAPort = "3799"

// errorCauses are the RFC 5176 Error-Cause values worth naming.
var errorCauses = map[uint32]string{
	201: "Residual-Session-Context-Removed", 202: "Invalid-EAP-Packet", 401: "Unsupported-Attribute",
	402: "Missing-Attribute", 403: "NAS-Identification-Mismatch", 404: "Invalid-Request",
	405: "Unsupported-Service", 406: "Unsupported-Extension", 407: "Invalid-Attribute-Value",
	501: "Administratively-Prohibited", 502: "Request-Not-Routable", 503: "Session-Context-Not-Found",
	504: "Session-Context-Not-Removable", 505: "Other-Proxy-Processing-Error", 506: "Resources-Unavailable",
	507: "Request-Initiated", 508: "Multiple-Session-Selection-Unsupported",
}

// rfc5176ErrorCause reads Error-Cause (type 101) from a reply.
func rfc5176ErrorCause(p *radius.Packet) (uint32, error) {
	a, ok := p.Lookup(101)
	if !ok || len(a) != 4 {
		return 0, fmt.Errorf("no Error-Cause")
	}
	return binary.BigEndian.Uint32(a), nil
}

// Disconnect sends a Disconnect-Request for one session to its NAS and waits for the ACK.
// The secret is the one of the nas row covering the session's NAS IP.
func Disconnect(ctx context.Context, q *db.Queries, key []byte, port string, s db.RadiusSession) error {
	return request(ctx, q, key, port, s, radius.CodeDisconnectRequest, nil)
}

// CoA sends a CoA-Request (RFC 5176) that sets the session's Mikrotik-Rate-Limit and Session-Timeout
// from the customer's active plan, as Access-Accept would now. plan != "": only when the active plan
// has that name (nil, nothing sent, otherwise). No active plan: nothing sent.
func CoA(ctx context.Context, q *db.Queries, key []byte, port string, s db.RadiusSession, plan string, now time.Time) error {
	lr, err := q.GetRadiusLogin(ctx, s.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if plan != "" && lr.PlanName != plan {
		return nil
	}
	rate := rateLimit(lr.RateUp, lr.RateDown, lr.RateUpUnit, lr.RateDownUnit, lr.Burst)
	timeout := lr.ExpiresAt - now.Unix()
	// ponytail: a time-limited plan keeps the NAS's current Session-Timeout (the remaining quota needs
	// the usage sum of authorize); it is recomputed at the next login.
	if lr.Limited == 1 && (lr.LimitType.String == "Time_Limit" || lr.LimitType.String == "Both_Limit") {
		timeout = 0
	}
	return request(ctx, q, key, port, s, radius.CodeCoARequest, func(p *radius.Packet) {
		if rate != "" {
			mikrotik.MikrotikRateLimit_SetString(p, rate)
		}
		if timeout > 0 {
			rfc2865.SessionTimeout_Set(p, rfc2865.SessionTimeout(min(timeout, 1<<32-1)))
		}
	})
}

// request sends a Disconnect- or CoA-Request for s, set adds the request's own attributes.
func request(ctx context.Context, q *db.Queries, key []byte, port string, s db.RadiusSession, code radius.Code, set func(*radius.Packet)) error {
	what, ack := "disconnect", radius.CodeDisconnectACK
	if code == radius.CodeCoARequest {
		what, ack = "CoA", radius.CodeCoAACK
	}
	if port == "" {
		port = CoAPort
	}
	nasIP := net.ParseIP(s.NasIp)
	rows, err := q.ListNAS(ctx)
	if err != nil {
		return err
	}
	for _, n := range rows {
		if nasIP == nil || !matchIP(n.Ip, nasIP) {
			continue
		}
		sec, err := secret.Open(key, n.SecretEnc)
		if err != nil {
			return err
		}
		p := radius.New(code, sec)
		rfc2865.UserName_SetString(p, s.Username)
		rfc2866.AcctSessionID_SetString(p, s.SessionID)
		// Identify the session to the NAS as it knows itself (RFC 5176 §3): the NAS-IP-Address
		// attribute it reported, which may differ from the packet source IP. Unknown = omit.
		if ip := net.ParseIP(s.NasIpAttr); ip != nil && ip.To4() != nil {
			rfc2865.NASIPAddress_Set(p, ip.To4())
		}
		if s.NasIdentifier != "" {
			rfc2865.NASIdentifier_SetString(p, s.NasIdentifier)
		}
		if ip := net.ParseIP(s.FramedIp); ip != nil && ip.To4() != nil {
			rfc2865.FramedIPAddress_Set(p, ip.To4())
		}
		if set != nil {
			set(p)
		}
		addMA(p)
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		resp, err := exchange(ctx, p, net.JoinHostPort(s.NasIp, port))
		if err != nil {
			return err
		}
		if resp.Code != ack {
			if ec, err := rfc5176ErrorCause(resp); err == nil {
				return fmt.Errorf("NAS %s refused %s: %v (Error-Cause %d %s)", s.NasIp, what, resp.Code, ec, errorCauses[ec])
			}
			return fmt.Errorf("NAS %s refused %s: %v", s.NasIp, what, resp.Code)
		}
		return nil
	}
	return fmt.Errorf("no NAS configured for %s", s.NasIp)
}

// ProbeCoA sends a Disconnect-Request for a session that does not exist to the NAS at ip and reports
// whether the NAS answered at all. An ACK or a NAK both prove the path, the CoA port and the secret.
func ProbeCoA(ctx context.Context, q *db.Queries, key []byte, ip string) error {
	nasIP := net.ParseIP(ip)
	rows, err := q.ListNAS(ctx)
	if err != nil {
		return err
	}
	for _, n := range rows {
		if nasIP == nil || !matchIP(n.Ip, nasIP) {
			continue
		}
		sec, err := secret.Open(key, n.SecretEnc)
		if err != nil {
			return err
		}
		p := radius.New(radius.CodeDisconnectRequest, sec)
		rfc2865.UserName_SetString(p, "gobill-probe")
		rfc2866.AcctSessionID_SetString(p, "gobill-probe")
		addMA(p)
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		resp, err := exchange(ctx, p, net.JoinHostPort(ip, CoAPort))
		if err != nil {
			return err
		}
		if resp.Code != radius.CodeDisconnectACK && resp.Code != radius.CodeDisconnectNAK {
			return fmt.Errorf("unexpected reply %v", resp.Code)
		}
		return nil
	}
	return fmt.Errorf("no NAS configured for %s", ip)
}

// DialUDP, when set, carries CoA and Disconnect requests (internal/wg dials routers inside the tunnel through it).
var DialUDP func(ctx context.Context, addr string) (net.Conn, error)

// exchange is radius.Exchange, over DialUDP when a tunnel is set.
func exchange(ctx context.Context, p *radius.Packet, addr string) (*radius.Packet, error) {
	if DialUDP == nil {
		return radius.Exchange(ctx, p, addr)
	}
	wire, err := p.Encode()
	if err != nil {
		return nil, err
	}
	c, err := DialUDP(ctx, addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	if _, err := c.Write(wire); err != nil {
		return nil, err
	}
	buf := make([]byte, radius.MaxPacketLength)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	return radius.Parse(buf[:n], p.Secret)
}
