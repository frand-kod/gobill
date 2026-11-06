package radius

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

// setup's clock is Monday 1970-01-12 13:46:40 UTC; the subscription ends in 3600 s.
func TestLoginWindow(t *testing.T) {
	for i, c := range []struct {
		window  string
		allowed bool
		timeout uint32
	}{
		{"", true, 3600},
		{"Mon 13:00-14:00", true, 800},      // capped to the window end (14:00:00)
		{"Mon-Fri 08:00-17:00", true, 3600}, // the window ends after the plan: not extended
		{"Sun 22:00-14:00", true, 800},      // started Sunday, runs past midnight into Monday
		{"* 22:00-06:00", false, 0},         // night window, it is afternoon
		{"Mon 15:00-16:00", false, 0},       // later today
		{"Tue-Sun 00:00-24:00", false, 0},   // not on Mondays
		{"Mon 08:00-12:00; Mon 13:30-14:00", true, 800},
	} {
		e := setup(t)
		if _, err := e.conn.Exec("UPDATE plans SET login_window = ?", c.window); err != nil {
			t.Fatal(err)
		}
		r := e.auth(t, pap("alice", "pw"))
		if (r.Code == radius.CodeAccessAccept) != c.allowed {
			t.Fatalf("%d %q: code %v %q", i, c.window, r.Code, rfc2865.ReplyMessage_GetString(r))
		}
		if !c.allowed {
			if msg := rfc2865.ReplyMessage_GetString(r); !strings.HasPrefix(msg, "Login not allowed at this time") {
				t.Fatalf("%d %q: reply %q", i, c.window, msg)
			}
			continue
		}
		if got := uint32(rfc2865.SessionTimeout_Get(r)); got != c.timeout {
			t.Fatalf("%d %q: Session-Timeout %d, want %d", i, c.window, got, c.timeout)
		}
	}
}

func TestParseWindowErrors(t *testing.T) {
	for _, bad := range []string{"Mon", "Mon 25:00-26:00", "Foo 08:00-09:00", "08:00-08:00", "Mon 8-9", "Mon 08:00-09:60"} {
		if _, err := ParseWindow(bad); err == nil {
			t.Errorf("%q must not parse", bad)
		}
	}
	if r, err := ParseWindow("Fri-Mon 08:00-17:00"); err != nil || len(r) != 1 || r[0].days != 1<<5|1<<6|1<<0|1<<1 {
		t.Errorf("wrapping day range: %v %v", r, err)
	}
}

func boostSetup(t *testing.T, window string) *env {
	t.Helper()
	e := setup(t)
	bw, err := e.q.CreateBandwidth(t.Context(), db.CreateBandwidthParams{Name: "boost", RateDown: 20, RateDownUnit: "Mbps", RateUp: 20, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.conn.Exec(fmt.Sprintf("UPDATE plans SET device='Radius', boost_bandwidth_id=%d, boost_window=?", bw.ID), window); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestBoostRateAtAuth(t *testing.T) {
	for window, want := range map[string]string{"Mon 13:00-14:00": "20M/20M", "Mon 15:00-16:00": "512K/5M 10M/10M"} {
		e := boostSetup(t, window)
		if got, _ := mikrotik.MikrotikRateLimit_LookupString(e.auth(t, pap("alice", "pw"))); got != want {
			t.Errorf("%q: rate %q, want %q", window, got, want)
		}
	}
}

// A session that started inside the boost window is moved back by one CoA once the window is over.
func TestBoostJobSendsCoAAtWindowEnd(t *testing.T) {
	e := boostSetup(t, "Mon 13:00-14:00")
	if boostOn("Mon 13:00-14:00", time.Now().UTC()) {
		t.Skip("the real clock is inside the test window")
	}
	sealed, _ := secret.Seal(e.s.Key, nasSecret)
	if _, err := e.q.CreateNAS(t.Context(), db.CreateNASParams{Name: "lo", Ip: "127.0.0.1", SecretEnc: sealed}); err != nil {
		t.Fatal(err)
	}
	// subscription valid now, session started in the window
	if _, err := e.conn.Exec(fmt.Sprintf("UPDATE subscriptions SET expires_at = %d; INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at) VALUES ('s', 'alice', '127.0.0.1', %d, %d)",
		time.Now().Unix()+3600, e.now.Unix()-50, e.now.Unix())); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.RecountSubscriptionUsage(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	port, got := fakeNAS(t, radius.CodeCoAACK)
	run := BoostJob(e.q, e.s.Key, port)
	for i := 0; i < 2; i++ {
		if err := run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	p, err := radius.Parse(<-got, nasSecret)
	if err != nil || p.Code != radius.CodeCoARequest {
		t.Fatalf("want a CoA-Request: %v %v", p, err)
	}
	if rate, _ := mikrotik.MikrotikRateLimit_LookupString(p); rate != "512K/5M 10M/10M" {
		t.Fatalf("CoA rate %q", rate)
	}
	select {
	case <-got:
		t.Fatal("second run must not send again")
	default:
	}
}
