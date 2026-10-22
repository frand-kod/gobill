package radius

import (
	"crypto/md5"
	"net"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2759"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/vendors/microsoft"
)

func seed(f *testing.F, ps ...*radius.Packet) {
	for _, p := range ps {
		if b, err := p.Encode(); err == nil {
			f.Add(b)
		}
	}
}

// FuzzHandleAuth feeds raw UDP payloads to the Access-Request handler: it must not panic.
func FuzzHandleAuth(f *testing.F) {
	chap := radius.New(radius.CodeAccessRequest, nasSecret)
	rfc2865.UserName_SetString(chap, "alice")
	ch := []byte("0123456789abcdef")
	rfc2865.CHAPChallenge_Set(chap, ch)
	sum := md5.Sum(append(append([]byte{7}, "pw"...), ch...))
	rfc2865.CHAPPassword_Set(chap, append([]byte{7}, sum[:]...))

	ms := radius.New(radius.CodeAccessRequest, nasSecret)
	rfc2865.UserName_SetString(ms, "alice")
	pch := []byte("fedcba9876543210")
	nt, _ := rfc2759.GenerateNTResponse(ch, pch, []byte("alice"), []byte("pw"))
	microsoft.MSCHAPChallenge_Set(ms, ch)
	microsoft.MSCHAP2Response_Set(ms, append(append(append([]byte{1, 0}, pch...), make([]byte, 8)...), nt...))

	ma := pap("alice", "pw")
	addMA(ma)
	seed(f, pap("alice", "pw"), pap("alice", "bad"), pap("", ""), chap, ms, ma)

	e := setup(f)
	from := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5000}
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := radius.Parse(b, nasSecret)
		if err != nil {
			return
		}
		e.s.HandleAuth(&rec{}, &radius.Request{Packet: p, RemoteAddr: from})
	})
}

// FuzzHandleAcct is the Accounting-Request counterpart.
func FuzzHandleAcct(f *testing.F) {
	var ps []*radius.Packet
	for _, typ := range []rfc2866.AcctStatusType{rfc2866.AcctStatusType_Value_Start, rfc2866.AcctStatusType_Value_InterimUpdate,
		rfc2866.AcctStatusType_Value_Stop, rfc2866.AcctStatusType_Value_AccountingOn, rfc2866.AcctStatusType_Value_AccountingOff} {
		p := radius.New(radius.CodeAccountingRequest, nasSecret)
		rfc2866.AcctStatusType_Set(p, typ)
		rfc2866.AcctSessionID_SetString(p, "s1")
		rfc2865.UserName_SetString(p, "alice")
		rfc2865.FramedIPAddress_Set(p, net.ParseIP("10.1.1.2"))
		rfc2865.CallingStationID_SetString(p, "AA:BB:CC:DD:EE:FF")
		rfc2866.AcctInputOctets_Set(p, 1000)
		rfc2866.AcctOutputOctets_Set(p, 2000)
		rfc2866.AcctSessionTime_Set(p, 60)
		ps = append(ps, p)
	}
	seed(f, ps...)

	e := setup(f)
	from := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5000}
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := radius.Parse(b, nasSecret)
		if err != nil {
			return
		}
		e.s.HandleAcct(&rec{}, &radius.Request{Packet: p, RemoteAddr: from})
	})
}

func FuzzCheckMA(f *testing.F) {
	ok := pap("alice", "pw")
	addMA(ok)
	seed(f, ok, pap("alice", "pw"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if p, err := radius.Parse(b, nasSecret); err == nil {
			checkMA(p)
		}
	})
}

func FuzzParsePrefix(f *testing.F) {
	for _, s := range []string{"10.0.0.1", "10.0.0.0/24", "::1", "fe80::1%eth0", "::ffff:1.2.3.4", "2001:db8::/32", "", "/", "1.2.3.4/33", "x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if p := parsePrefix(s); p.IsValid() && p != p.Masked() {
			t.Fatalf("%q not masked: %v", s, p)
		}
	})
}
