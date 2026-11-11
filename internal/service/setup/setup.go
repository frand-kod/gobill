// Package setup holds the quick-setup wizard saves that need no HTTP: settings steps, plan templates.
// Steps that save a NAS, router or bandwidth go through the admin handlers in the web package, which
// have no service yet.
package setup

import (
	"context"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/payment"
	"github.com/frand-kod/gobill/internal/service/plan"
	"github.com/frand-kod/gobill/internal/settings"
)

type Service struct {
	Q       *db.Queries
	PlanSvc *plan.Service
	Langs   []string              // languages the settings validation accepts
	Saved   func(context.Context) // called after settings were written (cache drop, hooks); may be nil
}

// Put writes settings the way the Settings pages do: one upsert per key.
func (s *Service) Put(ctx context.Context, kv map[string]string) error {
	for k, v := range kv {
		if err := s.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			return err
		}
	}
	if s.Saved != nil {
		s.Saved(ctx)
	}
	return nil
}

// Company saves company_name, app_url and phone. Errs are untranslated messages by field.
func (s *Service) Company(ctx context.Context, v map[string]string) (map[string]string, error) {
	if e := settings.Validate(v, s.Langs); len(e) > 0 {
		return e, nil
	}
	return nil, s.Put(ctx, v)
}

// WhatsApp is the gateway form. An empty Password keeps the saved one; Notify counts only as "yes" or "no".
type WhatsApp struct{ URL, Device, User, Password, Notify string }

func (s *Service) WhatsApp(ctx context.Context, w WhatsApp) error {
	kv := map[string]string{"alt_wga_server_url": strings.TrimSpace(w.URL), "alt_wga_device_id": strings.TrimSpace(w.Device),
		"alt_wga_username": strings.TrimSpace(w.User)}
	if w.Password != "" {
		kv["alt_wga_password"] = w.Password
	}
	if w.Notify == "yes" || w.Notify == "no" {
		kv["notify_customers"] = w.Notify
	}
	return s.Put(ctx, kv)
}

// QRIS stores the static QRIS text, written only when it differs from current. Empty text removes it.
func (s *Service) QRIS(ctx context.Context, current, text string) (errs map[string]string, err error) {
	if text != "" && payment.QRISValid(text) != nil {
		return map[string]string{"qris_payload": "Paste the full static QRIS text, the CRC must match"}, nil
	}
	if text == current {
		return nil, nil
	}
	return nil, s.Put(ctx, map[string]string{"qris_payload": text})
}

// Tripay is the gateway form. Empty keys keep the saved secrets.
type Tripay struct {
	On                      bool
	Merchant, Mode, Channel string
	APIKey, PrivateKey      string
}

// Save returns the values it validated (for showing again) and the errors by field.
func (s *Service) Tripay(ctx context.Context, t Tripay) (kv, errs map[string]string, err error) {
	kv = map[string]string{"payment_gateway": "", "tripay_merchant_code": strings.TrimSpace(t.Merchant),
		"tripay_mode": strings.TrimSpace(t.Mode), "tripay_channel": strings.TrimSpace(t.Channel)}
	if t.On {
		kv["payment_gateway"] = "tripay"
	}
	if kv["tripay_mode"] == "" {
		kv["tripay_mode"] = "sandbox"
	}
	if t.APIKey != "" {
		kv["tripay_api_key"] = t.APIKey
	}
	if t.PrivateKey != "" {
		kv["tripay_private_key"] = t.PrivateKey
	}
	if e := settings.Validate(kv, s.Langs); len(e) > 0 {
		return kv, e, nil
	}
	return kv, nil, s.Put(ctx, kv)
}

// Isolir turns Isolir on or off. The isolir plan is created the first time, as the Settings page does.
func (s *Service) Isolir(ctx context.Context, on bool) error {
	kv := map[string]string{"isolir_enabled": "no"}
	if on {
		kv["isolir_enabled"] = "yes"
		id, err := isolir.EnsurePlan(ctx, s.Q)
		if err != nil {
			return err
		}
		kv["isolir_plan_id"] = strconv.FormatInt(id, 10)
	}
	return s.Put(ctx, kv)
}

// PlanTemplate is one plan the wizard offers. Key is the form field its price comes in.
type PlanTemplate struct {
	Key, Name string
	Price     string
	Validity  int64
	Unit      string
}

// PlansInput: Bandwidth is the bandwidth id; API plans (RouterAPI) need Router and use the Mikrotik driver,
// the others are RADIUS plans without a router.
type PlansInput struct {
	Bandwidth, Router string
	RouterAPI         bool
	Templates         []PlanTemplate
}

// Plans creates the templates that have a price and no plan of that name yet, through the same
// plan.Service the Plans page uses. Errs are by wizard field (plan_bandwidth, plan_router or the template Key).
// The caller does what the Plans page does after a save (router sync, activity log) for each created plan.
func (s *Service) Plans(ctx context.Context, in PlansInput) (created []db.Plan, errs map[string]string, err error) {
	errs = map[string]string{}
	rows, err := s.Q.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
	if err != nil {
		return nil, nil, err
	}
	have := map[string]bool{}
	for _, p := range rows {
		have[p.Name] = true
	}
	for _, t := range in.Templates {
		if t.Price == "" || have[t.Name] {
			continue
		}
		v := map[string]string{"name": t.Name, "type": "Hotspot", "billing": "prepaid", "price": t.Price,
			"validity": strconv.FormatInt(t.Validity, 10), "validity_unit": t.Unit, "bandwidth_id": in.Bandwidth,
			"enabled": "1", "audience": "public", "device": "Radius"}
		if in.RouterAPI {
			v["device"] = "MikrotikHotspot"
			v["router_id"] = in.Router
		}
		pe := map[string]string{}
		res, err := s.PlanSvc.Save(ctx, &plan.SaveInput{V: v, Errs: pe})
		if err != nil {
			return created, errs, err
		}
		if res.Saved {
			created = append(created, res.Plan)
			continue
		}
		for k, msg := range pe {
			switch k {
			case "bandwidth_id":
				errs["plan_bandwidth"] = msg
			case "router_id":
				errs["plan_router"] = msg
			default:
				errs[t.Key] = msg
			}
		}
	}
	return created, errs, nil
}
