package billing

// Expiry reminders sent to customers.

import (
	"context"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"log/slog"
	"strconv"
	"time"
)

// ReminderJob ports cron_reminder.php: from setting reminder_hour (default 7, app zone) until
// midnight it sends the H-1/3/7 reminders, re-checking every reminderEvery. Each reminder is
// claimed in reminder_sent just before it goes out and released when the send fails, so a dead
// gateway or a restart only delays it to the next pass instead of losing it.
func (s *Service) ReminderJob(trusted func() bool) func(context.Context) error {
	var last time.Time
	return func(ctx context.Context) error {
		n := s.notifier()
		if n == nil {
			return nil
		}
		now := s.now()
		hour := 7
		if h, err := strconv.Atoi(setting(ctx, s.Q, "reminder_hour")); err == nil && h >= 0 && h < 24 {
			hour = h
		}
		if now.Hour() < hour || now.Sub(last) < reminderEvery || !trusted() {
			return nil
		}
		last = now
		if err := s.Q.PruneReminders(ctx, now.AddDate(0, 0, -60).Unix()); err != nil {
			slog.Error("reminder: prune", "err", err)
		}
		return s.sendReminders(ctx, n, now)
	}
}

// reminderEvery is how often the reminder job looks for unsent reminders.
const reminderEvery = 15 * time.Minute

func (s *Service) sendReminders(ctx context.Context, n *notify.Notifier, now time.Time) error {
	y, m, d := now.Date()
	day0 := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	subs, err := s.Q.ListActiveExpiringBetween(ctx, db.ListActiveExpiringBetweenParams{
		FromTs: day0.AddDate(0, 0, 1).Unix(), ToTs: day0.AddDate(0, 0, 8).Unix()})
	if err != nil {
		return err
	}
	autoRenew := setting(ctx, s.Q, "enable_balance") != "no"
	var sends []func(context.Context) (bool, error)
	for _, sub := range subs {
		e := time.Unix(sub.ExpiresAt, 0).In(now.Location())
		eDay := time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, now.Location())
		days := int(eDay.Sub(day0).Round(24*time.Hour) / (24 * time.Hour)) // Round: DST days are 23/25h
		if days != 1 && days != 3 && days != 7 {
			continue
		}
		c, err := s.Q.GetCustomer(ctx, sub.CustomerID)
		if err != nil {
			slog.Error("reminder: customer", "subscription", sub.ID, "err", err)
			continue
		}
		p, err := s.Q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			slog.Error("reminder: plan", "subscription", sub.ID, "err", err)
			continue
		}
		// a customer whose balance will renew the plan is not told it ends, like the expired notice
		if renewable(p, c, autoRenew) {
			if op, err := s.OrderPrice(ctx, c.ID, p.Price); err == nil && c.Balance >= op {
				continue
			}
		}
		price := p.Price
		at, err := attrs(ctx, s.Q, c.ID)
		if err != nil {
			slog.Error("reminder: attrs", "subscription", sub.ID, "err", err)
			continue
		}
		if inv := parseMoney(at["Invoice"]); p.ValidityUnit == "Period" && inv > 0 {
			price = inv // postpaid: the invoice amount, as cron_reminder.php
		}
		v := s.packageVars(ctx, c.ID, price)
		v["expired_date"] = e.Format("2006-01-02 15:04:05")
		key := db.ClaimReminderParams{SubscriptionID: sub.ID, ExpiresAt: sub.ExpiresAt, Days: int64(days)}
		sends = append(sends, func(ctx context.Context) (bool, error) {
			if k, err := s.Q.ClaimReminder(ctx, key); err != nil || k == 0 {
				return false, err // already sent this period
			}
			err := n.Reminder(ctx, c, days, p.Name, v)
			if err != nil { // free it so the next pass retries
				if e := s.Q.ReleaseReminder(context.Background(), db.ReleaseReminderParams(key)); e != nil {
					slog.Error("release reminder", "subscription", key.SubscriptionID, "err", e)
				}
			}
			return true, err
		})
	}
	// one goroutine, paced by message_delay like bulk messages
	delay := notify.MessageDelay(n.Settings)
	go func() {
		sent := false
		for _, send := range sends {
			if sent {
				time.Sleep(delay) // only between real sends: claimed ones cost nothing
			}
			ctx, cancel := context.WithTimeout(context.Background(), n.Timeout)
			var err error
			sent, err = send(ctx)
			if err != nil {
				slog.Error("notify failed", "kind", "reminder", "err", err)
			}
			cancel()
		}
	}()
	return nil
}
