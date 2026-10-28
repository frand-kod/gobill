package web

// Portal bell: announcement, live promo and the customer's own notices, with read state per account.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// bellItem is one line of the portal bell. Ver changes when the content does, which makes it unread again.
type bellItem struct {
	Key, Ver, Title, Text, Href string
	Unread                      bool
}

func verOf(parts ...any) string {
	h := sha256.Sum256([]byte(fmt.Sprint(parts...)))
	return hex.EncodeToString(h[:8])
}

// portalBell lists the items of this customer with Unread set from portal_seen.
func (s *Server) portalBell(r *http.Request, c *db.Customer) ([]bellItem, error) {
	ctx, now := r.Context(), time.Now().Unix()
	t := func(k string) string { return s.catalog.T(s.language(), k) }
	var items []bellItem
	if ann, err := s.queries.GetPage(ctx, "announcement"); err != nil {
		return nil, err
	} else if ann.Body != "" {
		items = append(items, bellItem{Key: "announcement", Ver: verOf(ann.Body), Title: t("Announcement"), Text: oneLine(ann.Body), Href: "/portal"})
	}
	if pp, err := s.promoPortal(ctx); err != nil {
		return nil, err
	} else if pp != nil {
		href := pp.First.Link
		if href == "" {
			href = "/portal"
		}
		items = append(items, bellItem{Key: "promo", Ver: pp.Key, Title: t("Promotions"), Text: pp.First.Title, Href: href})
	}
	subs, err := s.queries.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 50})
	if err != nil {
		return nil, err
	}
	var sub *db.Subscription
	for i := range subs {
		if subs[i].Status == "active" && subs[i].PendingStart == 0 && subs[i].ExpiresAt > now && (sub == nil || subs[i].ExpiresAt > sub.ExpiresAt) {
			sub = &subs[i]
		}
	}
	if sub != nil {
		p, _ := s.queries.GetPlan(ctx, sub.PlanID)
		if sub.ExpiresAt-now <= 3*86400 {
			items = append(items, bellItem{Key: "expiry", Ver: verOf(sub.ID, sub.ExpiresAt), Title: t("Plan ends soon"),
				Text: fmt.Sprintf(t("%s ends on %s"), p.Name, s.ts(sub.ExpiresAt)), Href: "/portal/plans"})
		}
		if q := s.planQuota(ctx, *c, *sub, p); (q.HasData && q.Data*100 > q.DataLim*80) || (q.HasTime && q.Time*100 > q.TimeLim*80) {
			items = append(items, bellItem{Key: "quota", Ver: verOf(sub.ID, sub.StartedAt), Title: t("Quota almost used up"),
				Text: t("Buy or extend a plan before it runs out"), Href: "/portal/plans"})
		}
	}
	seen, err := s.queries.ListPortalSeen(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	have := make(map[string]string, len(seen))
	for _, x := range seen {
		have[x.Item] = x.Version
	}
	for i := range items {
		items[i].Unread = have[items[i].Key] != items[i].Ver
	}
	return items, nil
}

// oneLine is the first line of s, cut to 90 characters.
func oneLine(s string) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			s = s[:i]
			break
		}
	}
	if rs := []rune(s); len(rs) > 90 {
		return string(rs[:90]) + "…"
	}
	return s
}

// pBellSeen marks every current bell item as seen (POST /portal/bell/seen).
func (s *Server) pBellSeen(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	items, err := s.portalBell(r, c)
	if err != nil {
		s.fail(w, "portal bell", err)
		return
	}
	for _, it := range items {
		if !it.Unread {
			continue
		}
		if err := s.queries.UpsertPortalSeen(r.Context(), db.UpsertPortalSeenParams{CustomerID: c.ID, Item: it.Key, Version: it.Ver}); err != nil {
			s.fail(w, "portal bell seen", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
