package web

// Header search palette: one box for data (customers, subscriptions, invoices, vouchers, plans, routers,
// NAS), pages, quick actions and settings. GET /admin/search.json?q= returns the groups as JSON for the
// palette; GET /admin/search renders the same groups as a page for browsers without JS.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

const searchMaxQ = 64 // longer queries are cut to this many characters

// Role sets for the palette. A nil set means every admin role (the page is open to all).
var (
	roleStaff  = []string{"SuperAdmin", "Admin", "Agent", "Sales"}
	roleMgr    = []string{"SuperAdmin", "Admin"}
	roleSuper  = []string{"SuperAdmin"}
	roleCoupon = []string{"SuperAdmin", "Admin", "Sales"}
	roleUsers  = []string{"SuperAdmin", "Admin", "Agent"}
)

// searchEntry is a page, quick action or setting. Syn holds the words people type that the title does not
// carry (English names, Indonesian synonyms); matching is on Title + Syn, case and accents ignored.
type searchEntry struct {
	Title, Sub, URL, Syn string
	Roles                []string
}

type searchItem struct {
	Title string `json:"title"`
	Sub   string `json:"sub,omitempty"`
	URL   string `json:"url"`
}

type searchGroup struct {
	Title string       `json:"title"`
	Items []searchItem `json:"items"`
}

// searchPages is the sidebar menu, in menu order. Keep it in step with web/templates/app.html.
var searchPages = []searchEntry{
	{Title: "Dashboard", URL: "/admin", Syn: "beranda home dasbor ringkasan", Roles: nil},
	{Title: "Customer", URL: "/admin/customers", Syn: "pelanggan member pengguna customers", Roles: nil},
	{Title: "Recharge Account", URL: "/admin/recharge", Syn: "isi ulang paket perpanjang top up recharge", Roles: roleStaff},
	{Title: "Redeem Voucher", URL: "/admin/vouchers/redeem", Syn: "tukar voucher redeem aktivasi kode", Roles: roleStaff},
	{Title: "Add balance", URL: "/admin/deposit", Syn: "deposit saldo isi saldo tambah saldo", Roles: roleStaff},
	{Title: "Voucher", URL: "/admin/vouchers", Syn: "kode voucher daftar", Roles: nil},
	{Title: "Subscriptions", URL: "/admin/subscriptions", Syn: "langganan paket aktif kedaluwarsa", Roles: nil},
	{Title: "Online Sessions", URL: "/admin/radius/sessions", Syn: "sesi online radius koneksi aktif session", Roles: roleMgr},
	{Title: "Send Message", URL: "/admin/message/send", Syn: "kirim pesan whatsapp sms notifikasi message", Roles: roleStaff},
	{Title: "Daily Report", URL: "/admin/reports", Syn: "laporan harian pendapatan report", Roles: nil},
	{Title: "Period Report", URL: "/admin/reports/period", Syn: "laporan periode bulanan report", Roles: nil},
	{Title: "Transactions", URL: "/admin/transactions", Syn: "transaksi invoice faktur pembayaran", Roles: nil},
	{Title: "Service Plan", URL: "/admin/plans", Syn: "paket layanan plan harga", Roles: roleMgr},
	{Title: "Bandwidth", URL: "/admin/bandwidth", Syn: "bandwidth kecepatan rate limit", Roles: roleMgr},
	{Title: "Pool", URL: "/admin/pool", Syn: "pool ip address alamat", Roles: roleMgr},
	{Title: "Coupons", URL: "/admin/coupons", Syn: "kupon diskon kode promo", Roles: roleCoupon},
	{Title: "Routers", URL: "/admin/routers", Syn: "router mikrotik perangkat", Roles: roleMgr},
	{Title: "NAS", URL: "/admin/nas", Syn: "nas radius client", Roles: roleMgr},
	{Title: "Customer Map", URL: "/admin/maps/customers", Syn: "peta map lokasi pelanggan", Roles: nil},
	{Title: "Router Map", URL: "/admin/maps/routers", Syn: "peta map lokasi router", Roles: roleMgr},
	{Title: "ODP Map", URL: "/admin/maps/odp", Syn: "peta map lokasi odp", Roles: roleMgr},
	{Title: "ODP", URL: "/admin/odp", Syn: "odp kotak distribusi tiang", Roles: roleMgr},
	{Title: "Payment Gateway", URL: "/admin/payment-gateway", Syn: "pembayaran tripay qris gateway audit", Roles: roleMgr},
	{Title: "Logs", URL: "/admin/logs", Syn: "log catatan aktivitas riwayat", Roles: roleMgr},
	{Title: "System Status", URL: "/admin/status", Syn: "status sistem kesehatan health monitor", Roles: roleMgr},
	{Title: "Admin Users", URL: "/admin/users", Syn: "pengguna admin operator akun user", Roles: roleUsers},
	{Title: "Pages", URL: "/admin/pages/announcement", Syn: "halaman pengumuman halaman statis", Roles: roleMgr},
	{Title: "Custom fields", URL: "/admin/fields", Syn: "field kolom tambahan data pelanggan", Roles: roleMgr},
	{Title: "Settings", URL: "/admin/settings", Syn: "pengaturan setting konfigurasi", Roles: roleMgr},
	{Title: "Change Password", URL: "/admin/password", Syn: "ganti kata sandi password", Roles: nil},
	{Title: "Guide", URL: "/admin/docs", Syn: "panduan bantuan docs petunjuk", Roles: nil},
}

// searchActions are the quick actions of the palette.
var searchActions = []searchEntry{
	{Title: "Recharge customer", URL: "/admin/recharge", Syn: "isi ulang paket pelanggan perpanjang", Roles: roleStaff},
	{Title: "Generate vouchers", URL: "/admin/vouchers/new", Syn: "buat voucher generate kode baru", Roles: roleStaff},
	{Title: "Add customer", URL: "/admin/customers/new", Syn: "tambah pelanggan baru daftar", Roles: roleStaff},
	{Title: "Add plan", URL: "/admin/plans/new", Syn: "tambah paket baru layanan", Roles: roleMgr},
	{Title: "System Status", URL: "/admin/status", Syn: "status sistem kesehatan", Roles: roleMgr},
}

// settingsSyn adds the words people search for that a setting's label and hint do not carry.
var settingsSyn = map[string]string{
	"session_timeout_duration":      "batas waktu sesi logout diam idle",
	"single_session":                "sesi tunggal login ganda",
	"qris_payload":                  "qris kode statis bayar pembayaran",
	"alt_wga_server_url":            "gowa whatsapp wa server",
	"wa_url":                        "whatsapp sms gateway pesan",
	"expired_notify_minutes_before": "kedaluwarsa menit",
	"daily_summary_time":            "ringkasan harian jam laporan",
	"maintenance_mode":              "pemeliharaan maintenance",
	"clock_guard":                   "jam waktu server ntp",
	"app_url":                       "alamat domain url",
	"company_name":                  "nama usaha perusahaan",
	"voucher_format":                "format kode voucher",
	"tripay_api_key":                "tripay api kunci",
	"user_notification_expired":     "notifikasi kedaluwarsa",
}

// searchFolder maps the accented letters Indonesian and the other languages use to plain ones.
var searchFolder = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "ö", "o", "ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n")

func searchFold(s string) string { return searchFolder.Replace(strings.ToLower(s)) }

// searchQuery trims q and cuts it to searchMaxQ characters.
func searchQuery(raw string) string {
	q := []rune(strings.TrimSpace(raw))
	if len(q) > searchMaxQ {
		q = q[:searchMaxQ]
	}
	return string(q)
}

// allowed reports whether role may open an entry; nil roles = every admin role.
func allowed(roles []string, role string) bool { return roles == nil || contains(roles, role) }

// matchWords reports whether every word of words (already folded) appears in the folded text.
func matchWords(words []string, text string) bool {
	hay := searchFold(text)
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// matchEntries returns up to limit items of es that role may open and that contain every word. Titles
// are English keys and are translated here; Sub is already translated.
func (s *Server) matchEntries(role string, es []searchEntry, words []string, limit int) []searchItem {
	var out []searchItem
	for _, e := range es {
		if len(out) == limit {
			break
		}
		if allowed(e.Roles, role) && matchWords(words, e.Title+" "+e.Syn) {
			out = append(out, searchItem{Title: s.catalog.T(s.language(), e.Title), Sub: e.Sub, URL: e.URL})
		}
	}
	return out
}

// addGroup appends a titled group, translating the title; empty groups are left out.
func (s *Server) addGroup(gs []searchGroup, title string, items []searchItem) []searchGroup {
	if len(items) == 0 {
		return gs
	}
	return append(gs, searchGroup{Title: s.catalog.T(s.language(), title), Items: items})
}

// settingsEntries lists every settings field of the sub-pages the role may open. The entries come from
// settingsFields, so a new field is searchable without a change here.
func (s *Server) settingsEntries(role string) []searchEntry {
	t := func(x string) string { return s.catalog.T(s.language(), x) }
	var out []searchEntry
	for _, tab := range settingsTabs {
		roles := roleMgr
		if tab.SuperOnly {
			roles = roleSuper
		}
		if !allowed(roles, role) {
			continue
		}
		fs := s.settingsFields(tab.Slug, nil, nil, nil)
		isSetting := map[string]bool{}
		for _, n := range fieldNames(fs) {
			isSetting[n] = true
		}
		for _, f := range fs {
			if !isSetting[f.Name] {
				continue
			}
			syn := strings.Join([]string{f.Label, f.Name, strings.ReplaceAll(f.Name, "_", " "), f.Hint, t(f.Hint), f.Section, t(f.Section), tab.Label, settingsSyn[f.Name]}, " ")
			if strings.Contains(f.Name, "reminder") {
				syn += " pengingat"
			}
			out = append(out, searchEntry{Title: f.Label, Sub: joinNonEmpty(" › ", t(tab.Label), t(f.Section)),
				URL: "/admin/settings/" + tab.Slug + "#f-" + f.Name, Syn: syn, Roles: roles})
		}
	}
	return out
}

func joinNonEmpty(sep string, parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, sep)
}

// searchData finds records by q (literal, case-insensitive; see search.sql). Each group is capped at 5.
// Customers, subscriptions and invoices show to every admin role, as their list pages do; the rest follow
// the role sets of their pages.
func (s *Server) searchData(ctx context.Context, a *db.Admin, q string) ([]searchGroup, error) {
	const n = 5
	var out []searchGroup
	cs, err := s.queries.SearchCustomersContains(ctx, db.SearchCustomersContainsParams{Q: q, Limit: n})
	if err != nil {
		return nil, err
	}
	var items []searchItem
	for _, c := range cs {
		items = append(items, searchItem{Title: c.Username, Sub: joinNonEmpty(" · ", c.Fullname, c.Phone, c.PppoeUsername, c.Email),
			URL: fmt.Sprint("/admin/customers/", c.ID)})
	}
	out = s.addGroup(out, "Customer", items)

	subs, err := s.queries.SearchSubscriptionsByUsername(ctx, db.SearchSubscriptionsByUsernameParams{Q: q, Limit: n})
	if err != nil {
		return nil, err
	}
	items = nil
	for _, x := range subs {
		items = append(items, searchItem{Title: x.Username, Sub: joinNonEmpty(" · ", x.PlanName, x.Status),
			URL: fmt.Sprint("/admin/customers/", x.CustomerID)})
	}
	out = s.addGroup(out, "Subscriptions", items)

	trx, err := s.queries.SearchTransactionsPrefix(ctx, db.SearchTransactionsPrefixParams{Q: q, Limit: n})
	if err != nil {
		return nil, err
	}
	items = nil
	for _, x := range trx {
		items = append(items, searchItem{Title: x.Invoice, Sub: joinNonEmpty(" · ", x.Username, x.PlanName, money(x.Price)),
			URL: fmt.Sprint("/admin/transactions/", x.ID, "/invoice")})
	}
	out = s.addGroup(out, "Transactions", items)

	if allowed(roleStaff, a.Role) {
		vs, err := s.queries.SearchVouchersPrefix(ctx, db.SearchVouchersPrefixParams{Q: q, Limit: n})
		if err != nil {
			return nil, err
		}
		items = nil
		for _, v := range vs {
			items = append(items, searchItem{Title: v.Code, Sub: joinNonEmpty(" · ", v.PlanName, v.Status),
				URL: "/admin/vouchers?q=" + url.QueryEscape(v.Code)})
		}
		out = s.addGroup(out, "Voucher", items)
	}
	if allowed(roleMgr, a.Role) {
		ps, err := s.queries.SearchPlansContains(ctx, db.SearchPlansContainsParams{Q: q, Limit: n})
		if err != nil {
			return nil, err
		}
		items = nil
		for _, p := range ps {
			items = append(items, searchItem{Title: p.Name, Sub: joinNonEmpty(" · ", p.Type, money(p.Price)),
				URL: fmt.Sprint("/admin/plans/", p.ID, "/edit")})
		}
		out = s.addGroup(out, "Service Plan", items)

		rs, err := s.queries.SearchRoutersContains(ctx, db.SearchRoutersContainsParams{Q: q, Limit: n})
		if err != nil {
			return nil, err
		}
		items = nil
		for _, r := range rs {
			items = append(items, searchItem{Title: r.Name, Sub: r.Host, URL: fmt.Sprint("/admin/routers/", r.ID, "/edit")})
		}
		out = s.addGroup(out, "Routers", items)

		ns, err := s.queries.SearchNASContains(ctx, db.SearchNASContainsParams{Q: q, Limit: n})
		if err != nil {
			return nil, err
		}
		items = nil
		for _, x := range ns {
			items = append(items, searchItem{Title: x.Name, Sub: x.Ip, URL: fmt.Sprint("/admin/nas/", x.ID, "/edit")})
		}
		out = s.addGroup(out, "NAS", items)
	}
	return out, nil
}

// searchGroupsFor is the palette result for raw q. Data groups need q; quick actions and pages always
// show (the first ones when q is empty); settings only with q.
func (s *Server) searchGroupsFor(ctx context.Context, a *db.Admin, raw string) ([]searchGroup, error) {
	q := searchQuery(raw)
	out := []searchGroup{}
	words := strings.Fields(searchFold(q))
	if q != "" {
		data, err := s.searchData(ctx, a, q)
		if err != nil {
			return nil, err
		}
		out = append(out, data...)
	}
	out = s.addGroup(out, "Quick actions", s.matchEntries(a.Role, searchActions, words, 5))
	out = s.addGroup(out, "Pages", s.matchEntries(a.Role, searchPages, words, 10))
	if q != "" {
		out = s.addGroup(out, "Settings", s.matchEntries(a.Role, s.settingsEntries(a.Role), words, 12))
	}
	return out, nil
}

// searchJSON is GET /admin/search.json?q=. Every admin role may call it; each group is limited to the roles
// that can open its pages.
func (s *Server) searchJSON(w http.ResponseWriter, r *http.Request) {
	groups, err := s.searchGroupsFor(r.Context(), adminFrom(r), r.URL.Query().Get("q"))
	if err != nil {
		s.fail(w, "search", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{"groups": groups})
}

// searchPage is GET /admin/search?q=: the palette's results as a page, for the form without JS.
func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	q := searchQuery(r.URL.Query().Get("q"))
	groups, err := s.searchGroupsFor(r.Context(), adminFrom(r), q)
	if err != nil {
		s.fail(w, "search", err)
		return
	}
	s.render(w, r, http.StatusOK, "search", Page{Title: "Search", Data: searchView{Q: q, Groups: groups}})
}

type searchView struct {
	Q      string
	Groups []searchGroup
}
