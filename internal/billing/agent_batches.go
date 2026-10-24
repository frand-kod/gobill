package billing

// Agent consignment: a batch of vouchers handed to an Agent who pays later. Every change to the debt
// is one append-only agent_ledger row written in the same transaction as the rest of the change.
// The web layer writes the activity log (it knows the client IP), as for vouchers.

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

var (
	ErrBatchInvalid  = errors.New("invalid agent batch")
	ErrBatchNotFound = errors.New("agent batch not found")
	ErrOverpay       = errors.New("payment is more than the debt")
	ErrReturnLimit   = errors.New("returns are more than the paid vouchers of the batch")
	ErrNotReturnable = errors.New("code is not an unused code of this batch")
)

const (
	maxBatchPrice = 100_000_000 // rupiah per voucher; keeps paid x price far from overflow
	maxBatchCodes = 500
	codeTries     = 8 // redraws of a clashing code
)

// BatchParams describes a new batch. NewCode draws one voucher code (the caller owns format and prefix).
type BatchParams struct {
	AgentID, PlanID, AdminID int64
	Paid, Bonus, Price       int64
	Note                     string
	NewCode                  func() (string, error)
}

// CreateAgentBatch makes the batch, its Paid+Bonus voucher codes and the debt (Paid x Price) at once.
// Any failure leaves nothing behind.
func (s *Service) CreateAgentBatch(ctx context.Context, p BatchParams) (int64, error) {
	if p.Paid < 0 || p.Bonus < 0 || p.Paid+p.Bonus < 1 || p.Paid+p.Bonus > maxBatchCodes || p.Price < 0 || p.Price > maxBatchPrice || p.NewCode == nil {
		return 0, ErrBatchInvalid
	}
	var id int64
	err := s.tx(ctx, func(q *db.Queries) error {
		agent, err := q.GetAdmin(ctx, p.AgentID)
		if err != nil || agent.Role != "Agent" || agent.Status != "Active" {
			return ErrBatchInvalid
		}
		if _, err := q.GetPlan(ctx, p.PlanID); err != nil {
			return ErrBatchInvalid
		}
		b, err := q.CreateAgentBatch(ctx, db.CreateAgentBatchParams{AgentID: p.AgentID, PlanID: p.PlanID, PaidCount: p.Paid,
			BonusCount: p.Bonus, UnitPrice: p.Price, Note: p.Note, CreatedBy: nullID(p.AdminID)})
		if err != nil {
			return err
		}
		id = b.ID
		for n := int64(0); n < p.Paid+p.Bonus; n++ {
			if err := batchCode(ctx, q, b, p); err != nil {
				return err
			}
		}
		return q.AddAgentLedger(ctx, db.AddAgentLedgerParams{BatchID: b.ID, Kind: "debt", Amount: p.Paid * p.Price,
			Note: "batch", AdminID: nullID(p.AdminID)})
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// batchCode inserts one voucher into the batch, redrawing the code on a UNIQUE clash.
func batchCode(ctx context.Context, q *db.Queries, b db.AgentBatch, p BatchParams) error {
	for try := 0; try < codeTries; try++ {
		code, err := p.NewCode()
		if err != nil {
			return err
		}
		v, err := q.CreateVoucher(ctx, db.CreateVoucherParams{Code: code, PlanID: b.PlanID, GeneratedBy: nullID(p.AdminID)})
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				continue
			}
			return err
		}
		return q.AddBatchVoucher(ctx, db.AddBatchVoucherParams{VoucherID: v.ID, BatchID: b.ID})
	}
	return errors.New("could not find a free voucher code")
}

// RecordAgentPayment lowers the debt of a batch by amount. A payment above the remaining debt is refused.
func (s *Service) RecordAgentPayment(ctx context.Context, batchID, amount int64, note string, adminID int64) error {
	if amount <= 0 {
		return ErrBatchInvalid
	}
	return s.tx(ctx, func(q *db.Queries) error {
		if _, err := q.GetAgentBatch(ctx, batchID); errors.Is(err, sql.ErrNoRows) {
			return ErrBatchNotFound
		} else if err != nil {
			return err
		}
		debt, err := q.AgentBatchDebt(ctx, batchID)
		if err != nil {
			return err
		}
		if amount > debt {
			return ErrOverpay
		}
		return q.AddAgentLedger(ctx, db.AddAgentLedgerParams{BatchID: batchID, Kind: "payment", Amount: amount, Note: note, AdminID: nullID(adminID)})
	})
}

// ReturnAgentVouchers cancels unused codes of a batch; each lowers the debt by the unit price. All or none:
// a code that is used, returned already or foreign to the batch, or more returns than paid vouchers, fails the lot.
func (s *Service) ReturnAgentVouchers(ctx context.Context, batchID int64, voucherIDs []int64, adminID int64) error {
	if len(voucherIDs) == 0 {
		return ErrBatchInvalid
	}
	return s.tx(ctx, func(q *db.Queries) error {
		b, err := q.GetAgentBatch(ctx, batchID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBatchNotFound
		} else if err != nil {
			return err
		}
		done, err := q.CountAgentReturns(ctx, batchID)
		if err != nil {
			return err
		}
		if done+int64(len(voucherIDs)) > b.PaidCount {
			return ErrReturnLimit
		}
		seen := map[int64]bool{}
		for _, id := range voucherIDs {
			if seen[id] {
				return ErrNotReturnable
			}
			seen[id] = true
			if _, err := q.ReturnableBatchVoucher(ctx, db.ReturnableBatchVoucherParams{BatchID: batchID, ID: id}); errors.Is(err, sql.ErrNoRows) {
				return ErrNotReturnable
			} else if err != nil {
				return err
			}
			if err := q.AddAgentLedger(ctx, db.AddAgentLedgerParams{BatchID: batchID, Kind: "return", Amount: b.UnitPrice,
				VoucherID: nullID(id), Note: "return", AdminID: nullID(adminID)}); err != nil {
				return err
			}
		}
		return nil
	})
}
