package radius

import (
	"fmt"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

// fupSetup gives alice a 1 MB data limit on a RADIUS plan with a 1M/1M FUP bandwidth and used bytes of data.
func fupSetup(t *testing.T, device string, fup bool, used int) *env {
	t.Helper()
	e := setup(t)
	bw, err := e.q.CreateBandwidth(t.Context(), db.CreateBandwidthParams{Name: "fup", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	fb := "NULL"
	if fup {
		fb = fmt.Sprint(bw.ID)
	}
	if _, err := e.conn.Exec(fmt.Sprintf(`UPDATE plans SET device='%s', fup_bandwidth_id=%s, limited=1, limit_type='Data_Limit', data_limit=1, data_unit='MB';
		INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, stopped_at, input_octets) VALUES ('s', 'alice', '10.0.0.1', %d, %d, %d, %d)`,
		device, fb, e.now.Unix()-50, e.now.Unix()-10, e.now.Unix()-10, used)); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestFUPAuthorize(t *testing.T) {
	const over, under = 2_000_000, 100_000
	for _, c := range []struct {
		name   string
		device string
		fup    bool
		used   int
		accept bool
		rate   string
		total  bool
	}{
		{"no FUP past the limit is rejected as before", "Radius", false, over, false, "", false},
		{"FUP past the limit gets the FUP rate and no Total-Limit", "Radius", true, over, true, "1M/1M", false},
		{"FUP below the limit gets the normal rate and Total-Limit", "Radius", true, under, true, "512K/5M 10M/10M", true},
		{"FUP is ignored on a non-RADIUS device", "Dummy", true, over, false, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := fupSetup(t, c.device, c.fup, c.used)
			r := e.auth(t, pap("alice", "pw"))
			if (r.Code == radius.CodeAccessAccept) != c.accept {
				t.Fatalf("code %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
			}
			if !c.accept {
				if got := rfc2865.ReplyMessage_GetString(r); got != "You have exceeded your data limit." {
					t.Fatalf("reply %q", got)
				}
				return
			}
			if got, _ := mikrotik.MikrotikRateLimit_LookupString(r); got != c.rate {
				t.Fatalf("rate %q, want %q", got, c.rate)
			}
			if _, err := mikrotik.MikrotikTotalLimit_Lookup(r); (err == nil) != c.total {
				t.Fatalf("total limit present = %v, want %v", err == nil, c.total)
			}
		})
	}
}

// The job slows an online session once with a CoA-Request carrying the FUP rate.
func TestFUPJobSendsCoAOnce(t *testing.T) {
	e := fupSetup(t, "Radius", true, 2_000_000)
	sealed, _ := secret.Seal(e.s.Key, nasSecret)
	if _, err := e.q.CreateNAS(t.Context(), db.CreateNASParams{Name: "lo", Ip: "127.0.0.1", SecretEnc: sealed}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.conn.Exec(`UPDATE radius_sessions SET nas_ip='127.0.0.1', stopped_at=NULL`); err != nil {
		t.Fatal(err)
	}
	port, got := fakeNAS(t, radius.CodeCoAACK)
	run := FUPJob(e.q, e.s.Key, port)
	for i := 0; i < 2; i++ {
		if err := run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	p, err := radius.Parse(<-got, nasSecret)
	if err != nil || p.Code != radius.CodeCoARequest {
		t.Fatalf("want a CoA-Request: %v %v", p, err)
	}
	if rate, _ := mikrotik.MikrotikRateLimit_LookupString(p); rate != "1M/1M" {
		t.Fatalf("CoA rate %q", rate)
	}
	select {
	case <-got:
		t.Fatal("second run must not send again")
	default:
	}
}
