package radius

import (
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
)

// radius.php:262 text; hotspot login pages may show it.
func TestNoActivePlanMessage(t *testing.T) {
	e := setup(t)
	if _, err := e.conn.Exec("UPDATE subscriptions SET status='expired'"); err != nil {
		t.Fatal(err)
	}
	r := e.auth(t, pap("alice", "pw"))
	if got := rfc2865.ReplyMessage_GetString(r); r.Code != radius.CodeAccessReject || got != "Internet Plan Expired.." {
		t.Fatalf("%v %q", r.Code, got)
	}
}
