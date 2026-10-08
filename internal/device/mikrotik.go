package device

import (
	"context"
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-routeros/routeros/v3"
)

// Router is the connection info of one MikroTik. A driver is bound to one router,
// so the `router` argument of the Device methods is ignored.
type Router struct {
	Addr string // host:port, port 8728 (plain) or 8729 (TLS)
	User string
	Pass string
	TLS  bool
	Exec execFunc // test seam; nil means dial the router
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
	if r.TLS {
		// ponytail: RouterOS certs are usually self-signed, so no verification.
		c, err = routeros.DialTLSContext(ctx, r.Addr, r.User, r.Pass, &tls.Config{InsecureSkipVerify: true})
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
