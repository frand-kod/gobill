// Package billing holds pure subscription math (no database).
// Data model ported from phpnuxbill; month arithmetic intentionally clamps instead of overflowing.
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

// defaultBillingDay is used when there is no attribute/plan value.
const defaultBillingDay = 20

type Options struct {
	// Extend is true when `from` is the current expiry of an active subscription of the same
	// plan (config extend_expiry == yes); false when `from` is "now" (new activation or plan change).
	Extend bool
	// BillingDay is the day of month for Period (customer "Expired Date" attribute, else the
	// postpaid plan's expired_date, else 20). <=0 means 20. The caller resolves precedence.
	BillingDay int
}

// daysIn is the number of days in the month starting at first.
func daysIn(first time.Time) int {
	return first.AddDate(0, 1, -1).Day()
}

// addMonths adds n months and clamps the day to the target month's length
// (Jan 31 + 1 month = Feb 28/29), keeping the time of day.
func addMonths(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(n), 1, 0, 0, 0, 0, t.Location())
	return time.Date(first.Year(), first.Month(), min(t.Day(), daysIn(first)),
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// NewExpiry returns when a subscription starting (or extended) at from expires.
// Month arithmetic clamps to the last day of the target month. This deliberately differs
// from PHP strtotime, which overflows into the next month.
func NewExpiry(from time.Time, value int, unit Unit, opts Options) time.Time {
	switch unit {
	case Mins:
		return from.Add(time.Duration(value) * time.Minute) // PHP :190, :219
	case Hrs:
		return from.Add(time.Duration(value) * time.Hour) // PHP :186, :214
	case Days:
		return from.AddDate(0, 0, value) // PHP :182, :210 (keeps time of day)
	case Months:
		return addMonths(from, value) // keeps time of day
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

// clampDay is y-m-day at midnight with day clamped to the month's length (m may be out of range).
func clampDay(y int, m time.Month, day int, loc *time.Location) time.Time {
	first := time.Date(y, m, 1, 0, 0, 0, 0, loc)
	return time.Date(first.Year(), first.Month(), min(day, daysIn(first)), 0, 0, 0, 0, loc)
}

// extendPeriod computes the target month (exp month + value) first, then sets the billing day
// clamped to that month's length, time 23:59:00. PHP overflowed the month first, which skipped
// a month for billing day 29-31; that is a bug and is not kept.
func extendPeriod(exp time.Time, value, day int) time.Time {
	d := clampDay(exp.Year(), exp.Month()+time.Month(value), day, exp.Location())
	return time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 0, 0, exp.Location())
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
