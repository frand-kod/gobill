package settings

// Promosi: short promo blocks. The isolir page gets promoBlocks; the portal dashboard gets one card at the
// bottom. Admin CRUD is under Settings > Promosi (/admin/settings/promo), SuperAdmin only.

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/service/promo"
)

const promoBase = "/admin/settings/promo"

func (s *Handlers) removeUpload(name string) {
	if !core.UploadName.MatchString(name) {
		return
	}
	dir, err := s.UploadDir()
	if err != nil {
		slog.Error("remove promo image", "err", err)
		return
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("remove promo image", "name", name, "err", err)
	}
}

// promoSchedule is the Schedule cell: "Always", or the dates the promo is on.
func (s *Handlers) promoSchedule(p db.Promo) string {
	day := func(x sql.NullInt64) string { return time.Unix(x.Int64, 0).In(s.Location()).Format("2006-01-02") }
	switch {
	case p.StartsAt.Valid && p.EndsAt.Valid:
		return fmt.Sprintf("%s - %s", day(p.StartsAt), day(p.EndsAt))
	case p.StartsAt.Valid:
		return fmt.Sprintf(s.T("From %s"), day(p.StartsAt))
	case p.EndsAt.Valid:
		return fmt.Sprintf(s.T("Until %s"), day(p.EndsAt))
	}
	return s.T("Always")
}

func (s *Handlers) promoList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Queries.ListPromos(r.Context())
	if err != nil {
		s.Fail(w, "list promos", err)
		return
	}
	lp := listPage{Heading: "Promotions", Base: promoBase, CanCreate: true, CanEdit: true,
		Actions: []rowAction{{Path: "up", Label: "Move up", Field: "", Confirm: false}, {Path: "down", Label: "Move down", Field: "", Confirm: false}},
		Cols:    []string{"Title", "Places", "Schedule", "Status"}}
	for _, p := range rows {
		var places []string
		if p.ShowIsolir == 1 {
			places = append(places, s.T("Isolir page"))
		}
		if p.ShowPortal == 1 {
			places = append(places, s.T("Portal dashboard"))
		}
		status := "Inactive"
		if p.Active == 1 {
			status = "Active"
		}
		lp.Rows = append(lp.Rows, listRow{ID: p.ID, Cells: []string{p.Title, strings.Join(places, ", "), s.promoSchedule(p), status}})
	}
	s.Render(w, r, http.StatusOK, "list", core.Page{Title: "Promotions", Tabs: adminx.SettingsNav(r), Flash: s.Sessions.PopString(r.Context(), "flash"),
		Error: s.Sessions.PopString(r.Context(), "error"), Data: lp})
}

func promoFields(v, e map[string]string, image string) []field {
	chk := func(name, label, hint string) field {
		return field{Name: name, Label: label, Type: "checkbox", Checked: v[name] == "1", Hint: hint}
	}
	fs := section([]field{
		{Name: "image", Label: "Image", Type: "file", Value: image, Error: e["image"],
			Hint: "PNG, JPG, WebP or ICO. 2 MB at most. Optional."},
		text("title", "Title", v, e).Req(),
		text("body", "Text", v, e).As("textarea").WithHint("Plain text. Line breaks are kept."),
		text("link", "Link", v, e).As("url").WithHint("Optional. Only https:// or http://, e.g. https://wa.me/628123456789"),
	}, "Content", "")
	fs = append(fs, section([]field{
		chk("show_isolir", "Show on the isolir page", "On by default"),
		chk("show_portal", "Show on the customer portal dashboard", "Off by default. Customers can close the card."),
		chk("active", "Active", "Off hides the promo everywhere"),
	}, "Where", "")...)
	fs = append(fs, section([]field{
		text("starts", "Start date", v, e).As("date").WithHint("Optional. Empty = from now on."),
		text("ends", "End date", v, e).As("date").WithHint("Optional. The last day it is shown."),
	}, "Schedule", "")...)
	return fs
}

func (s *Handlers) promoForm(w http.ResponseWriter, r *http.Request, status int, id int64, v, e map[string]string, image string) {
	head, action := "Add Promo", promoBase+"/new"
	if id != 0 {
		head, action = "Edit Promo", fmt.Sprintf("%s/%d", promoBase, id)
	}
	fp := formPage{Heading: head, Action: action, Cancel: promoBase, Fields: promoFields(v, e, image)}
	s.Render(w, r, status, "form", core.Page{Title: head, Tabs: adminx.SettingsNav(r), Data: fp})
}

func (s *Handlers) promoNew(w http.ResponseWriter, r *http.Request) {
	s.promoForm(w, r, http.StatusOK, 0, map[string]string{"show_isolir": "1", "active": "1"}, nil, "")
}

func (s *Handlers) promoEdit(w http.ResponseWriter, r *http.Request) {
	p, err := s.Queries.GetPromo(r.Context(), httpx.PathID(r))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get promo", err)
		return
	}
	date := func(x sql.NullInt64) string {
		if !x.Valid {
			return ""
		}
		return time.Unix(x.Int64, 0).In(s.Location()).Format("2006-01-02")
	}
	v := map[string]string{"title": p.Title, "body": p.Body, "link": p.Link, "starts": date(p.StartsAt), "ends": date(p.EndsAt),
		"show_isolir": fmt.Sprint(p.ShowIsolir), "show_portal": fmt.Sprint(p.ShowPortal), "active": fmt.Sprint(p.Active)}
	s.promoForm(w, r, http.StatusOK, p.ID, v, nil, p.Image)
}

// promoSave creates (POST /new) or updates (POST /{id}). A new image is stored only when the rest is valid,
// and the replaced or removed image is deleted from disk after the row is saved.
func (s *Handlers) promoSave(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	if id == 0 && r.PathValue("id") != "" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	var old db.Promo
	if id != 0 {
		p, err := s.Queries.GetPromo(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.Fail(w, "get promo", err)
			return
		}
		old = p
	}
	r.Body = http.MaxBytesReader(w, r.Body, adminx.MaxUpload+64<<10)
	e := map[string]string{}
	if err := r.ParseMultipartForm(adminx.MaxUpload); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		e["image"] = "File must be 2 MB or less"
	}
	v := httpx.FormVals(r, "title", "body", "link", "starts", "ends")
	for _, k := range []string{"show_isolir", "show_portal", "active"} {
		v[k] = ""
		if r.PostFormValue(k) == "1" {
			v[k] = "1"
		}
	}
	parsed := s.promos().Validate(v, e)
	image := old.Image
	if r.PostFormValue("image_remove") == "1" {
		image = ""
	}
	added := ""
	if len(e) == 0 && r.MultipartForm != nil && len(r.MultipartForm.File["image"]) > 0 {
		name, err := s.StoreUpload(r.MultipartForm.File["image"][0])
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

	err := s.promos().Save(ctx, id, parsed, image)
	if err != nil {
		if added != "" {
			s.removeUpload(added)
		}
		s.Fail(w, "save promo", err)
		return
	}
	if old.Image != image {
		s.removeUpload(old.Image)
	}
	act, msg := "promo.create", "Data Created Successfully"
	if id != 0 {
		act, msg = "promo.update", "Data Updated Successfully"
	}
	s.Done(w, r, promoBase, msg, act, v["title"])
}

func (s *Handlers) promoDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.Queries.GetPromo(ctx, httpx.PathID(r))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get promo", err)
		return
	}
	if err := s.Queries.DeletePromo(ctx, p.ID); err != nil {
		s.Fail(w, "delete promo", err)
		return
	}
	s.removeUpload(p.Image)
	s.Done(w, r, promoBase, "Data Deleted Successfully", "promo.delete", p.Title)
}

// promoMove swaps a promo with its neighbour (dir -1 up, +1 down) and renumbers the positions 1..n.
func (s *Handlers) promoMove(dir int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if err := s.promos().Move(ctx, httpx.PathID(r), dir); err != nil {
			s.Fail(w, "move promo", err)
			return
		}
		http.Redirect(w, r, promoBase, http.StatusSeeOther)
	}
}

func (s *Handlers) promos() *promo.Service {
	return &promo.Service{DB: s.Conn, Q: s.Queries, Loc: s.Location(), Now: time.Now}
}
