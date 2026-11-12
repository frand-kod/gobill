// Package promo holds the promo write logic: validation of the promo form, the save and the reorder.
// Image files are handled by the caller; the service only gets the stored file name.
package promo

import (
	"context"
	"database/sql"
	"net/url"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/frand-kod/gobill/internal/db"
)

const (
	TitleMax = 100
	BodyMax  = 1000
	LinkMax  = 500
)

type Service struct {
	DB  *sql.DB
	Q   *db.Queries
	Loc *time.Location // zone of the start and end dates
	Now func() time.Time
}

// Form holds the form values by field name: title, body, link, starts, ends and the flags
// show_isolir, show_portal, active ("1" is on).
type Form map[string]string

// Parsed is a validated form, ready to save.
type Parsed struct {
	Form         Form
	Starts, Ends sql.NullInt64
}

// LinkOK accepts only absolute http and https links (no javascript:, data: or bare text).
func LinkOK(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func flag(v string) int64 {
	if v == "1" {
		return 1
	}
	return 0
}

// Validate checks the form. Errs are untranslated messages by field; the map is added to in place.
func (s *Service) Validate(v Form, errs map[string]string) Parsed {
	if v["title"] == "" {
		errs["title"] = "This field is required"
	} else if utf8.RuneCountInString(v["title"]) > TitleMax {
		errs["title"] = "Too long"
	}
	if utf8.RuneCountInString(v["body"]) > BodyMax {
		errs["body"] = "Too long"
	}
	if v["link"] != "" && (len(v["link"]) > LinkMax || !LinkOK(v["link"])) {
		errs["link"] = "Use an https:// or http:// link"
	}
	p := Parsed{Form: v}
	if v["starts"] != "" {
		if t, err := time.ParseInLocation("2006-01-02", v["starts"], s.Loc); err != nil {
			errs["starts"] = "Invalid date"
		} else {
			p.Starts = sql.NullInt64{Int64: t.Unix(), Valid: true}
		}
	}
	if v["ends"] != "" {
		if t, err := time.ParseInLocation("2006-01-02", v["ends"], s.Loc); err != nil {
			errs["ends"] = "Invalid date"
		} else {
			p.Ends = sql.NullInt64{Int64: t.AddDate(0, 0, 1).Unix() - 1, Valid: true} // last second of that day
		}
	}
	if p.Starts.Valid && p.Ends.Valid && p.Ends.Int64 < p.Starts.Int64 {
		errs["ends"] = "End date must be after start date"
	}
	return p
}

// Save creates (id 0) or updates a promo with the stored image name ("" for none).
func (s *Service) Save(ctx context.Context, id int64, p Parsed, image string) error {
	v := p.Form
	now := s.Now().Unix()
	if id == 0 {
		pos, err := s.Q.MaxPromoPosition(ctx)
		if err != nil {
			return err
		}
		_, err = s.Q.CreatePromo(ctx, db.CreatePromoParams{Title: v["title"], Body: v["body"], Image: image, Link: v["link"],
			ShowIsolir: flag(v["show_isolir"]), ShowPortal: flag(v["show_portal"]), Active: flag(v["active"]),
			StartsAt: p.Starts, EndsAt: p.Ends, Position: pos + 1, UpdatedAt: now})
		return err
	}
	return s.Q.UpdatePromo(ctx, db.UpdatePromoParams{Title: v["title"], Body: v["body"], Image: image, Link: v["link"],
		ShowIsolir: flag(v["show_isolir"]), ShowPortal: flag(v["show_portal"]), Active: flag(v["active"]),
		StartsAt: p.Starts, EndsAt: p.Ends, UpdatedAt: now, ID: id})
}

// Move swaps a promo with its neighbour (dir -1 up, +1 down) and renumbers the positions 1..n.
func (s *Service) Move(ctx context.Context, id int64, dir int) error {
	rows, err := s.Q.ListPromos(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(rows, func(p db.Promo) bool { return p.ID == id })
	j := i + dir
	if i < 0 || j < 0 || j >= len(rows) {
		return nil
	}
	rows[i], rows[j] = rows[j], rows[i]
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := s.Q.WithTx(tx)
	for n, p := range rows {
		if p.Position != int64(n+1) {
			if err := q.SetPromoPosition(ctx, db.SetPromoPositionParams{Position: int64(n + 1), ID: p.ID}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
