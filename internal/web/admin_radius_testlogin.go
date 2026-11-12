package web

// Test login: runs the RADIUS decision (Server.Authorize, no UDP, read-only) for a user and password.

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/httpx"

	"github.com/frand-kod/gobill/internal/radius"
)

func (s *Server) testLoginFields(r *http.Request, v, e map[string]string, result string) ([]field, error) {
	nas, err := s.Queries.ListNAS(r.Context())
	if err != nil {
		return nil, err
	}
	sel := text("nas", "NAS", v, e).As("select")
	for _, n := range nas {
		if p, err := netip.ParsePrefix(n.Ip); err == nil {
			sel.Options = append(sel.Options, option{Value: p.Addr().String(), Label: n.Name})
		} else {
			sel.Options = append(sel.Options, option{Value: n.Ip, Label: n.Name})
		}
	}
	pt := text("port_type", "NAS-Port-Type", v, e).As("select")
	pt.Options = []option{{Value: "", Label: "Not sent"}, {Value: "Wireless-802.11", Label: "Wireless-802.11 (hotspot)"}, {Value: "Ethernet", Label: "Ethernet"}, {Value: "Virtual", Label: "Virtual (VPN / tunnel)"}}
	out := section([]field{
		text("username", "Username", v, e).Req(),
		text("password", "Password", v, e).As("password").Req().WithHint("Checked against the customer's router password. The attempt is logged in the activity log; no session or customer data changes. A voucher-style login (password = username) is refused: it would redeem the voucher."),
		sel,
		text("mac", "MAC (Calling-Station-Id)", v, e),
		pt,
		text("port_id", "NAS-Port-Id", v, e),
	}, "Test login", "")
	if result != "" {
		out = append(section([]field{{Name: "result", Label: "Result", Type: "pre", Value: result}}, "Result", ""), out...)
	}
	return out, nil
}

func (s *Server) testLoginForm(w http.ResponseWriter, r *http.Request) {
	fs, err := s.testLoginFields(r, nil, nil, "")
	if err != nil {
		s.Fail(w, "test login", err)
		return
	}
	s.renderForm(w, r, 200, formPage{Heading: "Test login", Action: "/admin/radius/test-login", Cancel: "/admin/nas", Fields: fs})
}

func (s *Server) testLoginRun(w http.ResponseWriter, r *http.Request) {
	v := httpx.FormVals(r, "username", "password", "nas", "mac", "port_type", "port_id")
	user, pw := strings.TrimSpace(v["username"]), v["password"]
	e := map[string]string{}
	switch {
	case user == "":
		e["username"] = "Enter a username"
	case pw == "":
		e["password"] = "Enter a password"
	case pw == user:
		e["password"] = "Enter a username and a password that differs from it"
	}
	var result, reject string
	if len(e) == 0 {
		d := s.radiusServer().Authorize(r.Context(), radius.AuthRequest{User: user, DryRun: true, NAS: v["nas"], MAC: strings.TrimSpace(v["mac"]), PortType: v["port_type"], PortID: strings.TrimSpace(v["port_id"]),
			Check: func(stored []byte) (bool, string) { return subtle.ConstantTimeCompare([]byte(pw), stored) == 1, "" }})
		if d.Reject != "" {
			reject = d.Reject
			if k, ok := adminx.RejectLabels[d.Reject]; ok {
				reject = s.T(k)
			}
		} else {
			result = fmt.Sprintf("%s\n%s: %s\n%s", s.T("Access-Accept"), s.T("Plan"), d.Plan.PlanName, strings.Join(d.Attrs(), "\n"))
		}
		s.logActivity(r, "radius.test_login", user)
	}
	v["password"] = ""
	fs, err := s.testLoginFields(r, v, e, result)
	if err != nil {
		s.Fail(w, "test login", err)
		return
	}
	// a reject is an error alert above the form (role=alert), the same as the validation errors
	page := Page{Title: "Test login", Data: formPage{Heading: "Test login", Action: "/admin/radius/test-login", Cancel: "/admin/nas", Fields: fs}}
	if reject != "" {
		page.Error = s.T("Access-Reject") + ": " + reject
	}
	s.Render(w, r, http.StatusOK, "form", page)
}
