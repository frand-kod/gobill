package billing

// Service type, reload, transactions and shared helpers.

import (
	"database/sql"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/notify"
	"sync"
	"time"
)

func (s *Service) logAdmin(ctx context.Context, q *db.Queries, adminID int64, action, desc string) error {
	return q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "admin", ActorID: adminID, Action: action, Description: desc})
}

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrVoucherInvalid      = errors.New("voucher not valid or already used")
)

// Service ports phpnuxbill Package::rechargeUser and cron.php expiry.
type Service struct {
	DB  *sql.DB
	Q   *db.Queries
	Key []byte         // decrypts routers.password_enc and customers.secret_enc
	Loc *time.Location // zone used for day/month/billing-day math
	Now func() time.Time
	// N sends notifications; nil means none. Swap at runtime with Reload.
	N  *notify.Notifier
	mu sync.RWMutex
	// DeviceFor builds the driver for a plan; nil means the default (by plans.device).
	DeviceFor func(plan db.Plan, router db.Router) (device.Device, error)
	// RouterFor builds the connection for a router (ping, pool sync); nil means from the stored row.
	RouterFor func(router db.Router) (device.Router, error)
}

// Reload swaps the notifier and time zone (after settings change).
func (s *Service) Reload(n *notify.Notifier, loc *time.Location) {
	s.mu.Lock()
	s.N, s.Loc = n, loc
	s.mu.Unlock()
}

// Location returns the billing zone, or Asia/Jakarta (WIB) when none is set.
func (s *Service) Location() *time.Location {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.Loc != nil {
		return s.Loc
	}
	if l, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return l
	}
	return time.FixedZone("WIB", 7*3600)
}

func (s *Service) notifier() *notify.Notifier {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.N
}

func (s *Service) now() time.Time {
	t := time.Now()
	if s.Now != nil {
		t = s.Now()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.Loc != nil {
		t = t.In(s.Loc)
	}
	return t
}

func (s *Service) tx(ctx context.Context, fn func(*db.Queries) error) error {
	t, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer t.Rollback()
	if err := fn(s.Q.WithTx(t)); err != nil {
		return err
	}
	return t.Commit()
}

func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id > 0} }

func setting(ctx context.Context, q *db.Queries, key string) string {
	rows, err := q.ListSettings(ctx)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.Key == key {
			return r.Value
		}
	}
	return ""
}

// telegram sends an admin alert in the background; no-op without telegram_bot, never blocks or fails billing.
func (s *Service) telegram(text string) {
	if n := s.notifier(); n != nil {
		n.Go("telegram", func(ctx context.Context) error { return n.Telegram(ctx, text) })
	}
}
