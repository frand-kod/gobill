package web

import (
	"net/url"
	"testing"
)

// FuzzRadiusRest throws arbitrary form fields at the FreeRADIUS rlm_rest endpoint: no panics, no 5xx.
func FuzzRadiusRest(f *testing.F) {
	f.Add("authorize", "bob", "pw", "", "", "", "")
	f.Add("authenticate", "bob", "", "0x0700112233445566778899aabbccddeeff", "0x30313233343536373839616263646566", "", "")
	f.Add("accounting", "bob", "", "", "", "Stop", "10.0.0.1")
	f.Add("accounting", "bob", "", "", "", "Start", "")
	f.Add("post-auth", "bob", "", "0x", "0xzz", "", "")
	s, _ := restSetup(f, false)
	h := s.Handler()
	f.Fuzz(func(t *testing.T, action, user, pass, chapResp, chapCh, status, nas string) {
		w := post(h, "/radius/rest", url.Values{"username": {user}, "password": {pass}, "CHAPassword": {chapResp},
			"CHAPchallenge": {chapCh}, "acctStatusType": {status}, "nasIpAddress": {nas}, "action": {action}}, "")
		if w.Code >= 500 {
			t.Fatalf("status %d", w.Code)
		}
	})
}
