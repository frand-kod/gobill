package web

import (
	"net/url"
	"strings"
	"testing"
)

func TestTestLogin(t *testing.T) {
	_, h, _, c := crudApp(t)
	wantCode(t, do(h, "GET", "/admin/radius/test-login", nil, c), 200, "form")
	post := func(user, pw string) string {
		w := do(h, "POST", "/admin/radius/test-login", url.Values{"username": {user}, "password": {pw}}, c)
		wantCode(t, w, 200, "run")
		return w.Body.String()
	}
	if b := post("nobody", "x"); !strings.Contains(b, "Access-Reject") {
		t.Errorf("unknown user must show the reject")
	}
	if b := post("nobody", "nobody"); strings.Contains(b, "Access-") {
		t.Errorf("voucher-style login must be refused before Authorize")
	}
}
