package web

// ISOLIR settings: reset buttons, the custom HTML variable list and the MikroTik setup helper.
// The public page itself is in web/public.

import (
	"fmt"
	"net"
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/notify"
)

// isolirSections are the defaults each "Kembalikan ke default" action writes.
var isolirSections = map[string]map[string]string{
	"paket":   {"isolir_enabled": "no", "isolir_plan_id": "", "isolir_days": "30"},
	"halaman": {"isolir_title": isolir.Defaults["isolir_title"], "isolir_message": isolir.Defaults["isolir_message"], "isolir_wa": "", "isolir_show_voucher": "yes", "isolir_address_list": isolir.DefaultAddrList},
	"mode":    {"isolir_mode": "default"},
	"html":    {"isolir_html": ""},
}

// isolirReset is POST /admin/settings/isolir/reset/{section}: writes the section's defaults.
func (s *Server) isolirReset(w http.ResponseWriter, r *http.Request) {
	sec := r.PathValue("section")
	set, ok := isolirSections[sec]
	if !ok {
		http.NotFound(w, r)
		return
	}
	for k, v := range set {
		if err := s.Queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			s.Fail(w, "isolir reset "+k, err)
			return
		}
	}
	s.settingsSaved(r.Context())
	s.done(w, r, "/admin/settings/isolir", "Settings saved", "settings.reset", "isolir "+sec)
}

// isolirVarList are the variables of the custom HTML, the ones renderIsolirCustom fills.
var isolirVarList = []notify.Var{
	{Name: "company_name", Desc: "Company name"},
	{Name: "logo_url", Desc: "Logo address"},
	{Name: "title", Desc: "Title of the page (Title setting)"},
	{Name: "message", Desc: "Message of the page (Message setting)"},
	{Name: "username", Desc: "Customer's username"},
	{Name: "name", Desc: "Customer's full name"},
	{Name: "plan_name", Desc: "Package name"},
	{Name: "expired_at", Desc: "Expiry date and time"},
	{Name: "amount", Desc: "Price of the package"},
	{Name: "logout_url", Desc: "Hotspot logout address, empty when the Hotspot address is not set"},
	{Name: "portal_url", Desc: "Customer login address, where the package is paid or extended"},
	{Name: "wa_admin_link", Desc: "WhatsApp link to the admin"},
	{Name: "voucher_form", Desc: "Voucher form, rendered by gobill"},
	{Name: "promos", Desc: "Promotions, rendered by gobill"},
	{Name: "known", Desc: "yes or no: the client is a known customer"},
	{Name: "notice", Desc: "Result of the last action, such as a voucher"},
}

// isolirSetupFields is the MikroTik setup helper: copyable RouterOS commands for the address gobill
// listens on (see gobillAddr).
func (s *Server) isolirSetupFields(st map[string]string) []field {
	host, port := s.gobillAddr()
	// M3: kept as-is, see docs/internal/rencana/modularisasi.md (Value gives "" for an empty list,
	// RADIUS sends "isolir")
	list := isolir.Value(st, "isolir_address_list")
	hosts := isolir.HostLines(isolir.Value(st, "isolir_allow_hosts"))
	sec := s.T("Setup router (MikroTik, RouterOS 6 and 7)")
	return []field{
		{Name: "isolir_cmd_rules", Label: s.T("1. Isolation rules (paste the whole block, in this order)"), Type: "pre", Section: sec,
			Value: isolir.RouterRules(list, host, port, hosts),
			Hint:  s.T("The rules must sit above any general accept rule in the forward chain, or isolated clients are not cut off. Only HTTP on port 80 is redirected to the isolir page. Hosts in Allowed while isolated are reachable; wildcard hosts are learned when the app opens a TLS connection. Changed the allowed hosts? Paste the block again.")},
		{Name: "isolir_cmd_walled", Label: s.T("2. Walled garden hotspot for gobill"), Type: "pre", Section: sec,
			Value: fmt.Sprintf("/ip hotspot walled-garden ip add dst-address=%s dst-port=%s action=accept", host, port),
			Hint:  s.T("So the isolir page opens before hotspot login.")},
		{Name: "isolir_cmd_bypass", Label: s.T("3. Bypass the hotspot for the gobill host"), Type: "pre", Section: sec,
			Value: "/ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed",
			Hint:  s.T("Replace <MAC-HOST-GOBILL> with the MAC address of gobill's LAN card. Without this rule, the hotspot can capture gobill's own traffic.")},
		{Name: "isolir_cmd_test", Label: s.T("Test from the LAN with curl"), Type: "pre", Section: sec,
			Value: fmt.Sprintf(`curl -i -H "Host: example.com" http://%s:%s/`, host, port),
			Hint:  s.T("gobill serves the isolir page to isolated clients at the root and at every other path. Do not test with /tool fetch: RouterOS sends a duplicate Host header and gets 400.")},
		{Name: "isolir_cmd_note", Label: s.T("Note"), Type: "note", Section: sec,
			Hint: s.T("The address-list is sent by RADIUS as Mikrotik-Address-List. RouterOS 6 resolves DNS allow-list entries to exact names only, so a host whose CDN address changes may sometimes fail, such as WhatsApp media. Add a bank app by putting its host in Allowed while isolated, then paste the block again.")},
	}
}

// gobillAddr is the LAN address and port gobill listens on (GOBILL_HTTP). A host that is not an IP,
// such as empty or 0.0.0.0, gives the first LAN IPv4 address of this host.
func (s *Server) gobillAddr() (host, port string) {
	addr := s.ListenAddr
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", "8080"
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = isolir.LanIPv4()
	}
	return host, port
}
