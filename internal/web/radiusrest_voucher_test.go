package web

import (
	"encoding/json"
	"net/url"
	"strconv"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/device"
)

func TestRadiusRestVoucher(t *testing.T) {
	s, q := restSetup(t, false)
	s.Billing = &billing.Service{DB: s.conn, Q: q, Key: s.SecretKey,
		DeviceFor: func(db.Plan, db.Router) (device.Device, error) { return device.Dummy{}, nil }}
	var pid int64
	s.conn.QueryRow(`SELECT id FROM plans LIMIT 1`).Scan(&pid)
	q.CreateVoucher(t.Context(), db.CreateVoucherParams{Code: "RV1", PlanID: pid})
	h := s.Handler()
	form := func(pw string) url.Values {
		return url.Values{"username": {"RV1"}, "password": {pw}, "macAddr": {"aa:bb"}}
	}
	for _, pw := range []string{"RV1", ""} { // first activates, second reuses
		w := post(h, "/radius.php?action=authorize", form(pw), "")
		var b map[string]any
		json.Unmarshal(w.Body.Bytes(), &b)
		if w.Code != 200 || b["reply:Session-Timeout"] == nil {
			t.Fatalf("pw %q: %d %s", pw, w.Code, w.Body)
		}
	}
	var n int
	s.conn.QueryRow(`SELECT count(*) FROM subscriptions s JOIN customers c ON c.id=s.customer_id WHERE c.username='RV1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("subscriptions %d", n)
	}
	if w := post(h, "/radius/rest?action=authenticate", form("RV1"), ""); w.Code != 204 {
		t.Fatalf("authenticate %d", w.Code)
	}
	for i := 0; i < 10; i++ {
		c := "BAD" + strconv.Itoa(i)
		post(h, "/radius.php?action=authorize", url.Values{"username": {c}, "password": {c}, "macAddr": {"cc:dd"}}, "")
	}
	w := post(h, "/radius.php?action=authorize", url.Values{"username": {"RV1"}, "password": {"RV1"}, "macAddr": {"cc:dd"}}, "")
	if w.Code != 401 {
		t.Fatalf("throttle %d %s", w.Code, w.Body)
	}
}
