// Package billing holds pure subscription math (no database).
// Ported from phpnuxbill system/autoload/Package.php rechargeUser().
package billing

// Subscription validity and expiry-date arithmetic (pure, no database).

import (
	"time"
)

type Unit string

const (
	Mins   Unit = "Mins"
	Hrs    Unit = "Hrs"
	Days   Unit = "Days"
	Months Unit = "Months"
	Period Unit = "Period"
)

// defaultBillingDay mirrors PHP: $day_exp = 20 when no attribute/plan value (Package.php:88-95).
const defaultBillingDay = 20

type Options struct {
	// Extend is true when `from` is the current expiry of an active subscription of the same
	// plan (config extend_expiry == yes); false when `from` is "now" (new activation or plan change).
	Extend bool
	// BillingDay is the day of month for Period (customer "Expired Date" attribute, else the
	// postpaid plan's expired_date, else 20). <=0 means 20. The caller resolves precedence.
	BillingDay int
}

// NewExpiry returns when a subscription starting (or extended) at from expires.
// Month arithmetic uses Go's AddDate/time.Date, which overflow exactly like PHP
// strtotime('+1 month') and DateTime::setDate (Jan 31 + 1 month = Mar 3, or Mar 2 in leap years).
func NewExpiry(from time.Time, value int, unit Unit, opts Options) time.Time {
	switch unit {
	case Mins:
		return from.Add(time.Duration(value) * time.Minute) // PHP :190, :219
	case Hrs:
		return from.Add(time.Duration(value) * time.Hour) // PHP :186, :214
	case Days:
		return from.AddDate(0, 0, value) // PHP :182, :210 (keeps time of day)
	case Months:
		return from.AddDate(0, value, 0) // PHP :145, :202 (keeps time of day)
	case Period:
		day := opts.BillingDay
		if day <= 0 {
			day = defaultBillingDay
		}
		if opts.Extend {
			return extendPeriod(from, value, day)
		}
		return freshPeriod(from, value, day)
	}
	return from
}

// extendPeriod mirrors PHP :206-207: date("Y-m-$day_exp", strtotime(exp + N months)), time 23:59:00.
// Month overflow happens first (Jan 31 + 1 month = Mar 3) and the billing day is then set in
// that month.
// Deviation: PHP builds the literal string "Y-m-31" so day 31 in a 30-day month stores an invalid
// date like 2025-04-31; here time.Date normalizes it to May 1 (same as the fresh path's setDate).
func extendPeriod(exp time.Time, value, day int) time.Time {
	m := exp.AddDate(0, value, 0)
	return time.Date(m.Year(), m.Month(), day, 23, 59, 0, 0, m.Location())
}

// freshPeriod mirrors PHP :146-180: next-month billing day, pushed so the first period is
// between 7*value and 35*value days away, then (value-1) more months. Time is 23:59:59.
func freshPeriod(now time.Time, value, day int) time.Time {
	loc := now.Location()
	cur := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	// 'first day of next month' then setDate(y, m, day)
	next := time.Date(cur.Year(), cur.Month()+1, 1, 0, 0, 0, 0, loc)
	exp := time.Date(next.Year(), next.Month(), day, 0, 0, 0, 0, loc)

	if value < 1 {
		value = 1 // PHP would loop forever on 0; not a valid plan
	}
	minDays, maxDays := 7*value, 35*value
	for daysBetween(exp, cur) < minDays {
		exp = exp.AddDate(0, 1, 0)
	}
	for daysBetween(exp, cur) > maxDays {
		exp = exp.AddDate(0, -1, 0)
	}
	if daysBetween(exp, cur) < minDays || !exp.After(cur) {
		exp = exp.AddDate(0, 1, 0)
	}
	if value > 1 {
		exp = exp.AddDate(0, value-1, 0)
	}
	return time.Date(exp.Year(), exp.Month(), exp.Day(), 23, 59, 59, 0, loc)
}

// daysBetween is |a-b| in whole days for two midnight times (DateTime::diff()->days).
func daysBetween(a, b time.Time) int {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return int(d.Hours() / 24)
}

// FirstPeriodInvoice is the prorated first postpaid invoice (PHP :432-448): price/(30*value)
// per day times days from start to expiry, capped at price. Integer math, rounded down
// (PHP stored a float). Only used when the customer has no stored "Invoice" yet.
func FirstPeriodInvoice(price int64, value int, start, expiry time.Time) int64 {
	if value < 1 {
		value = 1
	}
	s := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	e := time.Date(expiry.Year(), expiry.Month(), expiry.Day(), 0, 0, 0, 0, time.UTC)
	days := int64(daysBetween(e, s))
	got := price * days / int64(30*value)
	if got > price {
		return price
	}
	return got
}
