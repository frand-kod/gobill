package radius

import (
	"fmt"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/vendors/mikrotik"
)

// Usage under both login names (username and pppoe_username) counts against one quota, and a
// time carry (early renewal) raises the time total.
func TestLimitCountsBothLoginNames(t *testing.T) {
	e := setup(t)
	now := e.now.Unix()
	if _, err := e.conn.Exec(fmt.Sprintf(`UPDATE customers SET pppoe_username='alice-ppp';
		UPDATE plans SET limited=1, limit_type='Both_Limit', time_limit=30, time_unit='Mins', data_limit=1, data_unit='MB';
		UPDATE subscriptions SET started_at=%d, quota_carry_seconds=600;
		INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, stopped_at, input_octets) VALUES ('p', 'alice-ppp', '10.0.0.1', %d, %d, %d, 400000);
		INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, stopped_at, input_octets) VALUES ('u', 'alice', '10.0.0.1', %d, %d, %d, 100000)`,
		now-5000, now-3000, now-2000, now-2000, now-1500, now-1000, now-1000)); err != nil {
		t.Fatal(err)
	}
	// time: 1800+600 total, 1000+500 used = 900 left; data: 1048576-500000 left
	for _, login := range []string{"alice", "alice-ppp"} {
		r := e.auth(t, pap(login, "pw"))
		if got := rfc2865.SessionTimeout_Get(r); r.Code != radius.CodeAccessAccept || got != 900 {
			t.Fatalf("%s: %v timeout %d", login, r.Code, got)
		}
		if got, _ := mikrotik.MikrotikTotalLimit_Lookup(r); got != 1048576-500000 {
			t.Fatalf("%s: total limit %d", login, got)
		}
	}
}
