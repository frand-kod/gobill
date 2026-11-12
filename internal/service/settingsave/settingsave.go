// Package settingsave is the transaction behind saving one Settings tab: the legacy sms_url reset, the
// isolir plan and rates, stale WireGuard peers and the setting rows. No HTTP here; validation is
// settings.Validate and the after-save effects (reload, cache, files) stay with the caller.
package settingsave

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/settings"
)

// Speed are the Isolir rates. They are not stored as settings: Save writes them to the isolir bandwidth row.
var Speed = map[string]bool{"isolir_rate_down": true, "isolir_rate_up": true}

type Service struct {
	DB *sql.DB
	Q  *db.Queries
}

// Input is one tab save. V holds the validated values; Save sets V["isolir_plan_id"] when it creates the plan.
type Input struct {
	Tab       string
	Keys      []string // the fields of the tab the admin may save
	V         map[string]string
	WgRewrite bool // the WireGuard port, endpoint or subnet changed: existing peers go stale
}

// Save runs the whole write in one transaction.
func (s *Service) Save(ctx context.Context, in Input) error {
	v := in.V
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	q := s.Q.WithTx(tx)
	if in.Tab == "integrations" { // legacy sms_url: its value was moved to wa_url (the field showed it)
		if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "sms_url", Value: ""}); err != nil {
			return fmt.Errorf("save setting sms_url: %w", err)
		}
	}
	if in.Tab == "isolir" && v["isolir_enabled"] == "yes" { // the isolir plan is created the first time it is enabled
		id, err := isolir.EnsurePlan(ctx, q)
		if err != nil {
			return fmt.Errorf("isolir plan: %w", err)
		}
		v["isolir_plan_id"] = strconv.FormatInt(id, 10)
	}
	if in.Tab == "isolir" {
		if err := saveRates(ctx, q, v); err != nil {
			return fmt.Errorf("isolir rates: %w", err)
		}
	}
	if in.Tab == "wireguard" && in.WgRewrite {
		if _, err := q.MarkWgPeersStale(ctx); err != nil {
			return fmt.Errorf("mark wireguard peers: %w", err)
		}
	}
	for _, k := range in.Keys {
		if k == "wg_confirm" {
			continue // a confirmation, not a setting
		}
		if db.SecretSettings[k] && v[k] == "" {
			continue // empty secret keeps the stored value
		}
		if Speed[k] {
			continue // written to the isolir bandwidth row above
		}
		if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v[k]}); err != nil {
			return fmt.Errorf("save setting %s: %w", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit settings: %w", err)
	}
	return nil
}

// saveRates writes the typed rates to the isolir bandwidth row when the plan exists. A missing
// or invalid rate is left alone; the caller has already rejected invalid ones.
func saveRates(ctx context.Context, q *db.Queries, v map[string]string) error {
	p, err := q.GetPlan(ctx, isolir.StoredPlanID(ctx, q))
	if err != nil || !p.BandwidthID.Valid {
		return nil // no plan yet: it is made the next time Isolir is turned on
	}
	down, dunit, okDown := settings.ParseRate(v["isolir_rate_down"])
	up, uunit, okUp := settings.ParseRate(v["isolir_rate_up"])
	if !okDown || !okUp {
		return nil
	}
	b, err := q.GetBandwidth(ctx, p.BandwidthID.Int64)
	if err != nil {
		return nil
	}
	return q.UpdateBandwidth(ctx, db.UpdateBandwidthParams{Name: isolir.BandwidthName, RateDown: down, RateDownUnit: dunit,
		RateUp: up, RateUpUnit: uunit, Burst: b.Burst, ID: b.ID})
}
