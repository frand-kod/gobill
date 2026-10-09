package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

var (
	ErrSelfTransfer     = errors.New("Cannot send to yourself")
	ErrTargetNotFound   = errors.New("Username not found")
	ErrBelowMinimum     = errors.New("Minimum Transfer")
	ErrTransferDisabled = errors.New("Failed, balance is not available")
	ErrExtendDisabled   = errors.New("cannot extend")
	ErrExtendAlready    = errors.New("You already extend for this month")
	ErrNotExpired       = errors.New("Plan is not expired")
	ErrPlanNotFound     = errors.New("Plan Not Found or Not Active")
	ErrInactive         = errors.New("account is not active")
)

func settingOn(v string) bool { return v == "yes" || v == "1" }

// TransferBalance moves amount from one customer to another (home.php send=balance) in one
// transaction: debit, credit and one transaction row per side. Notifications run after commit.
func (s *Service) TransferBalance(ctx context.Context, fromID int64, toUsername string, amount int64) error {
	var from, to db.Customer
	err := s.tx(ctx, func(q *db.Queries) error {
		if setting(ctx, q, "enable_balance") == "no" || setting(ctx, q, "allow_balance_transfer") != "yes" {
			return ErrTransferDisabled
		}
		var err error
		if from, err = q.GetCustomer(ctx, fromID); err != nil {
			return err
		}
		if from.Status != "Active" {
			return ErrInactive
		}
		if to, err = q.GetCustomerByUsername(ctx, toUsername); errors.Is(err, sql.ErrNoRows) {
			return ErrTargetNotFound
		} else if err != nil {
			return err
		}
		if to.ID == from.ID {
			return ErrSelfTransfer
		}
		min, _ := strconv.ParseInt(setting(ctx, q, "minimum_transfer"), 10, 64)
		if amount <= 0 || amount < min {
			return ErrBelowMinimum
		}
		if from.Balance < amount {
			return ErrInsufficientBalance
		}
		if from.Balance, err = q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: -amount, ID: from.ID}); err != nil {
			return err
		}
		if to.Balance, err = q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: amount, ID: to.ID}); err != nil {
			return err
		}
		now := s.now().Unix()
		for _, t := range []struct {
			c          db.Customer
			name, peer string
		}{{from, "Send Balance", to.Username}, {to, "Receive Balance", from.Username}} {
			inv, err := nextInvoice(ctx, q)
			if err != nil {
				return err
			}
			if _, err = q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: inv, CustomerID: sql.NullInt64{Int64: t.c.ID, Valid: true},
				Username: t.c.Username, PlanName: t.name, RouterName: "balance", Type: "Balance", Price: amount,
				Method: "Customer - Balance", Note: t.peer, PeriodStart: now, PeriodEnd: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if n := s.notifier(); n != nil {
		bal := strconv.FormatInt(amount, 10)
		n.Go("balance_send", func(c context.Context) error {
			return n.Custom(c, from, "balance_send", "Balance Notification", map[string]string{"name": to.Fullname + " (" + to.Username + ")", "balance": bal, "current_balance": strconv.FormatInt(from.Balance, 10)})
		})
		n.Go("balance_received", func(c context.Context) error {
			return n.Custom(c, to, "balance_received", "Balance Notification", map[string]string{"name": from.Fullname + " (" + from.Username + ")", "balance": bal, "current_balance": strconv.FormatInt(to.Balance, 10)})
		})
		n.Go("telegram", func(c context.Context) error {
			return n.Telegram(c, fmt.Sprintf("#u%s send balance to #u%s \n%d", from.Username, to.Username, amount))
		})
	}
	return nil
}

// ExtendExpired is home.php ?extend=: an expired subscription is switched back on for
// extend_days days from now, once per calendar month per customer. (The old code kept only the
// month number in a cache file; the year is kept here too so it does not block a year later.)
func (s *Service) ExtendExpired(ctx context.Context, customerID, subID int64) (time.Time, error) {
	var p pending
	var until time.Time
	err := s.tx(ctx, func(q *db.Queries) error {
		c, err := q.GetCustomer(ctx, customerID)
		if err != nil {
			return err
		}
		if c.Status != "Active" {
			return ErrInactive
		}
		days, _ := strconv.Atoi(setting(ctx, q, "extend_days"))
		if !settingOn(setting(ctx, q, "extend_expired")) || days <= 0 {
			return ErrExtendDisabled
		}
		sub, err := q.GetSubscription(ctx, subID)
		if err != nil || sub.CustomerID != customerID {
			return ErrPlanNotFound
		}
		now := s.now()
		key, month := fmt.Sprint("extend_last_", customerID), now.Format("2006-01")
		if setting(ctx, q, key) == month {
			return ErrExtendAlready
		}
		if sub.Status == "active" {
			return ErrNotExpired
		}
		plan, err := q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			return err
		}
		until = now.AddDate(0, 0, days)
		if err = q.UpdateSubscription(ctx, db.UpdateSubscriptionParams{PlanID: sub.PlanID, RouterID: sub.RouterID, Type: sub.Type,
			ExpiresAt: until.Unix(), Status: "active", AdminID: sub.AdminID, ID: sub.ID}); err != nil {
			return err
		}
		if err = q.RestartSubscription(ctx, db.RestartSubscriptionParams{StartedAt: now.Unix(), ID: sub.ID}); err != nil { // usage window restarts (R4)
			return err
		}
		// ponytail: one settings row per customer that has extended (extend_last_<id>). Ceiling: the
		// settings table grows with customers; move to a customers column once a migration is allowed.
		if err = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: key, Value: month}); err != nil {
			return err
		}
		if err = q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "customer", ActorID: c.ID, Action: "extend",
			Description: fmt.Sprintf("%s extend for %d days", c.Username, days)}); err != nil {
			return err
		}
		p = pending{cust: c, plan: plan, change: true, expiry: until}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	if err := s.activate(ctx, &p); err != nil {
		slog.Error("device: extend activate failed, sync manually", "customer", p.cust.Username, "err", err)
	}
	if n := s.notifier(); n != nil {
		n.Go("telegram", func(c context.Context) error {
			return n.Telegram(c, fmt.Sprintf("#u%s (%s) #id%d #extend #%s \n%s\nNew Expired: %s", p.cust.Username, p.cust.Fullname, p.cust.ID, p.plan.Type, p.plan.Name, until.Format("2006-01-02 15:04:05")))
		})
	}
	return until, nil
}
