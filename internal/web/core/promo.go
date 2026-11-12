package core

// Promo cards for the isolir page and the portal dashboard.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"log/slog"
	"regexp"
	"time"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/db"
)

// UploadName is the format of a stored upload: 32 hex digits and an image extension.
var UploadName = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|webp|ico)$`)

var promoCards = template.Must(template.New("").ParseFS(gobill.FS, "web/templates/promo_cards.html"))

// PromoView is one promo as the cards show it. Image is the /uploads URL, "" when none.
type PromoView struct {
	ID                       int64
	Title, Body, Link, Image string
}

// PortalPromo is the dashboard card: the first live portal promo, the rest behind "Lainnya".
// Key is the localStorage key; it changes with the promo ids and updated_at, so a change shows the card again.
type PortalPromo struct {
	Key                          string
	First                        PromoView
	More                         []PromoView
	MoreLabel, CloseLabel, Label string // translated here: the card template has no translator
}

func PromoOf(p db.Promo) PromoView {
	v := PromoView{ID: p.ID, Title: p.Title, Body: p.Body, Link: p.Link}
	if UploadName.MatchString(p.Image) {
		v.Image = "/uploads/" + p.Image
	}
	return v
}

// PromoLive: active and inside its schedule at now (unix). Both ends are inclusive.
func PromoLive(p db.Promo, now int64) bool {
	return p.Active == 1 && (!p.StartsAt.Valid || now >= p.StartsAt.Int64) && (!p.EndsAt.Valid || now <= p.EndsAt.Int64)
}

// PromoRows returns the live promos shown at place ("isolir" or "portal"), in list order.
func (s *Core) PromoRows(ctx context.Context, place string) ([]db.Promo, error) {
	rows, err := s.Queries.ListPromos(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var out []db.Promo
	for _, p := range rows {
		shown := (place == "isolir" && p.ShowIsolir == 1) || (place == "portal" && p.ShowPortal == 1)
		if shown && PromoLive(p, now) {
			out = append(out, p)
		}
	}
	return out, nil
}

// PromoBlocks is the isolir page HTML: every live isolir promo as a compact card, "" when none.
// Errors are logged, never shown, so the isolir page still loads.
func (s *Core) PromoBlocks(ctx context.Context, place string) template.HTML {
	rows, err := s.PromoRows(ctx, place)
	if err != nil {
		slog.Error("promo blocks", "place", place, "err", err)
		return ""
	}
	if len(rows) == 0 {
		return ""
	}
	views := make([]PromoView, len(rows))
	for i, p := range rows {
		views[i] = PromoOf(p)
	}
	var b bytes.Buffer
	if err := promoCards.ExecuteTemplate(&b, "promo_isolir", views); err != nil {
		slog.Error("promo blocks", "place", place, "err", err)
		return ""
	}
	return template.HTML(b.String())
}

// PromoPortal builds the dashboard card, nil when no live portal promo exists.
func (s *Core) PromoPortal(ctx context.Context) (*PortalPromo, error) {
	rows, err := s.PromoRows(ctx, "portal")
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	h := sha256.New()
	pp := &PortalPromo{First: PromoOf(rows[0]), MoreLabel: s.T("More"), CloseLabel: s.T("Close"), Label: s.T("Promotions")}
	for i, p := range rows {
		fmt.Fprintf(h, "%d:%d;", p.ID, p.UpdatedAt)
		if i > 0 {
			pp.More = append(pp.More, PromoOf(p))
		}
	}
	pp.Key = "gb-promo-" + hex.EncodeToString(h.Sum(nil))[:16]
	return pp, nil
}
