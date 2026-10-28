package wg

import (
	"net/netip"
	"testing"
)

func TestAllocIP(t *testing.T) {
	sub := netip.MustParsePrefix("10.99.0.0/29") // .1 is gobill, .2 to .6 are peers, .7 is the broadcast
	var used []string
	for _, want := range []string{"10.99.0.2", "10.99.0.3", "10.99.0.4", "10.99.0.5", "10.99.0.6"} {
		ip, err := AllocIP(sub, used)
		if err != nil || ip.String() != want {
			t.Fatalf("got %v %v, want %s", ip, err, want)
		}
		used = append(used, ip.String())
	}
	if ip, err := AllocIP(sub, used); err == nil {
		t.Fatalf("subnet is full but got %v", ip)
	}
	// a freed address in the middle is reused; gobill's own address never is
	if ip, _ := AllocIP(sub, []string{"10.99.0.2", "10.99.0.4", "10.99.0.5", "10.99.0.6"}); ip.String() != "10.99.0.3" {
		t.Fatalf("hole not reused: %v", ip)
	}
}

func TestParseSubnet(t *testing.T) {
	for _, ok := range []string{"10.99.0.0/24", "192.168.200.0/24", "172.16.0.0/16", "10.0.0.5/29"} {
		if _, err := ParseSubnet(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "8.8.8.0/24", "10.0.0.0/8", "10.0.0.0/30", "fd00::/64", "10.99.0.1"} {
		if _, err := ParseSubnet(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	c, err := LoadConfig(map[string]string{})
	if err != nil || c.Port != DefaultPort || c.Subnet.String() != DefaultSubnet || c.Self().String() != "10.99.0.1" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if _, err := LoadConfig(map[string]string{"wg_port": "70000"}); err == nil {
		t.Error("bad port accepted")
	}
}

func TestKeys(t *testing.T) {
	priv, pub := GenKey()
	if got, err := PublicKey(priv); err != nil || got != pub {
		t.Fatalf("public key of %s: %s %v, want %s", priv, got, err, pub)
	}
	if p2, _ := GenKey(); p2 == priv {
		t.Fatal("two keys alike")
	}
}
