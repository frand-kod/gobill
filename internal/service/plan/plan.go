// Package plan holds the service plan write logic: validation of the plan form and the save.
package plan

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
)

// DefaultDevices is the device driver an empty "device" field falls back to, by plan type.
var DefaultDevices = map[string]string{"Hotspot": "MikrotikHotspot", "PPPoE": "MikrotikPppoe"}

// ErrNotFound is returned by Save when an update names a plan that does not exist.
var ErrNotFound = errors.New("plan not found")

type Service struct {
	DB *sql.DB
	Q  *db.Queries
}

// SaveInput is one create (ID 0) or update. V holds the form values by field name and is
// normalised in place (audience, validity unit, windows). Errs gets untranslated messages.
type SaveInput struct {
	ID   int64
	V    map[string]string
	Errs map[string]string
}

// Result: Saved is false when Errs is not empty. Old is the plan before an update.
type Result struct {
	Plan  db.Plan
	Old   db.Plan
	Saved bool
}

func oneOf(v string, list ...string) bool { return slices.Contains(list, v) }

// posInt parses a positive integer.
func posInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// wholeErr is the message for a whole-number field given a decimal ("12.5", "12,5"); "" for anything else.
func wholeErr(v string) string {
	if strings.ContainsAny(v, ".,") {
		return "Use whole numbers without dots or commas"
	}
	return ""
}

// optInt parses an optional positive integer; empty gives NULL.
func optInt(v string, e map[string]string, key string) sql.NullInt64 {
	if v == "" {
		return sql.NullInt64{}
	}
	n, ok := posInt(v)
	if !ok {
		e[key] = "Enter a number greater than 0"
	}
	return sql.NullInt64{Int64: n, Valid: ok}
}

func optStr(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }

// ScheduleError names which part of a schedule is wrong.
func ScheduleError(err error) string {
	switch m := err.Error(); {
	case strings.Contains(m, "bad day"):
		return "Unknown day name: use Mon, Tue, Wed, Thu, Fri, Sat, Sun"
	case strings.Contains(m, "bad time"):
		return "Invalid time: use HH:MM-HH:MM, from 00:00 to 24:00, start and end different"
	}
	return "Invalid rule: use DAYS HH:MM-HH:MM, e.g. Mon-Fri 08:00-17:00"
}

// Save validates in.V, adds problems to in.Errs and, if there are none, writes the plan.
// A duplicate name or an unknown router at write time is reported in Errs too.
func (s *Service) Save(ctx context.Context, in *SaveInput) (Result, error) {
	v, e, id := in.V, in.Errs, in.ID
	var old db.Plan
	if id != 0 {
		var err error
		if old, err = s.Q.GetPlan(ctx, id); err == sql.ErrNoRows {
			return Result{}, ErrNotFound
		} else if err != nil {
			return Result{}, err
		}
	}
	if v["name"] == "" {
		e["name"] = "This field is required"
	}
	if !oneOf(v["type"], "Hotspot", "PPPoE", "Balance") {
		e["type"] = "Invalid value"
	}
	if !oneOf(v["billing"], "prepaid", "postpaid") {
		e["billing"] = "Invalid value"
	}
	if !oneOf(v["validity_unit"], "Mins", "Hrs", "Days", "Months", "Period") {
		e["validity_unit"] = "Invalid value"
	}
	if !oneOf(v["device"], "", "MikrotikHotspot", "MikrotikPppoe", "Dummy", "Radius") {
		e["device"] = "Invalid value"
	}
	if v["audience"] == "" { // a form without the field (older clients) keeps the portal plan
		v["audience"] = "public"
	}
	if !oneOf(v["audience"], "public", "admin") {
		e["audience"] = "Invalid value"
	}
	price, err := strconv.ParseInt(v["price"], 10, 64)
	if err != nil || price < 0 {
		e["price"] = cmp.Or(wholeErr(v["price"]), "Enter a number of 0 or more")
	}
	balance := v["type"] == "Balance"
	validity, ok := posInt(v["validity"])
	if !ok {
		if balance && v["validity"] == "" {
			validity, v["validity_unit"] = 1, "Months" // unused for top-ups, the column is NOT NULL
		} else {
			e["validity"] = cmp.Or(wholeErr(v["validity"]), "Enter a number greater than 0")
		}
	}
	p := db.CreatePlanParams{Name: v["name"], Type: v["type"], Billing: v["billing"], Price: price, Validity: validity,
		ValidityUnit: v["validity_unit"], Device: v["device"], OnLogin: v["on_login"], OnLogout: v["on_logout"], Audience: v["audience"]}
	if v["enabled"] == "1" {
		p.Enabled = 1
	}
	p.BillingDay = optInt(v["billing_day"], e, "billing_day")
	if p.BillingDay.Valid && p.BillingDay.Int64 > 31 {
		e["billing_day"] = "Enter a day between 1 and 31"
	}
	if v["billing"] == "postpaid" && !p.BillingDay.Valid {
		e["billing_day"] = "This field is required"
	}
	if !balance {
		if p.BandwidthID = optInt(v["bandwidth_id"], e, "bandwidth_id"); !p.BandwidthID.Valid {
			e["bandwidth_id"] = "This field is required"
		}
		if p.RouterID = optInt(v["router_id"], e, "router_id"); !p.RouterID.Valid && p.Device != "Radius" {
			e["router_id"] = "This field is required" // Radius plans are served by the RADIUS server, no router
		}
		p.PoolID = optInt(v["pool_id"], e, "pool_id")
		p.ExpiredPlanID = optInt(v["expired_plan_id"], e, "expired_plan_id")
		if p.ExpiredPlanID.Valid && p.ExpiredPlanID.Int64 == id {
			e["expired_plan_id"] = "Invalid value"
		}
		p.SharedUsers = optInt(v["shared_users"], e, "shared_users")
		if p.Device == "" {
			p.Device = DefaultDevices[p.Type]
		}
		if p.Device == "Radius" {
			p.IdleTimeout = optInt(v["idle_timeout"], e, "idle_timeout")
			p.LoginWindow, p.BoostWindow = strings.TrimSpace(v["login_window"]), strings.TrimSpace(v["boost_window"])
			for _, k := range []string{"login_window", "boost_window"} {
				if _, err := radius.ParseWindow(v[k]); err != nil {
					e[k] = ScheduleError(err)
				}
			}
			p.BoostBandwidthID = optInt(v["boost_bandwidth_id"], e, "boost_bandwidth_id")
			if p.BoostBandwidthID.Valid != (p.BoostWindow != "") {
				e["boost_window"] = "Set both the boost bandwidth and the boost hours, or neither"
			}
		}
		pppoeQuota := v["type"] == "PPPoE" && p.Device == "Radius" // only RADIUS enforces a PPPoE quota, and only bytes (Session-Timeout would cut the line)
		if v["type"] == "PPPoE" && v["limited"] == "1" && !pppoeQuota {
			e["limit_type"] = "A PPPoE data limit needs the RADIUS (gobill) device; the MikroTik API cannot enforce it"
		}
		if (v["type"] == "Hotspot" || pppoeQuota) && v["limited"] == "1" {
			p.Limited = 1
			p.LimitType = optStr(v["limit_type"])
			if !oneOf(v["limit_type"], "Time_Limit", "Data_Limit", "Both_Limit") || pppoeQuota && v["limit_type"] != "Data_Limit" {
				e["limit_type"] = "Invalid value"
			}
			if v["limit_type"] != "Data_Limit" {
				if p.TimeLimit = optInt(v["time_limit"], e, "time_limit"); !p.TimeLimit.Valid {
					e["time_limit"] = "This field is required"
				}
				p.TimeUnit = optStr(v["time_unit"])
				if !oneOf(v["time_unit"], "Mins", "Hrs") {
					e["time_unit"] = "Invalid value"
				}
			}
			if v["limit_type"] != "Time_Limit" {
				if p.DataLimit = optInt(v["data_limit"], e, "data_limit"); !p.DataLimit.Valid {
					e["data_limit"] = "This field is required"
				}
				p.DataUnit = optStr(v["data_unit"])
				if !oneOf(v["data_unit"], "MB", "GB") {
					e["data_unit"] = "Invalid value"
				}
				if p.Device == "Radius" { // FUP is enforced by the RADIUS server only
					p.FupBandwidthID = optInt(v["fup_bandwidth_id"], e, "fup_bandwidth_id")
				}
			}
		}
	} else {
		p.Device = ""
	}
	if id != 0 && len(e) == 0 && (p.Type != old.Type || p.RouterID != old.RouterID || p.Device != old.Device) {
		if n, err := s.Q.CountActiveSubscriptionsOfPlan(ctx, id); err == nil && n > 0 {
			e["type"] = "Active subscriptions use this plan: the service, router and device cannot change until they end"
		}
	}
	if len(e) > 0 {
		return Result{Old: old}, nil
	}
	var saved db.Plan
	if id == 0 {
		saved, err = s.Q.CreatePlan(ctx, p)
	} else {
		err = s.Q.UpdatePlan(ctx, db.UpdatePlanParams{Name: p.Name, Type: p.Type, Billing: p.Billing, Price: p.Price,
			Validity: p.Validity, ValidityUnit: p.ValidityUnit, TimeLimit: p.TimeLimit, TimeUnit: p.TimeUnit,
			DataLimit: p.DataLimit, DataUnit: p.DataUnit, SharedUsers: p.SharedUsers, BandwidthID: p.BandwidthID,
			RouterID: p.RouterID, PoolID: p.PoolID, Enabled: p.Enabled, Limited: p.Limited, LimitType: p.LimitType,
			ExpiredPlanID: p.ExpiredPlanID, BillingDay: p.BillingDay, OnLogin: p.OnLogin, OnLogout: p.OnLogout,
			Device: p.Device, FupBandwidthID: p.FupBandwidthID, Audience: p.Audience, IdleTimeout: p.IdleTimeout, LoginWindow: p.LoginWindow, BoostWindow: p.BoostWindow, BoostBandwidthID: p.BoostBandwidthID, ID: id})
		if err == nil {
			saved, err = s.Q.GetPlan(ctx, id)
		}
	}
	switch {
	case err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed"):
		e["name"] = "Name already exists"
	case err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed"):
		e["router_id"] = "Invalid value"
	case err != nil:
		return Result{}, err
	default:
		return Result{Plan: saved, Old: old, Saved: true}, nil
	}
	return Result{Old: old}, nil
}
