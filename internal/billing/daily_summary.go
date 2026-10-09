package billing

// Daily summary text and delivery.

import (
	"log/slog"

	"context"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"strings"
	"time"
)

// DailySummaryText builds the operator's morning message (Indonesian). Income uses
// SumTransactionsBetween, the same query as the dashboard, so the exclusions stay in one place.
func (s *Service) DailySummaryText(ctx context.Context, now time.Time) (string, error) {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	yday := today.AddDate(0, 0, -1)
	income, err := s.Q.SumTransactionsBetween(ctx, db.SumTransactionsBetweenParams{CreatedAt: yday.Unix(), CreatedAt_2: today.Unix()})
	if err != nil {
		return "", err
	}
	newCust, err := s.Q.CountCustomersBetween(ctx, db.CountCustomersBetweenParams{CreatedAt: yday.Unix(), CreatedAt_2: today.Unix()})
	if err != nil {
		return "", err
	}
	expired, err := s.Q.CountExpiredBetween(ctx, db.CountExpiredBetweenParams{ExpiresAt: yday.Unix(), ExpiresAt_2: today.Unix()})
	if err != nil {
		return "", err
	}
	due, err := s.Q.ListActiveExpiringBetween(ctx, db.ListActiveExpiringBetweenParams{FromTs: today.Unix(), ToTs: today.AddDate(0, 0, 1).Unix()})
	if err != nil {
		return "", err
	}
	var names []string
	for i, sub := range due {
		if i == 10 {
			break
		}
		if c, err := s.Q.GetCustomer(ctx, sub.CustomerID); err == nil {
			names = append(names, c.Username)
		}
	}
	off, err := s.Q.ListOfflineRouters(ctx)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Ringkasan harian %s\n", today.Format("2006-01-02"))
	fmt.Fprintf(&b, "Pemasukan kemarin: %s\n", notify.Money(income))
	fmt.Fprintf(&b, "Pelanggan baru kemarin: %d\n", newCust)
	fmt.Fprintf(&b, "Langganan habis hari ini: %d", len(due))
	if len(names) > 0 {
		b.WriteString(" (" + strings.Join(names, ", "))
		if len(due) > len(names) {
			fmt.Fprintf(&b, " dan %d lainnya", len(due)-len(names))
		}
		b.WriteString(")")
	}
	fmt.Fprintf(&b, "\nLangganan habis kemarin: %d\n", expired)
	if len(off) == 0 {
		b.WriteString("Router offline: tidak ada")
	} else {
		rn := make([]string, len(off))
		for i, r := range off {
			rn[i] = r.Name
		}
		b.WriteString("Router offline: " + strings.Join(rn, ", "))
	}
	if u := strings.TrimRight(setting(ctx, s.Q, "app_url"), "/"); u != "" {
		b.WriteString("\nDashboard: " + u + "/admin")
	}
	return b.String(), nil
}

// ErrSummaryNotConfigured: no channel with a recipient is set up.
var ErrSummaryNotConfigured = errors.New("daily summary: no channel configured")

// dailySummaryTargets returns what is configured: telegram and/or wa (recipient phone).
func dailySummaryTargets(st map[string]string) (tg bool, wa string) {
	ch := st["daily_summary_channel"]
	if ch == "telegram" || ch == "both" {
		tg = st["telegram_bot"] != "" && st["telegram_target_id"] != ""
	}
	if (ch == "wa" || ch == "both") && notify.WAConfigured(st) {
		wa = st["daily_summary_wa_to"]
	}
	return
}

// SendDailySummary sends the summary now through the configured channel(s) and returns the
// channels that worked, e.g. ["Telegram", "WhatsApp"]. Errors are already redacted by notify.
func (s *Service) SendDailySummary(ctx context.Context) ([]string, error) {
	n := s.notifier()
	if n == nil {
		return nil, errors.New("notifier not ready")
	}
	tg, wa := dailySummaryTargets(n.Settings)
	if !tg && wa == "" {
		return nil, ErrSummaryNotConfigured
	}
	text, err := s.DailySummaryText(ctx, s.now())
	if err != nil {
		return nil, err
	}
	var sent []string
	var errs []error
	if tg {
		if err := n.Telegram(ctx, text); err != nil {
			errs = append(errs, fmt.Errorf("Telegram: %w", err))
		} else {
			sent = append(sent, "Telegram")
		}
	}
	if wa != "" {
		if err := n.WhatsApp(ctx, wa, text); err != nil {
			errs = append(errs, fmt.Errorf("WhatsApp: %w", err))
		} else {
			sent = append(sent, "WhatsApp")
		}
	}
	return sent, errors.Join(errs...)
}

// DailySummaryJob is a job.Run body (every minute). It fires once per local day at or after
// daily_summary_time (default 07:00); daily_summary_last (local date) survives restarts.
func (s *Service) DailySummaryJob(trusted func() bool) func(context.Context) error {
	return func(ctx context.Context) error {
		st := settingsMap(ctx, s.Q)
		if st["daily_summary_enabled"] != "yes" {
			return nil
		}
		now := s.now()
		hh, mm := 7, 0
		if t, err := time.Parse("15:04", st["daily_summary_time"]); err == nil {
			hh, mm = t.Hour(), t.Minute()
		}
		today := now.Format("2006-01-02")
		if now.Hour()*60+now.Minute() < hh*60+mm || st["daily_summary_last"] == today {
			return nil
		}
		if tg, wa := dailySummaryTargets(st); !tg && wa == "" || s.notifier() == nil {
			return nil
		}
		if !trusted() {
			return nil // retried next tick
		}
		if err := s.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "daily_summary_last", Value: today}); err != nil {
			return err
		}
		if _, err := s.SendDailySummary(ctx); err != nil {
			slog.Error("daily summary", "err", err) // never fails the job; not retried today
		}
		return nil
	}
}
