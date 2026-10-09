package billing

import (
	"context"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
)

// ReminderJob ports cron_reminder.php: once a day at setting reminder_hour (default 7, app
// zone) it sends the H-1/3/7 reminders. reminder_last_run (local date) stops a second send.
func (s *Service) ReminderJob(trusted func() bool) func(context.Context) error {
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
		today := now.Format("2006-01-02")
		if now.Hour() != hour || setting(ctx, s.Q, "reminder_last_run") == today {
			return nil
		}
		if !trusted() {
			return nil // retried next tick while still in the hour
		}
		if err := s.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "reminder_last_run", Value: today}); err != nil {
			return err
		}
		return s.sendReminders(ctx, n, now)
	}
}

func (s *Service) sendReminders(ctx context.Context, n *notify.Notifier, now time.Time) error {
	y, m, d := now.Date()
	day0 := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	subs, err := s.Q.ListActiveExpiringBetween(ctx, db.ListActiveExpiringBetweenParams{
		FromTs: day0.AddDate(0, 0, 1).Unix(), ToTs: day0.AddDate(0, 0, 8).Unix()})
	if err != nil {
		return err
	}
	for _, sub := range subs {
		e := time.Unix(sub.ExpiresAt, 0).In(now.Location())
		eDay := time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, now.Location())
		days := int(eDay.Sub(day0).Round(24*time.Hour) / (24 * time.Hour)) // Round: DST days are 23/25h
		if days != 1 && days != 3 && days != 7 {
			continue
		}
		c, err := s.Q.GetCustomer(ctx, sub.CustomerID)
		if err != nil {
			return err
		}
		p, err := s.Q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			return err
		}
		v := map[string]string{"expired_date": e.Format("2006-01-02 15:04:05")}
		n.Go("reminder", func(ctx context.Context) error { return n.Reminder(ctx, c, days, p.Name, v) })
	}
	return nil
}
