package web

// Network health: the dashboard card listing every router and NAS, and the detail page of each.
// The card reads the database only. The detail page calls a router only when the operator asks.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/secret"
)

// ponytail: a NAS counts as online while its newest RADIUS packet is this recent; no heartbeat is stored.
const nasFreshFor = 15 * time.Minute

// netRow is one line of the dashboard card.
type netRow struct {
	Kind, Name, Type, Status, Dot, Metric, Href string
}

// netCard is the dashboard card: every device, and how many are online.
type netCard struct {
	Rows   []netRow
	Online int
}

// netStat is what the RADIUS side saw of one NAS, keyed by its packet source IP.
type netStat struct {
	LastSeen int64
	Active   int64
}

func netHref(kind string, id int64) string { return fmt.Sprintf("/admin/network/%s/%d", kind, id) }

// nasStats returns the RADIUS packet stats of every NAS address that ever sent a packet.
func (s *Server) nasStats(ctx context.Context) (map[string]netStat, error) {
	rows, err := s.queries.ListNASPacketStats(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]netStat, len(rows))
	for _, x := range rows {
		m[x.NasIp] = netStat{LastSeen: x.LastSeen, Active: x.Active}
	}
	return m, nil
}

// networkCard lists the routers and NAS with their status. now decides whether a NAS is fresh.
func (s *Server) networkCard(ctx context.Context, now time.Time) (netCard, error) {
	var c netCard
	routers, err := s.queries.ListAllRouters(ctx)
	if err != nil {
		return c, err
	}
	nas, err := s.queries.ListNAS(ctx)
	if err != nil {
		return c, err
	}
	stats, err := s.nasStats(ctx)
	if err != nil {
		return c, err
	}
	for _, r := range routers {
		c.Rows = append(c.Rows, s.routerRow(r))
	}
	for _, n := range nas {
		c.Rows = append(c.Rows, s.nasRow(n, stats, now))
	}
	for _, r := range c.Rows {
		if r.Status == "Online" {
			c.Online++
		}
	}
	return c, nil
}

// routerStatus reads the monitor's online flag; a disabled router is not checked, so it stays unknown.
func routerStatus(r db.Router) string {
	switch {
	case r.Enabled == 0 || !r.Online.Valid:
		return "Unknown"
	case r.Online.Int64 == 1:
		return "Online"
	}
	return "Offline"
}

func (s *Server) routerRow(r db.Router) netRow {
	lang := s.language()
	row := netRow{Kind: "router", Name: r.Name, Type: "Router API", Href: netHref("router", r.ID), Status: routerStatus(r)}
	switch {
	case r.Enabled == 0:
		row.Metric = s.catalog.T(lang, "Disabled, not checked")
	case r.LastSeenAt.Valid:
		row.Metric = fmt.Sprintf(s.catalog.T(lang, "Last online: %s"), s.ts(r.LastSeenAt.Int64))
	default:
		row.Metric = s.catalog.T(lang, "Never seen online")
	}
	row.Dot = statusDot(row.Status)
	return row
}

func (s *Server) nasRow(n db.Na, stats map[string]netStat, now time.Time) netRow {
	lang := s.language()
	row := netRow{Kind: "nas", Name: n.Name, Type: "NAS RADIUS", Href: netHref("nas", n.ID), Status: "Unknown"}
	st, ok := stats[n.Ip]
	if !ok {
		row.Metric = s.catalog.T(lang, "No packet yet")
	} else {
		row.Metric = fmt.Sprintf(s.catalog.T(lang, "%d active sessions, last packet %s"), st.Active, s.ts(st.LastSeen))
		row.Status = "Offline"
		if now.Sub(time.Unix(st.LastSeen, 0)) <= nasFreshFor {
			row.Status = "Online"
		}
	}
	row.Dot = statusDot(row.Status)
	return row
}

func statusDot(status string) string {
	switch status {
	case "Online":
		return "bg-ok-fg"
	case "Offline":
		return "bg-err-fg"
	}
	return "bg-ink-2"
}

// netLive is a successful live check, formatted for the page.
type netLive struct {
	Uptime, Version, Board, CPU, Memory, Hotspot, PPP string
	At                                                string
}

// netPage is the detail page of one router or NAS.
type netPage struct {
	Kind, Name, TypeLabel, Address, Description string
	Row                                         netRow
	Enabled                                     bool
	Edit                                        string
	APIUser                                     string
	CanCheck                                    bool // router with API credentials: "Cek sekarang" is shown
	Live                                        *netLive
	LiveErr, LiveRaw                            string
	// NAS only: what the RADIUS side knows.
	Active     int64
	LastPacket string
	SecretOK   bool
	SecretMsg  string
}

// routerPage builds the router detail. A check is requested only by the POST handler.
func (s *Server) routerPage(x db.Router) netPage {
	return netPage{Kind: "router", Name: x.Name, TypeLabel: "Router API", Address: fmt.Sprintf("%s:%d", x.Host, x.Port),
		Description: x.Description, Enabled: x.Enabled == 1, Edit: fmt.Sprint("/admin/routers/", x.ID, "/edit"),
		APIUser: x.Username, CanCheck: x.Username != "" && len(x.PasswordEnc) > 0,
		Row: s.routerRow(x)}
}

// nasPage builds the NAS detail from its RADIUS-side data only.
func (s *Server) nasPage(ctx context.Context, n db.Na, now time.Time) (netPage, error) {
	lang := s.language()
	stats, err := s.nasStats(ctx)
	if err != nil {
		return netPage{}, err
	}
	p := netPage{Kind: "nas", Name: n.Name, TypeLabel: "NAS RADIUS", Address: n.Ip, Description: n.Description,
		Enabled: true, Edit: fmt.Sprint("/admin/nas/", n.ID, "/edit"), Row: s.nasRow(n, stats, now)}
	if st, ok := stats[n.Ip]; ok {
		p.Active, p.LastPacket = st.Active, s.ts(st.LastSeen)
	} else {
		p.LastPacket = s.catalog.T(lang, "No packet yet")
	}
	if plain, err := secret.Open(s.SecretKey, n.SecretEnc); err != nil {
		p.SecretMsg = s.catalog.T(lang, "The secret could not be read. Set it again under NAS.")
	} else if m := weakSecret(string(plain)); m != "" {
		p.SecretMsg = s.catalog.T(lang, m)
	} else {
		p.SecretOK = true
	}
	return p, nil
}

// liveView formats a router health reply for the page.
func (s *Server) liveView(h device.Health) *netLive {
	lang := s.language()
	const mb = 1 << 20
	return &netLive{
		Uptime:  h.Uptime,
		Version: h.Version,
		Board:   h.Board,
		CPU:     fmt.Sprintf("%d%%", h.CPULoad),
		Memory:  fmt.Sprintf(s.catalog.T(lang, "%d MB free of %d MB"), h.FreeMem/mb, h.TotalMem/mb),
		Hotspot: fmt.Sprint(h.Hotspot),
		PPP:     fmt.Sprint(h.PPP),
		At:      s.ts(time.Now().Unix()),
	}
}

// networkPage serves the detail page; check runs the live router check first (POST).
func (s *Server) networkPage(w http.ResponseWriter, r *http.Request, check bool) {
	kind, id, ctx := r.PathValue("kind"), pathID(r), r.Context()
	var p netPage
	switch kind {
	case "router":
		x, err := s.queries.GetRouter(ctx, id)
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.fail(w, "get router", err)
			return
		}
		p = s.routerPage(x)
		if check && p.CanCheck {
			s.liveCheck(ctx, x, &p)
		}
	case "nas":
		if check {
			http.NotFound(w, r)
			return
		}
		n, err := s.queries.GetNAS(ctx, id)
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.fail(w, "get nas", err)
			return
		}
		if p, err = s.nasPage(ctx, n, time.Now()); err != nil {
			s.fail(w, "nas health", err)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	s.render(w, r, http.StatusOK, "network", Page{Title: p.Name, Data: p})
}

// liveCheck asks the router for its health with a short timeout. Errors are mapped to plain text.
func (s *Server) liveCheck(ctx context.Context, x db.Router, p *netPage) {
	lang := s.language()
	if s.Billing == nil {
		p.LiveErr = s.catalog.T(lang, "Router connection is not configured")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h, err := s.Billing.Health(cctx, x)
	if err != nil {
		p.LiveErr = s.catalog.T(lang, routerErr(err, msgRouterOther))
		p.LiveRaw = err.Error()
		if len(p.LiveRaw) > 300 {
			p.LiveRaw = p.LiveRaw[:300]
		}
		return
	}
	p.Live = s.liveView(h)
}

func (s *Server) networkDetail(w http.ResponseWriter, r *http.Request) { s.networkPage(w, r, false) }

func (s *Server) networkCheck(w http.ResponseWriter, r *http.Request) { s.networkPage(w, r, true) }
