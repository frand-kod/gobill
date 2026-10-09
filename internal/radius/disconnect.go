package radius

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

// StaleAfter is how long (seconds) an open session may go without an update before it is
// considered stale.
const StaleAfter = staleAfter

// CoAPort is the RFC 5176 Disconnect-Request port on the NAS.
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
		p := radius.New(radius.CodeDisconnectRequest, sec)
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
		addMA(p)
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		resp, err := radius.Exchange(ctx, p, net.JoinHostPort(s.NasIp, port))
		if err != nil {
			return err
		}
		if resp.Code != radius.CodeDisconnectACK {
			if ec, err := rfc5176ErrorCause(resp); err == nil {
				return fmt.Errorf("NAS %s refused disconnect: %v (Error-Cause %d %s)", s.NasIp, resp.Code, ec, errorCauses[ec])
			}
			return fmt.Errorf("NAS %s refused disconnect: %v", s.NasIp, resp.Code)
		}
		return nil
	}
	return fmt.Errorf("no NAS configured for %s", s.NasIp)
}
