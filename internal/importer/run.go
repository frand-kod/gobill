package importer

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
)

// Trigger SQL copied verbatim from migrations/0022_agent_batches.sql; wipeAgent recreates them.
const (
	agentLedgerNoUpdate = `CREATE TRIGGER agent_ledger_no_update BEFORE UPDATE OF batch_id, kind, amount, voucher_id, note, created_at ON agent_ledger
BEGIN SELECT RAISE(ABORT, 'agent_ledger is append-only'); END;`
	agentLedgerNoDelete = `CREATE TRIGGER agent_ledger_no_delete BEFORE DELETE ON agent_ledger
BEGIN SELECT RAISE(ABORT, 'agent_ledger is append-only'); END;`
)

// wipeAgent empties the agent tables, which hold RESTRICT foreign keys into vouchers, admins and
// plans. agent_ledger is append-only (its triggers abort DELETE), so they are dropped for this
// transaction only and recreated before returning. A rollback restores them too.
func wipeAgent(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		"DROP TRIGGER agent_ledger_no_update",
		"DROP TRIGGER agent_ledger_no_delete",
		"DELETE FROM agent_ledger",
		"DELETE FROM agent_batch_vouchers",
		"DELETE FROM agent_batches",
		agentLedgerNoUpdate,
		agentLedgerNoDelete,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Run imports everything in one SQLite transaction.
func Run(ctx context.Context, my, lite *sql.DB, o Options) (*Report, error) {
	tx, err := lite.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if o.Force {
		if err := wipeAgent(ctx, tx); err != nil {
			return nil, err
		}
	}
	for _, t := range dataTables {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+t).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		if !o.Force {
			return nil, fmt.Errorf("target is not empty (%s has %d rows); use --force to wipe it", t, n)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return nil, err
		}
	}
	m := &imp{my: my, tx: tx, ctx: ctx, o: o, rep: &Report{Unsupported: slices.Clone(unsupportedFeatures)},
		routers: map[string]int64{}, pools: map[string]int64{}, plans: map[string]int64{},
		customers: map[string]int64{}, admins: map[int64]bool{}, custIDs: map[int64]bool{}}
	for _, step := range []func() error{m.settings, m.notifications, m.admin, m.router, m.bandwidth, m.pool, m.plan,
		m.customer, m.fields, m.subscription, m.transaction, m.voucher, m.logs, m.nas} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	if o.DryRun {
		return m.rep, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// Fresh planner stats for the imported row counts; without them RADIUS auth scans every
	// active subscription. A failure only costs speed: the data is already committed.
	lite.ExecContext(ctx, "ANALYZE")
	return m.rep, nil
}

// Timezone returns the old appconfig timezone, or Asia/Jakarta.
func Timezone(ctx context.Context, my *sql.DB) string {
	var tz string
	if my.QueryRowContext(ctx, "SELECT value FROM tbl_appconfig WHERE setting = 'timezone'").Scan(&tz) != nil || tz == "" {
		return "Asia/Jakarta"
	}
	return tz
}
