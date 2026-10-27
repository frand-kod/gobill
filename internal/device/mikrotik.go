package device

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-routeros/routeros/v3"
)

// DialTCP, when set, carries the plain API connection (internal/wg dials routers inside the tunnel through it).
var DialTCP func(ctx context.Context, addr string) (net.Conn, error)

// Router is the connection info of one MikroTik. A driver is bound to one router,
// so the `router` argument of the Device methods is ignored.
type Router struct {
	Addr string // host:port, port 8728 (plain) or 8729 (TLS)
	User string
	Pass string
	TLS  bool
	// Pin is the stored SHA-256 fingerprint (hex) of the router's leaf certificate. Empty means
	// trust on first use: the first fingerprint seen is passed to OnPin and stored.
	Pin   string
	OnPin func(fp string)
	Exec  execFunc // test seam; nil means dial the router
}

// execFunc is the test seam: send one sentence, get the !re replies back.
type execFunc func(ctx context.Context, sentence []string) ([]map[string]string, error)

// ponytail: dials a new connection per call; add pooling if call volume ever matters.
func (r Router) exec(ctx context.Context, sentence []string) ([]map[string]string, error) {
	if r.Exec != nil {
		return r.Exec(ctx, sentence)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var c *routeros.Client
	var err error
	if DialTCP != nil && !r.TLS {
		var nc net.Conn
		if nc, err = DialTCP(ctx, r.Addr); err == nil {
			if c, err = routeros.NewClient(nc); err == nil {
				if err = c.LoginContext(ctx, r.User, r.Pass); err != nil {
					c.Close()
				}
			}
		}
	} else if r.TLS {
		// RouterOS certs are self-signed, so the chain is not verified; the leaf is pinned instead.
		c, err = routeros.DialTLSContext(ctx, r.Addr, r.User, r.Pass,
			&tls.Config{InsecureSkipVerify: true, VerifyConnection: r.verifyPin})
	} else {
		c, err = routeros.DialContext(ctx, r.Addr, r.User, r.Pass)
	}
	if err != nil {
		return nil, err
	}
	defer c.Close()
	rep, err := c.RunArgsContext(ctx, sentence)
	if err != nil {
		return nil, err
	}
	var out []map[string]string
	for _, s := range rep.Re {
		out = append(out, s.Map)
	}
	return out, nil
}

// CertFingerprint is the SHA-256 of the whole DER leaf certificate, lower-case hex.
func CertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// verifyPin is the tls.Config.VerifyConnection of API-SSL: it runs before the login is sent, so
// a mismatching (possibly man-in-the-middle) router never sees the password.
func (r Router) verifyPin(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("router sent no certificate")
	}
	got := CertFingerprint(cs.PeerCertificates[0])
	if r.Pin == "" {
		if r.OnPin != nil {
			r.OnPin(got)
		}
		return nil
	}
	if got != r.Pin {
		return fmt.Errorf("router certificate does not match the pinned one (expected %s, got %s); reset the certificate pin on the router page if the certificate was changed on purpose", r.Pin, got)
	}
	return nil
}

// firstID returns the .id of the first row of `path`/print matching key=val, or "".
func firstID(ctx context.Context, ex execFunc, path, key, val string) (string, error) {
	rows, err := ex(ctx, []string{path + "/print", "=.proplist=.id", "?" + key + "=" + val})
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0][".id"], nil
}

// removeWhere deletes the first row matching key=val; no match is not an error.
func removeWhere(ctx context.Context, ex execFunc, path, key, val string) error {
	id, err := firstID(ctx, ex, path, key, val)
	if err != nil || id == "" {
		return err
	}
	_, err = ex(ctx, []string{path + "/remove", "=numbers=" + id})
	return err
}

func (p Plan) rateLimit() string {
	if p.RateUp == 0 || p.RateDown == 0 {
		return ""
	}
	unit := func(u string) string {
		if u == "Kbps" {
			return "K"
		}
		return "M"
	}
	r := fmt.Sprintf("%d%s/%d%s", p.RateUp, unit(p.RateUpUnit), p.RateDown, unit(p.RateDownUnit))
	if b := strings.TrimSpace(p.Burst); b != "" {
		r += " " + b
	}
	return r
}

func (c Customer) pppUser() string {
	if c.PPPoEUsername != "" {
		return c.PPPoEUsername
	}
	return c.Username
}

func itoa(i int) string { return strconv.Itoa(i) }
