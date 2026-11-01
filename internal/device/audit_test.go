package device

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func sentAny(f *fake, sub string) bool {
	for _, s := range f.sent {
		if strings.Contains(strings.Join(s, " "), sub) {
			return true
		}
	}
	return false
}

// Router #3: re-sending an existing hotspot user deletes it first (counters reset, add failure = user gone).
func TestAuditRouter3_HotspotAddDeletes(t *testing.T) {
	d, f := hs(map[string]string{"/ip/hotspot/user|name=bob": "*1"})
	must(t, d.AddCustomer(ctx, cust, plan))
	if sentAny(f, "/ip/hotspot/user/remove") {
		t.Fatal("existing hotspot user removed and re-added instead of set in place")
	}
}

// Router #7: a TLS (8729) router ignores the WireGuard tunnel dialer.
func TestAuditRouter7_TLSIgnoresTunnel(t *testing.T) {
	t.Cleanup(func() { SetDialTCP(nil) })
	used := false
	SetDialTCP(func(context.Context, string) (net.Conn, error) { used = true; return nil, errors.New("tunnel") })
	Router{Addr: "127.0.0.1:1", TLS: true}.exec(ctx, []string{"/system/identity/print"})
	if !used {
		t.Fatal("TLS router dialed directly, bypassing the tunnel")
	}
}

// Router #7: the tunnel sets and clears the dial hook while jobs dial; `go test -race` must stay quiet.
func TestAuditRouter7_DialHookRace(t *testing.T) {
	t.Cleanup(func() { SetDialTCP(nil) })
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			SetDialTCP(func(context.Context, string) (net.Conn, error) { return nil, errors.New("x") })
			SetDialTCP(nil)
		}
	}()
	for i := 0; i < 200; i++ {
		if c, err := DialTCP(ctx, "127.0.0.1:1"); err == nil {
			c.Close()
		}
	}
	<-done
}

// Router #9: with pppoe_username set, a secret named after the gobill username is still taken over.
func TestAuditRouter9_SecretIDHijack(t *testing.T) {
	d, f := pp(map[string]string{"/ppp/secret|name=bob": "*9"}) // someone else's secret named "bob"
	c := cust
	c.PPPoEUsername = "bob-ppp"
	must(t, d.AddCustomer(ctx, c, plan))
	if sentAny(f, "=numbers=*9") {
		t.Fatal("secret named after Username was rewritten although PPPoEUsername is set")
	}
}

// Router #11: AddPlan omits on-login/on-up scripts that UpdatePlan sends.
func TestAuditRouter11_AddPlanScripts(t *testing.T) {
	p := plan
	p.OnLogin, p.OnLogout = ":log info up", ":log info down"
	h, fh := hs(nil)
	must(t, h.AddPlan(ctx, p))
	o, fo := pp(nil)
	must(t, o.AddPlan(ctx, p))
	if !sentAny(fh, "=on-login=") || !sentAny(fo, "=on-up=") {
		t.Fatal("AddPlan dropped the plan scripts")
	}
}
