package web

// Promosi: short promo blocks. The isolir page gets promoBlocks; the portal dashboard gets one card at the
// bottom. Admin CRUD is under Settings > Promosi (/admin/settings/promo), SuperAdmin only.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/db"
)

const (
	promoTitleMax = 100
	promoBodyMax  = 1000
	promoLinkMax  = 500
	promoBase     = "/admin/settings/promo"
)

var promoCards = template.Must(template.New("").ParseFS(gobill.FS, "web/templates/promo_cards.html"))

// promoView is one promo as the cards show it. Image is the /uploads URL, "" when none.
type promoView struct {
	ID                       int64
	Title, Body, Link, Image string
}

// portalPromo is the dashboard card: the first live portal promo, the rest behind "Lainnya".
// Key is the localStorage key; it changes with the promo ids and updated_at, so a change shows the card again.
type portalPromo struct {
	Key                   string
	First                 promoView
	More                  []promoView
	MoreLabel, CloseLabel, Label string // translated here: the card template has no translator
}

func promoOf(p db.Promo) promoView {
	v := promoView{ID: p.ID, Title: p.Title, Body: p.Body, Link: p.Link}
	if uploadName.MatchString(p.Image) {
		v.Image = "/uploads/" + p.Image
	}
	return v
}

// promoLive: active and inside its schedule at now (unix). Both ends are inclusive.
func promoLive(p db.Promo, now int64) bool {
	return p.Active == 1 && (!p.StartsAt.Valid || now >= p.StartsAt.Int64) && (!p.EndsAt.Valid || now <= p.EndsAt.Int64)
}

// promoRows returns the live promos shown at place ("isolir" or "portal"), in list order.
func (s *Server) promoRows(ctx context.Context, place string) ([]db.Promo, error) {
	rows, err := s.queries.ListPromos(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var out []db.Promo
	for _, p := range rows {
		shown := (place == "isolir" && p.ShowIsolir == 1) || (place == "portal" && p.ShowPortal == 1)
		if shown && promoLive(p, now) {
			out = append(out, p)
		}
	}
	return out, nil
}

// promoBlocks is the isolir page HTML: every live isolir promo as a compact card, "" when none.
// Errors are logged, never shown, so the isolir page still loads.
func (s *Server) promoBlocks(ctx context.Context, place string) template.HTML {
	rows, err := s.promoRows(ctx, place)
	if err != nil {
		slog.Error("promo blocks", "place", place, "err", err)
		return ""
	}
	if len(rows) == 0 {
		return ""
	}
	views := make([]promoView, len(rows))
	for i, p := range rows {
		views[i] = promoOf(p)
	}
	var b bytes.Buffer
	if err := promoCards.ExecuteTemplate(&b, "promo_isolir", views); err != nil {
		slog.Error("promo blocks", "place", place, "err", err)
		return ""
	}
	return template.HTML(b.String())
}

// promoPortal builds the dashboard card, nil when no live portal promo exists.
func (s *Server) promoPortal(ctx context.Context) (*portalPromo, error) {
	rows, err := s.promoRows(ctx, "portal")
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	h := sha256.New()
	pp := &portalPromo{First: promoOf(rows[0]), MoreLabel: s.catalog.T(s.language(), "More"), CloseLabel: s.catalog.T(s.language(), "Close"), Label: s.catalog.T(s.language(), "Promotions")}
	for i, p := range rows {
		fmt.Fprintf(h, "%d:%d;", p.ID, p.UpdatedAt)
		if i > 0 {
			pp.More = append(pp.More, promoOf(p))
		}
	}
	pp.Key = "gb-promo-" + hex.EncodeToString(h.Sum(nil))[:16]
	return pp, nil
}

// promoLinkOK accepts only absolute http and https links (no javascript:, data: or bare text).
func promoLinkOK(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// removeUpload deletes one stored promo image. Only upload-format names are touched.
func (s *Server) removeUpload(name string) {
	if !uploadName.MatchString(name) {
		return
	}
	dir, err := s.uploadDir()
	if err != nil {
		slog.Error("remove promo image", "err", err)
		return
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("remove promo image", "name", name, "err", err)
	}
}

func promoFlag(v string) int64 {
	if v == "1" {
		return 1
	}
	return 0
}

// promoSchedule is the Schedule cell: "Always", or the dates the promo is on.
func (s *Server) promoSchedule(p db.Promo) string {
	t := func(k string) string { return s.catalog.T(s.language(), k) }
	day := func(x sql.NullInt64) string { return time.Unix(x.Int64, 0).In(s.location()).Format("2006-01-02") }
	switch {
	case p.StartsAt.Valid && p.EndsAt.Valid:
		return fmt.Sprintf("%s - %s", day(p.StartsAt), day(p.EndsAt))
	case p.StartsAt.Valid:
		return fmt.Sprintf(t("From %s"), day(p.StartsAt))
	case p.EndsAt.Valid:
		return fmt.Sprintf(t("Until %s"), day(p.EndsAt))
	}
	return t("Always")
}

func (s *Server) promoList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListPromos(r.Context())
	if err != nil {
		s.fail(w, "list promos", err)
		return
	}
	t := func(k string) string { return s.catalog.T(s.language(), k) }
	lp := listPage{Heading: "Promotions", Base: promoBase, CanCreate: true, CanEdit: true,
		Actions: []rowAction{{"up", "Move up", "", false}, {"down", "Move down", "", false}},
		Cols:    []string{"Title", "Places", "Schedule", "Status"}}
	for _, p := range rows {
		var places []string
		if p.ShowIsolir == 1 {
			places = append(places, t("Isolir page"))
		}
		if p.ShowPortal == 1 {
			places = append(places, t("Portal dashboard"))
		}
		status := "Inactive"
		if p.Active == 1 {
			status = "Active"
		}
		lp.Rows = append(lp.Rows, listRow{p.ID, []string{p.Title, strings.Join(places, ", "), s.promoSchedule(p), status}})
	}
	s.render(w, r, http.StatusOK, "list", Page{Title: "Promotions", Tabs: settingsNav(r), Flash: s.sessions.PopString(r.Context(), "flash"),
		Error: s.sessions.PopString(r.Context(), "error"), Data: lp})
}

func promoFields(v, e map[string]string, image string) []field {
	chk := func(name, label, hint string) field {
		return field{Name: name, Label: label, Type: "checkbox", Checked: v[name] == "1", Hint: hint}
	}
	fs := section([]field{
		{Name: "image", Label: "Image", Type: "file", Value: image, Error: e["image"],
			Hint: "PNG, JPG, WebP or ICO. 2 MB at most. Optional."},
		text("title", "Title", v, e).req(),
		text("body", "Text", v, e).as("textarea").hint("Plain text. Line breaks are kept."),
		text("link", "Link", v, e).as("url").hint("Optional. Only https:// or http://, e.g. https://wa.me/628123456789"),
	}, "Content", "")
	fs = append(fs, section([]field{
		chk("show_isolir", "Show on the isolir page", "On by default"),
		chk("show_portal", "Show on the customer portal dashboard", "Off by default. Customers can close the card."),
		chk("active", "Active", "Off hides the promo everywhere"),
	}, "Where", "")...)
	fs = append(fs, section([]field{
		text("starts", "Start date", v, e).as("date").hint("Optional. Empty = from now on."),
		text("ends", "End date", v, e).as("date").hint("Optional. The last day it is shown."),
	}, "Schedule", "")...)
	return fs
}

func (s *Server) promoForm(w http.ResponseWriter, r *http.Request, status int, id int64, v, e map[string]string, image string) {
	head, action := "Add Promo", promoBase+"/new"
	if id != 0 {
		head, action = "Edit Promo", fmt.Sprintf("%s/%d", promoBase, id)
	}
	fp := formPage{Heading: head, Action: action, Cancel: promoBase, Fields: promoFields(v, e, image)}
	s.render(w, r, status, "form", Page{Title: head, Tabs: settingsNav(r), Data: fp})
}

func (s *Server) promoNew(w http.ResponseWriter, r *http.Request) {
	s.promoForm(w, r, http.StatusOK, 0, map[string]string{"show_isolir": "1", "active": "1"}, nil, "")
}

func (s *Server) promoEdit(w http.ResponseWriter, r *http.Request) {
	p, err := s.queries.GetPromo(r.Context(), pathID(r))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get promo", err)
		return
	}
	date := func(x sql.NullInt64) string {
		if !x.Valid {
			return ""
		}
		return time.Unix(x.Int64, 0).In(s.location()).Format("2006-01-02")
	}
	v := map[string]string{"title": p.Title, "body": p.Body, "link": p.Link, "starts": date(p.StartsAt), "ends": date(p.EndsAt),
		"show_isolir": fmt.Sprint(p.ShowIsolir), "show_portal": fmt.Sprint(p.ShowPortal), "active": fmt.Sprint(p.Active)}
	s.promoForm(w, r, http.StatusOK, p.ID, v, nil, p.Image)
}

// promoSave creates (POST /new) or updates (POST /{id}). A new image is stored only when the rest is valid,
// and the replaced or removed image is deleted from disk after the row is saved.
func (s *Server) promoSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if id == 0 && r.PathValue("id") != "" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	var old db.Promo
	if id != 0 {
		p, err := s.queries.GetPromo(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.fail(w, "get promo", err)
			return
		}
		old = p
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+64<<10)
	e := map[string]string{}
	if err := r.ParseMultipartForm(maxUpload); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		e["image"] = "File must be 2 MB or less"
	}
	v := formVals(r, "title", "body", "link", "starts", "ends")
	for _, k := range []string{"show_isolir", "show_portal", "active"} {
		v[k] = ""
		if r.PostFormValue(k) == "1" {
			v[k] = "1"
		}
	}
	if v["title"] == "" {
		e["title"] = "This field is required"
	} else if utf8.RuneCountInString(v["title"]) > promoTitleMax {
		e["title"] = "Too long"
	}
	if utf8.RuneCountInString(v["body"]) > promoBodyMax {
		e["body"] = "Too long"
	}
	if v["link"] != "" && (len(v["link"]) > promoLinkMax || !promoLinkOK(v["link"])) {
		e["link"] = "Use an https:// or http:// link"
	}
	loc := s.location()
	var starts, ends sql.NullInt64
	if v["starts"] != "" {
		if t, err := time.ParseInLocation("2006-01-02", v["starts"], loc); err != nil {
			e["starts"] = "Invalid date"
		} else {
			starts = sql.NullInt64{Int64: t.Unix(), Valid: true}
		}
	}
	if v["ends"] != "" {
		if t, err := time.ParseInLocation("2006-01-02", v["ends"], loc); err != nil {
			e["ends"] = "Invalid date"
		} else {
			ends = sql.NullInt64{Int64: t.AddDate(0, 0, 1).Unix() - 1, Valid: true} // last second of that day
		}
	}
	if starts.Valid && ends.Valid && ends.Int64 < starts.Int64 {
		e["ends"] = "End date must be after start date"
	}
	image := old.Image
	if r.PostFormValue("image_remove") == "1" {
		image = ""
	}
	added := ""
	if len(e) == 0 && r.MultipartForm != nil && len(r.MultipartForm.File["image"]) > 0 {
		name, err := s.storeUpload(r.MultipartForm.File["image"][0])
		if err != nil {
			e["image"] = err.Error()
		} else {
			added, image = name, name
		}
	}
	if len(e) > 0 {
		s.promoForm(w, r, http.StatusUnprocessableEntity, id, v, e, old.Image)
		return
	}

	now := time.Now().Unix()
	var err error
	if id == 0 {
		var pos int64
		if pos, err = s.queries.MaxPromoPosition(ctx); err == nil {
			_, err = s.queries.CreatePromo(ctx, db.CreatePromoParams{Title: v["title"], Body: v["body"], Image: image, Link: v["link"],
				ShowIsolir: promoFlag(v["show_isolir"]), ShowPortal: promoFlag(v["show_portal"]), Active: promoFlag(v["active"]),
				StartsAt: starts, EndsAt: ends, Position: pos + 1, UpdatedAt: now})
		}
	} else {
		err = s.queries.UpdatePromo(ctx, db.UpdatePromoParams{Title: v["title"], Body: v["body"], Image: image, Link: v["link"],
			ShowIsolir: promoFlag(v["show_isolir"]), ShowPortal: promoFlag(v["show_portal"]), Active: promoFlag(v["active"]),
			StartsAt: starts, EndsAt: ends, UpdatedAt: now, ID: id})
	}
	if err != nil {
		if added != "" {
			s.removeUpload(added)
		}
		s.fail(w, "save promo", err)
		return
	}
	if old.Image != image {
		s.removeUpload(old.Image)
	}
	act, msg := "promo.create", "Data Created Successfully"
	if id != 0 {
		act, msg = "promo.update", "Data Updated Successfully"
	}
	s.done(w, r, promoBase, msg, act, v["title"])
}

func (s *Server) promoDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.queries.GetPromo(ctx, pathID(r))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get promo", err)
		return
	}
	if err := s.queries.DeletePromo(ctx, p.ID); err != nil {
		s.fail(w, "delete promo", err)
		return
	}
	s.removeUpload(p.Image)
	s.done(w, r, promoBase, "Data Deleted Successfully", "promo.delete", p.Title)
}

// promoMove swaps a promo with its neighbour (dir -1 up, +1 down) and renumbers the positions 1..n.
func (s *Server) promoMove(dir int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		rows, err := s.queries.ListPromos(ctx)
		if err != nil {
			s.fail(w, "list promos", err)
			return
		}
		id := pathID(r)
		i := slices.IndexFunc(rows, func(p db.Promo) bool { return p.ID == id })
		if j := i + dir; i >= 0 && j >= 0 && j < len(rows) {
			rows[i], rows[j] = rows[j], rows[i]
			tx, err := s.conn.BeginTx(ctx, nil)
			if err != nil {
				s.fail(w, "begin promo move", err)
				return
			}
			defer tx.Rollback()
			q := s.queries.WithTx(tx)
			for n, p := range rows {
				if p.Position != int64(n+1) {
					if err := q.SetPromoPosition(ctx, db.SetPromoPositionParams{Position: int64(n + 1), ID: p.ID}); err != nil {
						s.fail(w, "move promo", err)
						return
					}
				}
			}
			if err := tx.Commit(); err != nil {
				s.fail(w, "commit promo move", err)
				return
			}
		}
		http.Redirect(w, r, promoBase, http.StatusSeeOther)
	}
}
