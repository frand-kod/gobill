package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"layeh.com/radius"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

func newCust(t *testing.T, q *db.Queries, user, full, phone, pppoe, status string) db.Customer {
	t.Helper()
	c, err := q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: user, PasswordHash: "h", Fullname: full, Phone: phone,
		PppoeUsername: pppoe, ServiceType: "PPPoE", Status: status})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type hit struct {
	ID                        int64
	Username, Fullname, Phone string
	URL                       string `json:"url"`
}

func TestHeaderSearch(t *testing.T) {
	_, h, q, c := crudApp(t)
	zed := newCust(t, q, "zed01", "Zed Smith", "081234", "PP_One", "Active")
	for i := 1; i <= 10; i++ {
		newCust(t, q, fmt.Sprintf("bulk%02d", i), "Bulk User", fmt.Sprintf("0899%02d", i), "", "Active")
	}
	get := func(term string) []hit {
		t.Helper()
		w := do(h, "GET", "/admin/search?q="+url.QueryEscape(term), nil, c)
		wantCode(t, w, 200, "search "+term)
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("content type %q", ct)
		}
		var out []hit
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("json: %v: %s", err, w.Body)
		}
		return out
	}
	for _, term := range []string{"zed01", "ZED", "smith", "081234", "pp_one", "PP_ONE"} { // username, name, phone, pppoe; any case
		if r := get(term); len(r) != 1 || r[0].ID != zed.ID || r[0].URL != "/admin/customers/"+itoa(zed.ID) {
			t.Errorf("%q: %+v", term, r)
		}
	}
	if r := get("bulk"); len(r) != 8 {
		t.Errorf("limit: got %d rows, want 8", len(r))
	}
	if r := get("bulk03"); len(r) == 0 || r[0].Username != "bulk03" {
		t.Errorf("exact match must come first: %+v", r)
	}
	// SQL safety: quotes and LIKE wildcards are plain text
	for _, term := range []string{"%", "z_d", "b%1", "' OR 1=1 --", `"; DROP TABLE customers; --`} {
		if r := get(term); len(r) != 0 {
			t.Errorf("%q matched %d rows", term, len(r))
		}
	}
	if r := get(""); len(r) != 0 {
		t.Error("empty term returned rows")
	}
	if n, _ := q.QuickSearchCustomers(t.Context(), "zed"); len(n) != 1 {
		t.Error("table damaged")
	}
	// the plain list search (no JS) also finds PPPoE usernames
	if b := do(h, "GET", "/admin/customers?q=pp_one", nil, c).Body.String(); !strings.Contains(b, "zed01") {
		t.Error("customer list ?q= misses pppoe_username")
	}

	// roles: staff only
	mk(t, q, "agnes", "Agent", 1)
	mk(t, q, "sally", "Sales", 1)
	for _, u := range []string{"agnes", "sally"} {
		wantCode(t, do(h, "GET", "/admin/search?q=zed", nil, login(t, h, u)), 200, u)
	}
	wantCode(t, do(h, "GET", "/admin/search?q=zed", nil, login(t, h, "rita")), 403, "Report role")
	wantCode(t, do(h, "GET", "/admin/search?q=zed", nil, nil), 303, "anonymous")

	// the header ships the type-ahead for staff, the plain form for everyone
	b := do(h, "GET", "/admin", nil, c).Body.String()
	for _, want := range []string{`action="/admin/customers"`, `name="q"`, `role="combobox"`, "/admin/search?q="} {
		if !strings.Contains(b, want) {
			t.Errorf("header lacks %q", want)
		}
	}
	if b := do(h, "GET", "/admin", nil, login(t, h, "rita")).Body.String(); strings.Contains(b, "/admin/search") || !strings.Contains(b, `role="search"`) {
		t.Error("report role must get the plain form only")
	}
}

func TestDiagnose(t *testing.T) {
	s, h, q, c := crudApp(t)
	ctx, now := t.Context(), time.Now().Unix()
	rt, _ := q.CreateRouter(ctx, db.CreateRouterParams{Name: "edge1", Host: "h", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	bw, _ := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	mkPlan := func(name, device string, limited int64, lt string, mins, mb int64) db.Plan {
		p, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: name, Type: "PPPoE", Billing: "prepaid", Validity: 30, ValidityUnit: "Days", Device: device, Enabled: 1, BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true},
			RouterID: sql.NullInt64{Int64: rt.ID, Valid: true}, Limited: limited,
			LimitType: sql.NullString{String: lt, Valid: lt != ""}, TimeLimit: sql.NullInt64{Int64: mins, Valid: mins > 0}, TimeUnit: sql.NullString{String: "Mins", Valid: mins > 0},
			DataLimit: sql.NullInt64{Int64: mb, Valid: mb > 0}, DataUnit: sql.NullString{String: "MB", Valid: mb > 0}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	sub := func(c db.Customer, p db.Plan, status string, start, end int64) {
		sb, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: p.ID, RouterID: sql.NullInt64{Int64: rt.ID, Valid: true}, Type: "PPPoE", StartedAt: start, ExpiresAt: end})
		if err != nil {
			t.Fatal(err)
		}
		if status != "active" {
			s.conn.ExecContext(ctx, "UPDATE subscriptions SET status = ? WHERE id = ?", status, sb.ID)
		}
	}
	diag := func(c db.Customer) diagnosis {
		t.Helper()
		subs, _ := q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 50})
		return s.diagnose(ctx, c, subs, "")
	}
	tr := func(k string) string { return s.catalog.T(s.language(), k) }
	level := func(d diagnosis, label string) string {
		for _, it := range d.Items {
			if it.Label == label {
				return it.Cls
			}
		}
		return ""
	}
	radiusPlan := mkPlan("Radius10", "Radius", 0, "", 0, 0)
	routerPlan := mkPlan("Mikro10", "MikrotikPppoe", 0, "", 0, 0)

	// active plan, online now over RADIUS
	on := newCust(t, q, "online1", "On Line", "", "", "Active")
	sub(on, radiusPlan, "active", now-1000, now+86400)
	q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "a", Username: "online1", NasIp: "10.0.0.1", FramedIp: "10.9.9.9", Mac: "AA:BB:CC", StartedAt: now - 600, UpdatedAt: now - 30})
	d := diag(on)
	if level(d, "Account") != "badge-ok" || level(d, "Plan") != "badge-ok" || level(d, "Connection") != "badge-ok" || level(d, "Router") != "badge-ok" {
		t.Errorf("online customer: %+v", d.Items)
	}
	if d.Advice != tr("Everything looks normal") {
		t.Errorf("advice %q", d.Advice)
	}
	if got := d.Items[2].Text; !strings.Contains(got, "10.9.9.9") || !strings.Contains(got, "AA:BB:CC") || !strings.Contains(got, "10.0.0.1") {
		t.Errorf("connection text %q", got)
	}

	// expired plan
	ex := newCust(t, q, "expired1", "Ex Pired", "", "", "Active")
	sub(ex, radiusPlan, "active", now-90000, now-3600)
	d = diag(ex)
	if level(d, "Plan") != "badge-bad" || d.Advice != tr("Plan ended: press Recharge") {
		t.Errorf("expired (still flagged active): %+v / %q", d.Items, d.Advice)
	}
	// plan already marked expired by the sweeper
	ex2 := newCust(t, q, "expired2", "Ex Pired", "", "", "Active")
	sub(ex2, radiusPlan, "expired", now-90000, now-3600)
	if d = diag(ex2); level(d, "Plan") != "badge-bad" || d.Advice != tr("Plan ended: press Recharge") {
		t.Errorf("expired status: %+v / %q", d.Items, d.Advice)
	}
	// no plan at all
	np := newCust(t, q, "noplan1", "No Plan", "", "", "Active")
	if d = diag(np); level(d, "Plan") != "badge-bad" || d.Advice != tr("No plan: press Recharge") {
		t.Errorf("no plan: %+v / %q", d.Items, d.Advice)
	}

	// disabled account beats everything
	dis := newCust(t, q, "disabled1", "Dis Abled", "", "", "Disabled")
	sub(dis, radiusPlan, "active", now-1000, now+86400)
	d = diag(dis)
	if level(d, "Account") != "badge-bad" || !strings.Contains(d.Advice, "Edit") {
		t.Errorf("disabled: %+v / %q", d.Items, d.Advice)
	}

	// offline, last seen earlier
	off := newCust(t, q, "offline1", "Off Line", "", "", "Active")
	sub(off, radiusPlan, "active", now-1000, now+86400)
	q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "b", Username: "offline1", NasIp: "10.0.0.1", StartedAt: now - 7200, UpdatedAt: now - 3600, StoppedAt: sql.NullInt64{Int64: now - 3600, Valid: true}})
	d = diag(off)
	if level(d, "Connection") != "badge-warn" || !strings.Contains(d.Advice, tr("Account and plan are fine but the device is not connected: check the customer's modem and cable")) {
		t.Errorf("offline: %+v / %q", d.Items, d.Advice)
	}

	// quota: 10 minutes of online time allowed, 12 used; and a data limit still fine
	qa := mkPlan("Limited", "Radius", 1, "Both_Limit", 10, 100)
	qc := newCust(t, q, "quota1", "Quota", "", "", "Active")
	sub(qc, qa, "active", now-5000, now+86400)
	q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "c", Username: "quota1", NasIp: "10.0.0.1", StartedAt: now - 4000, UpdatedAt: now - 3280, StoppedAt: sql.NullInt64{Int64: now - 3280, Valid: true}, InputOctets: 1 << 20})
	d = diag(qc)
	if level(d, "Quota") != "badge-bad" || d.Advice != tr("Quota used up: press Recharge for a new plan") {
		t.Errorf("quota: %+v / %q", d.Items, d.Advice)
	}
	if n := strings.Count(fmt.Sprint(d.Items), "Quota"); n != 2 {
		t.Errorf("want time and data lines, got %d: %+v", n, d.Items)
	}
	// plenty left
	q2 := newCust(t, q, "quota2", "Quota Two", "", "", "Active")
	sub(q2, qa, "active", now-5000, now+86400)
	if d = diag(q2); level(d, "Quota") != "badge-ok" {
		t.Errorf("quota left: %+v", d.Items)
	}

	// router plans: unchecked, offline, online
	rc := newCust(t, q, "router1", "Router Cust", "", "", "Active")
	sub(rc, routerPlan, "active", now-1000, now+86400)
	if d = diag(rc); level(d, "Router") != "badge-warn" {
		t.Errorf("unchecked router: %+v", d.Items)
	}
	q.SetRouterStatus(ctx, db.SetRouterStatusParams{ID: rt.ID, Online: sql.NullInt64{Int64: 0, Valid: true}})
	if d = diag(rc); level(d, "Router") != "badge-bad" || d.Advice != tr("Router offline: check its power and cables") {
		t.Errorf("offline router: %+v / %q", d.Items, d.Advice)
	}
	q.SetRouterStatus(ctx, db.SetRouterStatusParams{ID: rt.ID, Online: sql.NullInt64{Int64: 1, Valid: true}, LastSeenAt: sql.NullInt64{Int64: now, Valid: true}})
	if d = diag(rc); level(d, "Router") != "badge-ok" {
		t.Errorf("online router: %+v", d.Items)
	}

	// the card is on the customer page
	body := do(h, "GET", "/admin/customers/"+itoa(on.ID), nil, c).Body.String()
	for _, want := range []string{"Diagnosa", "Saran", "10.9.9.9"} {
		if !strings.Contains(body, want) {
			t.Errorf("customer page lacks %q", want)
		}
	}
}

func TestManifests(t *testing.T) {
	_, h, q, _ := crudApp(t)
	ctx := t.Context()
	q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "company_name", Value: "Fiber Kita Jaya Net"})
	q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "theme_accent", Value: "orange"})
	q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "theme_mode", Value: "dark"})
	type manifest struct {
		Name, ShortName, StartURL, Scope, Display, ThemeColor, BackgroundColor string
		Icons                                                                  []struct{ Src, Sizes, Type string }
	}
	read := func(path string) manifest {
		t.Helper()
		w := do(h, "GET", path, nil, nil) // public: no cookie
		wantCode(t, w, 200, path)
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
			t.Fatalf("content type %q", ct)
		}
		var raw map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		var m manifest
		m.Name, _ = raw["name"].(string)
		m.ShortName, _ = raw["short_name"].(string)
		m.StartURL, _ = raw["start_url"].(string)
		m.Scope, _ = raw["scope"].(string)
		m.Display, _ = raw["display"].(string)
		m.ThemeColor, _ = raw["theme_color"].(string)
		m.BackgroundColor, _ = raw["background_color"].(string)
		for _, i := range raw["icons"].([]any) {
			ic := i.(map[string]any)
			m.Icons = append(m.Icons, struct{ Src, Sizes, Type string }{ic["src"].(string), ic["sizes"].(string), ic["type"].(string)})
		}
		return m
	}
	a := read("/admin/manifest.webmanifest")
	if a.Name != "Fiber Kita Jaya Net Admin" || a.ShortName != "Fiber Kita J" || a.StartURL != "/admin" || a.Scope != "/admin/" || a.Display != "standalone" ||
		a.ThemeColor != "#f38020" || a.BackgroundColor != "#171b21" {
		t.Errorf("admin manifest: %+v", a)
	}
	if len(a.Icons) != 2 || a.Icons[0].Src != "/static/icons/app-192.png" || a.Icons[1].Sizes != "512x512" {
		t.Errorf("default icons: %+v", a.Icons)
	}
	p := read("/portal/manifest.webmanifest")
	if p.Name != "Fiber Kita Jaya Net" || p.StartURL != "/portal" || p.Scope != "/portal/" {
		t.Errorf("portal manifest: %+v", p)
	}
	if b := do(h, "GET", "/static/icons/app-512.png", nil, nil); b.Code != 200 || b.Header().Get("Content-Type") != "image/png" {
		t.Errorf("default icon not served: %d %s", b.Code, b.Header().Get("Content-Type"))
	}
	// uploaded logo replaces the default icons
	q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "logo", Value: strings.Repeat("a", 32) + ".png"})
	if m := read("/portal/manifest.webmanifest"); len(m.Icons) != 1 || m.Icons[0].Src != "/uploads/"+strings.Repeat("a", 32)+".png" || m.Icons[0].Type != "image/png" {
		t.Errorf("logo icon: %+v", m.Icons)
	}
	// layouts link them
	for path, want := range map[string]string{"/login": `href="/admin/manifest.webmanifest"`, "/portal/login": `href="/portal/manifest.webmanifest"`} {
		b := do(h, "GET", path, nil, nil).Body.String()
		for _, w := range []string{want, `rel="apple-touch-icon"`, `<meta name="theme-color" content="#f38020">`} {
			if !strings.Contains(b, w) {
				t.Errorf("%s lacks %s", path, w)
			}
		}
	}
}

func TestBulkDisconnect(t *testing.T) {
	s, h, q, c := crudApp(t)
	ctx, now := t.Context(), time.Now().Unix()
	sec := []byte("nas-secret")
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if p, err := radius.Parse(buf[:n], sec); err == nil {
				b, _ := p.Response(radius.CodeDisconnectACK).Encode()
				pc.WriteTo(b, from)
			}
		}
	}()
	s.CoAPort = strconv.Itoa(pc.LocalAddr().(*net.UDPAddr).Port)
	enc, _ := secret.Seal(s.SecretKey, sec)
	q.CreateNAS(ctx, db.CreateNASParams{Name: "lo", Ip: "127.0.0.1", SecretEnc: enc})

	// plan name column
	bw, _ := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	plan, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: "Gold50", Type: "PPPoE", Billing: "prepaid", Validity: 1, ValidityUnit: "Days", Device: "Radius", Enabled: 1, BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	cu := newCust(t, q, "u1", "U One", "", "", "Active")
	if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: cu.ID, PlanID: plan.ID, Type: "PPPoE", StartedAt: now - 10, ExpiresAt: now + 1000}); err != nil {
		t.Fatal(err)
	}
	add := func(user, nas string) {
		if err := q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: user, Username: user, NasIp: nas, StartedAt: now - 100, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	add("u1", "127.0.0.1")
	add("u2", "127.0.0.1")
	add("u3", "10.99.0.1") // no NAS row: fails
	ss, _ := q.SearchOpenRadiusSessions(ctx, db.SearchOpenRadiusSessionsParams{PageLimit: 10})
	ids := map[string]string{}
	for _, x := range ss {
		ids[x.Username] = itoa(x.ID)
	}
	page := do(h, "GET", "/admin/radius/sessions", nil, c).Body.String()
	for _, want := range []string{"Gold50", `name="ids"`, "disconnect-many", `form="bulk"`} {
		if !strings.Contains(page, want) {
			t.Errorf("sessions page lacks %q", want)
		}
	}
	flash := func() string { return do(h, "GET", "/admin/radius/sessions", nil, c).Body.String() }
	logCount := func() int {
		n := 0
		ls, _ := q.ListActivityLogs(ctx, db.ListActivityLogsParams{Limit: 100})
		for _, l := range ls {
			if l.Action == "radius.disconnect_many" {
				n++
			}
		}
		return n
	}

	// roles
	wantCode(t, do(h, "POST", "/admin/radius/sessions/disconnect-many", url.Values{"ids": {ids["u1"]}}, login(t, h, "rita")), 403, "report role")
	mk(t, q, "agnes", "Agent", 1)
	wantCode(t, do(h, "POST", "/admin/radius/sessions/disconnect-many", url.Values{"ids": {ids["u1"]}}, login(t, h, "agnes")), 403, "agent")
	wantCode(t, do(h, "POST", "/admin/radius/sessions/disconnect-many", url.Values{"ids": {ids["u1"]}}, nil), 303, "anonymous")
	if logCount() != 0 {
		t.Fatal("refused requests were logged")
	}

	// bad ids
	for _, bad := range []url.Values{nil, {"ids": {""}}, {"ids": {"abc"}}, {"ids": {"1", "-2"}}, {"ids": {"0"}}} {
		wantCode(t, do(h, "POST", "/admin/radius/sessions/disconnect-many", bad, c), 303, "bad ids")
		if !strings.Contains(flash(), "Pilih minimal satu") {
			t.Errorf("%v: no error flash", bad)
		}
	}
	if logCount() != 0 {
		t.Fatal("bad ids were logged")
	}

	// all fine
	wantCode(t, do(h, "POST", "/admin/radius/sessions/disconnect-many", url.Values{"ids": {ids["u1"], ids["u2"], ids["u1"]}}, c), 303, "ok")
	if !strings.Contains(flash(), "2 diputus, 0 gagal") {
		t.Error("success count missing")
	}
	// partial failure: u3 has no NAS, 999999 does not exist
	wantCode(t, do(h, "POST", "/admin/radius/sessions/disconnect-many", url.Values{"ids": {ids["u1"], ids["u3"], "999999"}}, c), 303, "partial")
	if b := flash(); !strings.Contains(b, "1 diputus, 2 gagal") {
		t.Errorf("partial counts missing:\n%s", b)
	}
	if logCount() != 2 {
		t.Errorf("want one log entry per action, got %d", logCount())
	}
	// everything fails: error flash with the counts
	wantCode(t, do(h, "POST", "/admin/radius/sessions/disconnect-many", url.Values{"ids": {ids["u3"]}}, c), 303, "all fail")
	if b := flash(); !strings.Contains(b, "0 diputus, 1 gagal") || !strings.Contains(b, "alert-error") {
		t.Error("all-fail flash wrong")
	}
	// too many
	many := url.Values{}
	for i := 1; i <= 101; i++ {
		many.Add("ids", itoa(int64(i)))
	}
	wantCode(t, do(h, "POST", "/admin/radius/sessions/disconnect-many", many, c), 303, "too many")
	if !strings.Contains(flash(), "paling banyak 100") {
		t.Error("cap not reported")
	}
}
