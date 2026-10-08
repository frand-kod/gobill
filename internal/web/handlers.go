package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

// ---- login / logout ----

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "login", Page{Title: "Sign in"})
}

type loginData struct{ Username string }

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		s.render(w, r, http.StatusTooManyRequests, "login", Page{
			Title: "Sign in",
			Error: "Too many failed attempts. Try again in 15 minutes.",
		})
		return
	}

	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")

	admin, err := s.queries.GetAdminByUsername(r.Context(), username)
	hash := s.dummyHash // same bcrypt work whether or not the user exists
	if err == nil {
		hash = []byte(admin.PasswordHash)
	}
	passOK := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if err != nil || !passOK || admin.Status != "Active" {
		s.recordFailure(ip)
		s.render(w, r, http.StatusOK, "login", Page{
			Title: "Sign in",
			Error: "Invalid Username or Password",
			Data:  loginData{Username: username},
		})
		return
	}

	s.clearFailures(ip)
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		slog.Error("renew session", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.sessions.Put(r.Context(), "admin_id", admin.ID)
	if err := s.queries.TouchAdminLogin(r.Context(), admin.ID); err != nil {
		slog.Error("touch login", "err", err)
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Destroy(r.Context()); err != nil {
		slog.Error("destroy session", "err", err)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) tooManyFailures(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recent(ip)) >= maxFailedLogins
}

func (s *Server) recordFailure(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed[ip] = append(s.recent(ip), time.Now())
}

func (s *Server) clearFailures(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failed, ip)
}

// recent drops failures older than the window. Caller holds s.mu.
func (s *Server) recent(ip string) []time.Time {
	cutoff := time.Now().Add(-loginWindow)
	list := s.failed[ip][:0]
	for _, t := range s.failed[ip] {
		if t.After(cutoff) {
			list = append(list, t)
		}
	}
	if len(list) == 0 {
		delete(s.failed, ip)
	} else {
		s.failed[ip] = list
	}
	return list
}

// ---- dashboard ----

type dashData struct {
	Warn                     string
	IncomeToday, IncomeMonth int64
	ActiveSubs, ExpiredSubs  int64
	Customers                int64
	Year                     int
	Labels, Regs, Sales      string // JSON arrays for the charts
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := s.dashboardData(r.Context(), time.Now())
	if err != nil {
		s.fail(w, "dashboard", err)
		return
	}
	if s.ClockWarning != nil {
		d.Warn = s.ClockWarning()
	}
	s.render(w, r, http.StatusOK, "dashboard", Page{
		Title: "Dashboard",
		Data:  d,
		Flash: s.sessions.PopString(r.Context(), "flash"),
	})
}

// dashboardData gathers the tiles and the per-month series of the current year in the app location.
func (s *Server) dashboardData(ctx context.Context, now time.Time) (d dashData, err error) {
	loc := s.location()
	now = now.In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	sum := func(from, to time.Time) (int64, error) {
		return s.queries.SumTransactionsBetween(ctx, db.SumTransactionsBetweenParams{CreatedAt: from.Unix(), CreatedAt_2: to.Unix()})
	}
	if d.IncomeToday, err = sum(day, day.AddDate(0, 0, 1)); err != nil {
		return
	}
	if d.IncomeMonth, err = sum(month, month.AddDate(0, 1, 0)); err != nil {
		return
	}
	if d.ActiveSubs, err = s.queries.CountSubscriptionsByStatus(ctx, "active"); err != nil {
		return
	}
	if d.ExpiredSubs, err = s.queries.CountSubscriptionsByStatus(ctx, "expired"); err != nil {
		return
	}
	if d.Customers, err = s.queries.CountCustomers(ctx); err != nil {
		return
	}
	d.Year = now.Year()
	labels, regs, sales := make([]string, 12), make([]int64, 12), make([]int64, 12)
	for m := 0; m < 12; m++ {
		from := time.Date(d.Year, time.Month(m+1), 1, 0, 0, 0, 0, loc)
		to := from.AddDate(0, 1, 0)
		labels[m] = s.catalog.T(s.language(), from.Month().String()[:3])
		if regs[m], err = s.queries.CountCustomersBetween(ctx, db.CountCustomersBetweenParams{CreatedAt: from.Unix(), CreatedAt_2: to.Unix()}); err != nil {
			return
		}
		if sales[m], err = sum(from, to); err != nil {
			return
		}
	}
	d.Labels, d.Regs, d.Sales = jsonStr(labels), jsonStr(regs), jsonStr(sales)
	return
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---- settings ----

func (s *Server) settingsForm(w http.ResponseWriter, r *http.Request) {
	values, err := s.loadSettings(r.Context())
	if err != nil {
		slog.Error("load settings", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.renderSettings(w, r, http.StatusOK, values, nil)
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, status int, v, e map[string]string) {
	fp := formPage{Heading: "Settings", Action: "/admin/settings", Cancel: "/admin", Fields: section([]field{
		text("company_name", "Company name", v, e).req(),
		text("language", "Language", v, e).opts(s.catalog.Languages()...),
		text("timezone", "Timezone", v, e).req().hint("Use a name like Asia/Jakarta."),
		text("currency_code", "Currency_Code", v, e).req(),
	}, "General", "")}
	s.render(w, r, status, "form", Page{Title: "Settings", Flash: s.sessions.PopString(r.Context(), "flash"), Data: fp})
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	values := map[string]string{}
	for _, k := range []string{"company_name", "language", "timezone", "currency_code"} {
		values[k] = strings.TrimSpace(r.PostFormValue(k))
	}

	errs := map[string]string{}
	if values["company_name"] == "" {
		errs["company_name"] = "This field is required"
	}
	if _, ok := s.catalog[values["language"]]; !ok {
		errs["language"] = "Choose one of the listed languages"
	}
	if _, err := time.LoadLocation(values["timezone"]); err != nil || values["timezone"] == "" {
		errs["timezone"] = "Unknown timezone. Use a name like Asia/Jakarta."
	}
	if values["currency_code"] == "" {
		errs["currency_code"] = "This field is required"
	}
	if len(errs) > 0 {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, values, errs)
		return
	}

	tx, err := s.conn.BeginTx(r.Context(), nil)
	if err != nil {
		slog.Error("begin", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	for k, v := range values {
		if err := q.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			slog.Error("save setting", "key", k, "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Error("commit settings", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	s.lang.Store(values["language"])
	s.sessions.Put(r.Context(), "flash", s.catalog.T(values["language"], "Settings saved"))
	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}
