package device

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// fake records sentences; print replies come from ids keyed by "path|key=val".
type fake struct {
	sent [][]string
	ids  map[string]string
}

func (f *fake) exec(_ context.Context, s []string) ([]map[string]string, error) {
	f.sent = append(f.sent, s)
	if strings.HasSuffix(s[0], "/print") {
		if id := f.ids[strings.TrimSuffix(s[0], "/print")+"|"+s[2][1:]]; id != "" {
			return []map[string]string{{".id": id}}, nil
		}
	}
	return nil, nil
}

func (f *fake) check(t *testing.T, want ...string) {
	t.Helper()
	var got []string
	for _, s := range f.sent {
		got = append(got, strings.Join(s, " "))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

var (
	cust = Customer{Username: "bob", Password: "pw", FullName: "Bob B", Email: "b@x", Bills: []string{"Gold", "Silver"}}
	plan = Plan{Name: "Gold", SharedUsers: 2, RateUp: 512, RateUpUnit: "Kbps", RateDown: 2, RateDownUnit: "Mbps", Burst: " 3M/4M "}
	ctx  = context.Background()
)

func hs(ids map[string]string) (*MikrotikHotspot, *fake) {
	f := &fake{ids: ids}
	return &MikrotikHotspot{ex: f.exec}, f
}

func pp(ids map[string]string) (*MikrotikPPPoE, *fake) {
	f := &fake{ids: ids}
	return &MikrotikPPPoE{ex: f.exec}, f
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRateLimit(t *testing.T) {
	if g := plan.rateLimit(); g != "512K/2M 3M/4M" {
		t.Fatal(g)
	}
	if (Plan{RateUp: 0, RateDown: 5}).rateLimit() != "" {
		t.Fatal("zero rate must give empty limit")
	}
}

func TestHotspotAddCustomer(t *testing.T) {
	d, f := hs(map[string]string{"/ip/hotspot/user|name=bob": "*1"})
	must(t, d.AddCustomer(ctx, cust, plan))
	f.check(t,
		"/ip/hotspot/user/print =.proplist=.id ?name=bob",
		"/ip/hotspot/user/remove =numbers=*1",
		"/ip/hotspot/user/add =name=bob =profile=Gold =password=pw =comment=Bob B | Gold, Silver =email=b@x")
}

func TestHotspotAddCustomerLimitsAndExpired(t *testing.T) {
	d, f := hs(nil)
	p := plan
	p.Limited, p.LimitType, p.TimeUnit, p.TimeLimit, p.DataUnit, p.DataLimit = true, "Both_Limit", "Hrs", 5, "GB", 2
	p.IsExpiredProfile = true
	must(t, d.AddCustomer(ctx, cust, p))
	f.check(t,
		"/ip/hotspot/user/print =.proplist=.id ?name=bob",
		"/ip/hotspot/active/print =.proplist=.id ?user=bob",
		"/ip/hotspot/user/add =name=bob =profile=Gold =password=pw =comment=Bob B | Gold, Silver =email=b@x =limit-uptime=5:00:00 =limit-bytes-total=2000000000")
}

func TestHotspotRemoveCustomer(t *testing.T) {
	d, f := hs(map[string]string{"/ip/hotspot/user|name=bob": "*1", "/ip/hotspot/active|user=bob": "*9"})
	must(t, d.RemoveCustomer(ctx, cust, plan))
	f.check(t,
		"/ip/hotspot/user/print =.proplist=.id ?name=bob",
		"/ip/hotspot/user/remove =numbers=*1",
		"/ip/hotspot/active/print =.proplist=.id ?user=bob",
		"/ip/hotspot/active/remove =numbers=*9")
}

func TestHotspotRemoveCustomerExpiredPlan(t *testing.T) {
	d, f := hs(nil)
	p := plan
	p.ExpiredPlan = &Plan{Name: "Expired"}
	must(t, d.RemoveCustomer(ctx, cust, p))
	f.check(t,
		"/ip/hotspot/user/print =.proplist=.id ?name=bob",
		"/ip/hotspot/active/print =.proplist=.id ?user=bob",
		"/ip/hotspot/user/add =name=bob =profile=Expired =password=pw =comment=Bob B | Gold, Silver =email=b@x",
		"/ip/hotspot/active/print =.proplist=.id ?user=bob")
}

func TestHotspotPlans(t *testing.T) {
	d, f := hs(map[string]string{"/ip/hotspot/user/profile|name=Old": "*5"})
	p := plan
	p.OnLogin = "x"
	must(t, d.AddPlan(ctx, p))
	must(t, d.UpdatePlan(ctx, "Old", p))
	must(t, d.UpdatePlan(ctx, "Missing", p))
	must(t, d.RemovePlan(ctx, Plan{Name: "Old"}))
	f.check(t,
		"/ip/hotspot/user/profile/add =name=Gold =shared-users=2 =rate-limit=512K/2M 3M/4M",
		"/ip/hotspot/user/profile/print =.proplist=.id ?name=Old",
		"/ip/hotspot/user/profile/set =numbers=*5 =name=Gold =shared-users=2 =rate-limit=512K/2M 3M/4M =on-login=x =on-logout=",
		"/ip/hotspot/user/profile/print =.proplist=.id ?name=Missing",
		"/ip/hotspot/user/profile/add =name=Gold =shared-users=2 =rate-limit=512K/2M 3M/4M",
		"/ip/hotspot/user/profile/print =.proplist=.id ?name=Old",
		"/ip/hotspot/user/profile/remove =numbers=*5")
}

func TestHotspotOnlineConnectDisconnect(t *testing.T) {
	d, f := hs(map[string]string{"/ip/hotspot/active|user=bob": "*9"})
	on, err := d.IsOnline(ctx, cust, "r")
	if err != nil || !on {
		t.Fatal(on, err)
	}
	on, _ = d.IsOnline(ctx, Customer{Username: "al"}, "r")
	if on {
		t.Fatal("al must be offline")
	}
	must(t, d.Disconnect(ctx, cust, "r"))
	must(t, d.Connect(ctx, cust, "1.2.3.4", "AA:BB", "r"))
	f.check(t,
		"/ip/hotspot/active/print =.proplist=.id ?user=bob",
		"/ip/hotspot/active/print =.proplist=.id ?user=al",
		"/ip/hotspot/active/print =.proplist=.id ?user=bob",
		"/ip/hotspot/active/remove =numbers=*9",
		"/ip/hotspot/active/login =user=bob =password=pw =ip=1.2.3.4 =mac-address=AA:BB")
}

func TestHotspotChangeUsername(t *testing.T) {
	d, f := hs(map[string]string{"/ip/hotspot/user|name=bob": "*1"})
	must(t, d.ChangeUsername(ctx, plan, "bob", "rob"))
	f.check(t,
		"/ip/hotspot/user/print =.proplist=.id ?name=bob",
		"/ip/hotspot/user/set =numbers=*1 =name=rob",
		"/ip/hotspot/active/print =.proplist=.id ?user=bob")
}

func TestPPPoEAddCustomerNew(t *testing.T) {
	d, f := pp(nil)
	c := cust
	c.PPPoEUsername, c.PPPoEPassword, c.PPPoEIP = "bob-ppp", "ppw", "10.0.0.5"
	must(t, d.AddCustomer(ctx, c, plan))
	f.check(t,
		"/ppp/secret/print =.proplist=.id ?name=bob",
		"/ppp/secret/print =.proplist=.id ?name=bob-ppp",
		"/ppp/secret/add =service=pppoe =profile=Gold =comment=Bob B | b@x | Gold, Silver =password=ppw =name=bob-ppp =remote-address=10.0.0.5")
}

func TestPPPoEAddCustomerExistingExpired(t *testing.T) {
	d, f := pp(map[string]string{"/ppp/secret|name=bob": "*2"})
	c := cust
	c.PPPoEIP = "10.0.0.5"
	p := plan
	p.IsExpiredProfile = true
	must(t, d.AddCustomer(ctx, c, p))
	f.check(t,
		"/ppp/secret/print =.proplist=.id ?name=bob",
		"/ppp/secret/set =numbers=*2 =password=pw =name=bob =profile=Gold =comment=Bob B | b@x | Gold, Silver",
		"/ppp/secret/unset =.id=*2 =value-name=remote-address")
}

func TestPPPoERemoveCustomer(t *testing.T) {
	d, f := pp(map[string]string{"/ppp/secret|name=bob": "*2", "/ppp/active|name=bob": "*7"})
	must(t, d.RemoveCustomer(ctx, cust, plan))
	f.check(t,
		"/ppp/secret/print =.proplist=.id ?name=bob",
		"/ppp/secret/remove =numbers=*2",
		"/ppp/active/print =.proplist=.id ?name=bob",
		"/ppp/active/remove =numbers=*7")
}

func TestPPPoERemoveCustomerExpiredPlan(t *testing.T) {
	d, f := pp(map[string]string{"/ppp/secret|name=bob": "*2"})
	p := plan
	p.ExpiredPlan = &Plan{Name: "Expired"}
	c := cust
	c.PPPoEIP = "10.0.0.5"
	must(t, d.RemoveCustomer(ctx, c, p))
	f.check(t,
		"/ppp/secret/print =.proplist=.id ?name=bob",
		"/ppp/secret/set =numbers=*2 =password=pw =name=bob =profile=Expired =comment=Bob B | b@x | Gold, Silver",
		"/ppp/secret/unset =.id=*2 =value-name=remote-address",
		"/ppp/active/print =.proplist=.id ?name=bob")
}

func TestPPPoEPlans(t *testing.T) {
	d, f := pp(map[string]string{"/ppp/profile|name=Old": "*5"})
	p := plan
	p.Pool, p.PoolLocalIP = "pool1", "10.0.0.1"
	must(t, d.AddPlan(ctx, p))
	must(t, d.UpdatePlan(ctx, "Old", p))
	p.PoolLocalIP = ""
	must(t, d.UpdatePlan(ctx, "Nope", p))
	must(t, d.RemovePlan(ctx, Plan{Name: "Old"}))
	f.check(t,
		"/ppp/profile/add =name=Gold =local-address=10.0.0.1 =remote-address=pool1 =rate-limit=512K/2M 3M/4M",
		"/ppp/profile/print =.proplist=.id ?name=Old",
		"/ppp/profile/set =numbers=*5 =name=Gold =local-address=10.0.0.1 =remote-address=pool1 =rate-limit=512K/2M 3M/4M =on-up= =on-down=",
		"/ppp/profile/print =.proplist=.id ?name=Nope",
		"/ppp/profile/add =name=Gold =local-address=pool1 =remote-address=pool1 =rate-limit=512K/2M 3M/4M",
		"/ppp/profile/print =.proplist=.id ?name=Old",
		"/ppp/profile/remove =numbers=*5")
}

func TestPPPoEOnlineDisconnect(t *testing.T) {
	d, f := pp(map[string]string{"/ppp/active|name=bob-ppp": "*7"})
	c := cust
	c.PPPoEUsername = "bob-ppp"
	on, err := d.IsOnline(ctx, c, "r")
	if err != nil || !on {
		t.Fatal(on, err)
	}
	must(t, d.Disconnect(ctx, c, "r"))
	f.check(t,
		"/ppp/active/print =.proplist=.id ?name=bob",
		"/ppp/active/print =.proplist=.id ?name=bob-ppp",
		"/ppp/active/print =.proplist=.id ?name=bob",
		"/ppp/active/print =.proplist=.id ?name=bob-ppp",
		"/ppp/active/remove =numbers=*7")
}

var (
	_ Device = Dummy{}
	_ Device = (*MikrotikHotspot)(nil)
	_ Device = (*MikrotikPPPoE)(nil)
)

func TestAddCustomerRefusesEmptyPassword(t *testing.T) {
	c := cust
	c.Password, c.PPPoEPassword = "", ""
	d, f := hs(nil)
	if d.AddCustomer(ctx, c, plan) == nil || len(f.sent) != 0 {
		t.Fatal("hotspot must refuse empty password")
	}
	p, _ := pp(nil)
	if p.AddCustomer(ctx, c, plan) == nil {
		t.Fatal("pppoe must refuse empty password")
	}
}
