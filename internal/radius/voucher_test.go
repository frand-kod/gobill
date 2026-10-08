package radius

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

// voucherEnv: a fake Redeem with the same atomic claim as billing.RedeemVoucher.
func voucherEnv(t *testing.T) (*env, *int32) {
	e := setup(t)
	var plan int64
	e.conn.QueryRow(`SELECT id FROM plans LIMIT 1`).Scan(&plan)
	var n int32
	e.s.Redeem = func(ctx context.Context, code string, cid int64) error {
		v, err := e.q.GetVoucherByCode(ctx, code)
		if err != nil {
			return err
		}
		if r, _ := e.q.UseVoucher(ctx, db.UseVoucherParams{UsedBy: sql.NullInt64{Int64: cid, Valid: true}, ID: v.ID}); r == 0 {
			return errors.New("used")
		}
		atomic.AddInt32(&n, 1)
		_, err = e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: cid, PlanID: plan, Type: "Hotspot",
			StartedAt: e.now.Unix(), ExpiresAt: e.now.Unix() + 3600})
		return err
	}
	for _, c := range []string{"V1", "V2", "V3"} {
		e.q.CreateVoucher(context.Background(), db.CreateVoucherParams{Code: c, PlanID: plan})
	}
	return e, &n
}

func papMAC(user, pass, mac string) *radius.Packet {
	p := pap(user, pass)
	rfc2865.CallingStationID_SetString(p, mac)
	return p
}

func TestVoucherLogin(t *testing.T) {
	e, n := voucherEnv(t)
	r := e.auth(t, papMAC("V1", "V1", "m1"))
	if r.Code != radius.CodeAccessAccept || rfc2865.SessionTimeout_Get(r) != 3600 {
		t.Fatalf("first: %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
	}
	if c, err := e.q.GetCustomerByUsername(context.Background(), "V1"); err != nil || c.ServiceType != "Hotspot" {
		t.Fatalf("customer %v %v", c, err)
	}
	// same code again, username==password and empty password
	for _, p := range []*radius.Packet{papMAC("V1", "V1", "m1"), papMAC("V1", "", "m1")} {
		if r := e.auth(t, p); r.Code != radius.CodeAccessAccept {
			t.Fatalf("again: %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
		}
	}
	if *n != 1 {
		t.Fatalf("activations %d", *n)
	}
	if r := e.auth(t, papMAC("V1", "nope", "m1")); r.Code != radius.CodeAccessReject {
		t.Fatal("wrong pw accepted")
	}
	if r := e.auth(t, papMAC("ZZ", "ZZ", "m1")); rfc2865.ReplyMessage_GetString(r) != "Invalid Voucher.." {
		t.Fatalf("unknown: %s", rfc2865.ReplyMessage_GetString(r))
	}
}

func TestVoucherConcurrent(t *testing.T) {
	e, n := voucherEnv(t)
	var wg sync.WaitGroup
	var acc int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc := &rec{}
			e.s.HandleAuth(rc, &radius.Request{Packet: papMAC("V2", "V2", "m2"), RemoteAddr: &net.UDPAddr{IP: net.ParseIP("10.0.0.1")}})
			if rc.p.Code == radius.CodeAccessAccept {
				atomic.AddInt32(&acc, 1)
			}
		}()
	}
	wg.Wait()
	if *n != 1 || acc == 0 {
		t.Fatalf("activations %d accepted %d", *n, acc)
	}
}

func TestVoucherUsedExpired(t *testing.T) {
	e, _ := voucherEnv(t)
	e.auth(t, papMAC("V3", "V3", "m3"))
	e.now = e.now.Add(2 * time.Hour)
	if r := e.auth(t, papMAC("V3", "V3", "m3")); r.Code != radius.CodeAccessReject {
		t.Fatalf("expired voucher: %v", r.Code)
	}
	e.conn.Exec(`UPDATE subscriptions SET status='expired'`)
	if r := e.auth(t, papMAC("V3", "V3", "m3")); rfc2865.ReplyMessage_GetString(r) != "Voucher Expired..." {
		t.Fatalf("msg %q", rfc2865.ReplyMessage_GetString(r))
	}
}

func TestVoucherThrottle(t *testing.T) {
	e, _ := voucherEnv(t)
	for i := 0; i < voucherMaxFails; i++ {
		e.auth(t, papMAC(fmt.Sprintf("X%d", i), fmt.Sprintf("X%d", i), "mt"))
	}
	if r := e.auth(t, papMAC("V1", "V1", "mt")); rfc2865.ReplyMessage_GetString(r) != "Too many attempts, try again later" {
		t.Fatalf("not throttled: %s", rfc2865.ReplyMessage_GetString(r))
	}
	if r := e.auth(t, papMAC("V1", "V1", "other")); r.Code != radius.CodeAccessAccept {
		t.Fatal("other MAC throttled")
	}
	e.now = e.now.Add(16 * time.Minute)
	if r := e.auth(t, papMAC("V1", "V1", "mt")); r.Code != radius.CodeAccessAccept {
		t.Fatal("window did not expire")
	}
}
