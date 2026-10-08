package radius

import (
	"context"
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
		if ip4 := nasIP.To4(); ip4 != nil {
			rfc2865.NASIPAddress_Set(p, ip4)
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		resp, err := radius.Exchange(ctx, p, net.JoinHostPort(s.NasIp, port))
		if err != nil {
			return err
		}
		if resp.Code != radius.CodeDisconnectACK {
			return fmt.Errorf("NAS %s refused disconnect: %v", s.NasIp, resp.Code)
		}
		return nil
	}
	return fmt.Errorf("no NAS configured for %s", s.NasIp)
}
