package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/notify"
)

// settingsTabs are the sub-pages in menu order. Integrations is SuperAdmin only; the rest
// are SuperAdmin and Admin, as in the old settings controller. Payment waits for Tripay.
var settingsTabs = []struct {
	Slug, Label string
	SuperOnly   bool
}{
	{"app", "General", false},
	{"localisation", "Localisation", false},
	{"notifications", "Notifications", false},
	{"integrations", "Integrations", true},
	{"miscellaneous", "Miscellaneous", false},
}

// settingsSecret are write-only: never rendered, and an empty post keeps the stored value.
// ponytail: stored plaintext in settings, as the old app did; move to internal/secret if needed.
var settingsSecret = map[string]bool{"telegram_bot": true, "smtp_pass": true, "webhook_secret": true}

// settingsFile are image uploads. The setting holds the stored filename; an empty post keeps it.
var settingsFile = map[string]bool{"logo": true, "login_page_logo": true, "login_page_favicon": true, "login_page_wallpaper": true}

const maxUpload = 2 << 20

// uploadExt maps the sniffed types to extensions. SVG sniffs as text, so it is refused (XSS).
var uploadExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/x-icon": ".ico"}

var uploadName = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|webp|ico)$`)

// thousandsSep is the digit group separator used by money. Settings load and save keep it current.
var thousandsSep atomic.Value // string

func setThousandsSep(v string) {
	if v == "" {
		v = "."
	}
	thousandsSep.Store(v)
}

func groupSep() string {
	if v, ok := thousandsSep.Load().(string); ok {
		return v
	}
	return "."
}

// settingsZones is the timezone select; the server still checks any value with time.LoadLocation.
var settingsZones = []string{"Asia/Jakarta", "Asia/Makassar", "Asia/Jayapura", "Asia/Singapore", "Asia/Kuala_Lumpur",
	"Asia/Bangkok", "Asia/Kolkata", "Asia/Dubai", "Asia/Tokyo", "Asia/Shanghai", "Europe/London", "Europe/Paris",
	"Europe/Berlin", "America/New_York", "America/Chicago", "America/Los_Angeles", "Australia/Sydney", "UTC"}

var (
	settingsYesNo    = []option{{"yes", "Yes"}, {"no", "No"}}
	settingsOnOff    = []option{{"on", "On"}, {"off", "Off"}}
	settingsChannels = []option{{"", "Disabled"}, {"sms", "SMS"}, {"wa", "WhatsApp"}, {"email", "Email"}}
)

// settingsFields builds one sub-page. With nil maps it only names the fields, which is how save finds the keys.
func (s *Server) settingsFields(tab string, v, e map[string]string) []field {
	sel := func(name, label string, opts ...option) field {
		f := text(name, label, v, e).as("select")
		f.Options = opts
		return f
	}
	chk := func(name, label string) field {
		f := text(name, label, v, e).as("checkbox")
		f.Checked = v[name] == "yes"
		return f
	}
	area := func(name, label string) field { return text(name, label, v, e).as("textarea") }
	// secrets are never filled from v, so they are never rendered back
	sec := func(name, label string) field {
		return text(name, label, nil, e).as("password").hint("Leave empty to keep the current secret")
	}
	// notif shows the built-in message while the stored template is empty
	notif := func(name, label string) field {
		f := area(name, label)
		if f.Value == "" {
			f.Value = notify.DefaultTemplate(strings.TrimPrefix(name, "notif_"))
		}
		return f
	}
	// upl is an image upload; the stored filename is kept when nothing is posted.
	upl := func(name, label string) field {
		return field{Name: name, Label: label, Type: "file", Error: e[name], Hint: "PNG, JPG, WebP or ICO, 2 MB max. Leave empty to keep the current image"}
	}
	switch tab {
	case "app":
		out := section([]field{
			text("company_name", "Company Name", v, e).req(),
			text("company_footer", "Company Footer", v, e),
			area("address", "Address"),
			text("phone", "Phone Number", v, e),
			area("note", "Invoice Footer"),
			text("currency_code", "Currency Code", v, e).req(),
			upl("logo", "Company Logo"),
		}, "General", "")
		return append(out, section([]field{
			text("login_page_head", "Page Heading / Company Name", v, e),
			area("login_page_description", "Page Description"),
			upl("login_page_logo", "Login Page Logo"),
			upl("login_page_favicon", "Favicon"),
			upl("login_page_wallpaper", "Login Page Wallpaper"),
		}, "Login page", "")...)
	case "localisation":
		zones := append([]string(nil), settingsZones...)
		if z := v["timezone"]; z != "" && !contains(zones, z) {
			zones = append(zones, z)
		}
		out := section([]field{
			text("language", "Language", v, e).opts(s.catalog.Languages()...),
			text("timezone", "Timezone", v, e).opts(zones...).req().hint("Use a name like Asia/Jakarta."),
			text("date_format", "Date Format", v, e).opts("Y-m-d", "d-m-Y", "d/m/Y", "d M Y"),
			text("country_code_phone", "Country Code Phone", v, e).hint("Country code for numbers that start with 0, e.g. 62"),
		}, "Regional", "")
		return append(out, section([]field{
			text("dec_point", "Decimal Point", v, e),
			text("thousands_sep", "Thousands Separator", v, e).hint("Default is a dot: Rp 1.234.000"),
			text("reset_day", "Income Reset Date", v, e).as("number").hint("Day of the month, 1-28"),
		}, "Money", "")...)
	case "notifications":
		out := section([]field{
			notif("notif_expired", "Expired Notification Message"),
			notif("notif_reminder_7_day", "Reminder Message (7 days)"),
			notif("notif_reminder_3_day", "Reminder Message (3 days)"),
			notif("notif_reminder_1_day", "Reminder Message (1 day)"),
			notif("notif_invoice_paid", "Invoice Notification Payment"),
			notif("notif_invoice_balance", "Balance Notification Payment"),
			notif("notif_welcome_message", "Welcome Message"),
			notif("notif_balance_send", "Send Balance"),
			notif("notif_balance_received", "Received Balance"),
		}, "Message templates", "")
		out = append(out, section([]field{
			sel("user_notification_expired", "Expired Notification", settingsChannels...),
			sel("user_notification_payment", "Payment Notification", settingsChannels...),
			sel("user_notification_reminder", "Reminder Notification", settingsChannels...),
		}, "Channels", "")...)
		return append(out, section([]field{
			sel("notification_reminder_7day", "Send 7-day reminder", settingsYesNo...),
			sel("notification_reminder_3day", "Send 3-day reminder", settingsYesNo...),
			sel("notification_reminder_1day", "Send 1-day reminder", settingsYesNo...),
			text("reminder_hour", "Reminder Hour", v, e).as("number").req().hint("Hour of day, 0-23, when reminders are sent"),
		}, "Reminders", "")...)
	case "integrations":
		out := section([]field{
			sec("telegram_bot", "Telegram Bot Token"),
			text("telegram_target_id", "Telegram User/Channel/Group ID", v, e),
		}, "Telegram", "")
		out = append(out, section([]field{
			text("sms_url", "SMS Server URL", v, e).hint("Must contain [number] and [text]"),
			text("wa_url", "WhatsApp Server URL", v, e).hint("Must contain [number] and [text]"),
		}, "SMS & WhatsApp", "")...)
		out = append(out, section([]field{
			text("smtp_host", "SMTP Host", v, e),
			text("smtp_port", "SMTP Port", v, e).as("number").hint("1-65535"),
			text("smtp_user", "SMTP Username", v, e),
			sec("smtp_pass", "SMTP Password"),
			sel("smtp_ssltls", "SMTP Security", option{"", "None"}, option{"ssl", "SSL"}, option{"tls", "TLS"}),
			text("mail_from", "Mail From", v, e),
			text("mail_reply_to", "Mail Reply To", v, e),
		}, "Email (SMTP)", "")...)
		return append(out, section([]field{
			text("webhook_url", "Webhook URL", v, e).hint("http or https. Requests are signed with X-Signature"),
			sec("webhook_secret", "Webhook Secret"),
		}, "Webhook", "")...)
	case "miscellaneous":
		out := section([]field{
			sel("extend_expiry", "Extend Package Expiry", settingsYesNo...),
			sel("enable_balance", "Enable Balance System", settingsYesNo...),
			sel("voucher_format", "Voucher Format (default)", option{"up", "UPPERCASE"}, option{"low", "lowercase"},
				option{"rand", "Random case"}, option{"numbers", "Numbers"}),
		}, "Billing", "")
		out = append(out, section([]field{
			chk("man_fields_email", "Mandatory field: Email"),
			chk("man_fields_fname", "Mandatory field: Full Name"),
			chk("man_fields_address", "Mandatory field: Address"),
			sel("disable_registration", "Disable Registration", settingsYesNo...),
			sel("registration_username", "Registration Username", option{"username", "Username"}, option{"phone", "Phone"}),
			sel("sms_otp_registration", "SMS OTP Registration", settingsYesNo...),
			sel("phone_otp_type", "OTP Method", option{"sms", "SMS"}, option{"wa", "WhatsApp"}),
			sel("reg_nofify_admin", "Notify Admin", settingsYesNo...),
		}, "Registration", "")...)
		out = append(out, section([]field{
			chk("enable_session_timeout", "Enable Session Timeout"),
			text("session_timeout_duration", "Timeout Duration", v, e).as("number").hint("Minutes"),
			sel("single_session", "Single Admin Session", settingsYesNo...),
		}, "Session", "")...)
		return append(out, section([]field{
			chk("maintenance_mode", "Maintenance Mode"),
			text("maintenance_date", "Maintenance Date", v, e).as("date"),
			sel("clock_guard", "Clock Guard", settingsOnOff...).hint("Off disables the clock check"),
		}, "System", "")...)
	}
	return nil
}

func fieldNames(fs []field) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}

func inRange(x string, lo, hi int) bool {
	n, err := strconv.Atoi(x)
	return err == nil && n >= lo && n <= hi
}

// settingsErrors checks the posted values of one sub-page. Only keys present in v are checked.
func (s *Server) settingsErrors(v map[string]string) map[string]string {
	e := map[string]string{}
	for _, k := range []string{"company_name", "currency_code", "timezone", "reminder_hour"} {
		if x, ok := v[k]; ok && x == "" {
			e[k] = "This field is required"
		}
	}
	if x, ok := v["language"]; ok {
		if _, ok := s.catalog[x]; !ok {
			e["language"] = "Choose one of the listed languages"
		}
	}
	if x, ok := v["timezone"]; ok && x != "" {
		if _, err := time.LoadLocation(x); err != nil {
			e["timezone"] = "Unknown timezone. Use a name like Asia/Jakarta."
		}
	}
	if x, ok := v["reminder_hour"]; ok && x != "" && !inRange(x, 0, 23) {
		e["reminder_hour"] = "Enter a whole number from 0 to 23"
	}
	if x := v["reset_day"]; x != "" && !inRange(x, 1, 28) {
		e["reset_day"] = "Enter a day from 1 to 28"
	}
	if x := v["session_timeout_duration"]; x != "" && !inRange(x, 1, 525600) {
		e["session_timeout_duration"] = "Enter whole minutes, 1 or more"
	}
	if x := v["maintenance_date"]; x != "" {
		if _, err := time.Parse("2006-01-02", x); err != nil {
			e["maintenance_date"] = "Use a date like 2026-10-31"
		}
	}
	if x := v["phone_otp_type"]; x != "" && !oneOf(x, "sms", "wa") {
		e["phone_otp_type"] = "Choose SMS or WhatsApp"
	}
	if x := v["registration_username"]; x != "" && !oneOf(x, "username", "phone") {
		e["registration_username"] = "Choose username or phone"
	}
	if x := v["voucher_format"]; x != "" && !oneOf(x, "up", "low", "rand", "numbers") {
		e["voucher_format"] = "Choose one of the listed formats"
	}
	if x, ok := v["smtp_port"]; ok && x != "" && !inRange(x, 1, 65535) {
		e["smtp_port"] = "Enter a port from 1 to 65535"
	}
	for _, k := range []string{"sms_url", "wa_url"} {
		if x := v[k]; x != "" && !(strings.Contains(x, "[number]") && strings.Contains(x, "[text]")) {
			e[k] = "URL must contain [number] and [text]"
		}
	}
	if x := v["webhook_url"]; x != "" {
		if u, err := url.Parse(x); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			e["webhook_url"] = "Use an http or https URL"
		}
	}
	return e
}

// settingsTab reads the sub-page from the path. It 404s an unknown page and 403s a role that may not open it.
func settingsTab(w http.ResponseWriter, r *http.Request) (string, bool) {
	tab := r.PathValue("tab")
	for _, t := range settingsTabs {
		if t.Slug == tab {
			if t.SuperOnly && adminFrom(r).Role != "SuperAdmin" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return "", false
			}
			return tab, true
		}
	}
	http.NotFound(w, r)
	return "", false
}

func (s *Server) settingsForm(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("tab") == "" {
		http.Redirect(w, r, "/admin/settings/app", http.StatusSeeOther)
		return
	}
	tab, ok := settingsTab(w, r)
	if !ok {
		return
	}
	values, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "load settings", err)
		return
	}
	s.renderSettings(w, r, http.StatusOK, tab, values, nil)
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, status int, tab string, v, e map[string]string) {
	var nav []option
	for _, t := range settingsTabs {
		if !t.SuperOnly || adminFrom(r).Role == "SuperAdmin" {
			nav = append(nav, option{"/admin/settings/" + t.Slug, t.Label})
		}
	}
	fp := formPage{Heading: "Settings", Action: "/admin/settings/" + tab, Cancel: "/admin", Fields: s.settingsFields(tab, v, e)}
	s.render(w, r, status, "form", Page{Title: "Settings", Flash: s.sessions.PopString(r.Context(), "flash"), Tabs: nav, Data: fp})
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	tab, ok := settingsTab(w, r)
	if !ok {
		return
	}
	fields := s.settingsFields(tab, nil, nil)
	keys := fieldNames(fields)
	// the body limit covers the image uploads; a bigger body fails the parse, shown on the file fields
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+64<<10)
	if err := r.ParseMultipartForm(maxUpload); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		e := map[string]string{}
		for _, k := range keys {
			if settingsFile[k] {
				e[k] = "File must be 2 MB or less"
			}
		}
		s.renderSettings(w, r, http.StatusUnprocessableEntity, tab, map[string]string{}, e)
		return
	}
	v := formVals(r, keys...)
	for _, f := range fields {
		if f.Type == "checkbox" { // stored as yes/no, the PHP convention every reader expects
			v[f.Name] = map[bool]string{true: "yes", false: "no"}[v[f.Name] == "1"]
		}
	}
	if e := s.settingsErrors(v); len(e) > 0 {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, tab, v, e)
		return
	}
	e := map[string]string{}
	for _, k := range keys {
		if !settingsFile[k] || r.MultipartForm == nil || len(r.MultipartForm.File[k]) == 0 {
			continue
		}
		name, err := s.storeUpload(r.MultipartForm.File[k][0])
		if err != nil {
			e[k] = err.Error()
			continue
		}
		v[k] = name
	}
	if len(e) > 0 {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, tab, v, e)
		return
	}

	tx, err := s.conn.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, "begin", err)
		return
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	for _, k := range keys {
		if (settingsSecret[k] || settingsFile[k]) && v[k] == "" {
			continue // empty secret or image keeps the stored value
		}
		if err := q.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: k, Value: v[k]}); err != nil {
			s.fail(w, "save setting "+k, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, "commit settings", err)
		return
	}

	if l, ok := v["language"]; ok {
		s.lang.Store(l)
	}
	if x, ok := v["thousands_sep"]; ok {
		setThousandsSep(x)
	}
	if s.SettingsChanged != nil {
		s.SettingsChanged(r.Context())
	}
	s.done(w, r, "/admin/settings/"+tab, "Settings saved", "settings.save", tab)
}

// uploadDir is <db dir>/uploads, found from the open database file so no other setting is needed.
func (s *Server) uploadDir() (string, error) {
	var file string
	if err := s.conn.QueryRow("SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(file), "uploads"), nil
}

// storeUpload checks the sniffed type and size, then saves the image under a random name.
func (s *Server) storeUpload(fh *multipart.FileHeader) (string, error) {
	if fh.Size > maxUpload {
		return "", errors.New("File must be 2 MB or less")
	}
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	ext, ok := uploadExt[http.DetectContentType(head[:n])]
	if !ok {
		return "", errors.New("Use a PNG, JPG, WebP or ICO image")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	dir, err := s.uploadDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	name := hex.EncodeToString(raw) + ext
	out, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, io.LimitReader(f, maxUpload)); err != nil {
		return "", err
	}
	return name, out.Close()
}

// serveUpload serves a stored image. The name must match uploadName, so no path can leave the folder.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !uploadName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	dir, err := s.uploadDir()
	if err != nil {
		s.fail(w, "uploads dir", err)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, filepath.Join(dir, name))
}

// dbBackup streams a consistent copy of the database made with VACUUM INTO. SuperAdmin only.
func (s *Server) dbBackup(w http.ResponseWriter, r *http.Request) {
	if a := adminFrom(r); a == nil || a.Role != "SuperAdmin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	dir, err := os.MkdirTemp("", "nuxbill-backup-")
	if err != nil {
		s.fail(w, "backup dir", err)
		return
	}
	defer os.RemoveAll(dir)
	copyPath := filepath.Join(dir, "backup.db")
	if _, err := s.conn.ExecContext(r.Context(), "VACUUM INTO ?", copyPath); err != nil {
		s.fail(w, "vacuum into", err)
		return
	}
	f, err := os.Open(copyPath)
	if err != nil {
		s.fail(w, "open backup", err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.fail(w, "stat backup", err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="nuxbill-%s.db"`, time.Now().Format("20060102-150405")))
	io.Copy(w, f)
}
