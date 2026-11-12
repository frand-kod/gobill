package core

import (
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/web/view"
)

// SessRow is one session in a table. The tags are the live view's JSON; Speed and Bars are set on the live page only.
type SessRow struct {
	ID    int64    `json:"id"`
	User  string   `json:"user"`
	NAS   string   `json:"nas"`
	IP    string   `json:"ip"`
	MAC   string   `json:"mac"`
	Start string   `json:"start"`
	For   string   `json:"for"`
	Up    string   `json:"up"`
	Down  string   `json:"down"`
	Stale bool     `json:"stale"`
	Plan  string   `json:"plan"`
	Speed string   `json:"speed"`
	Bars  []string `json:"bars"` // the five meter segments, see SpeedMeter.Bars
}

// SessRows turns sessions into rows; the duration of a closed session ends at stopped_at.
func (s *Core) SessRows(ss []db.RadiusSession, now int64) []SessRow {
	out := make([]SessRow, 0, len(ss))
	for _, x := range ss {
		end, stale := now, false
		if x.StoppedAt.Valid {
			end = x.StoppedAt.Int64
		} else {
			stale = now-x.UpdatedAt > radius.StaleFor(x.NasIp)
		}
		// Input is what the NAS received from the user = upload.
		out = append(out, SessRow{ID: x.ID, User: x.Username, NAS: x.NasIp, IP: x.FramedIp, MAC: x.Mac,
			Start: s.TS(x.StartedAt), For: view.HumanDur(end - x.StartedAt),
			Up: view.HumanBytes(x.InputOctets), Down: view.HumanBytes(x.OutputOctets), Stale: stale})
	}
	return out
}
