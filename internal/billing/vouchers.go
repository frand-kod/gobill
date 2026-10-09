package billing

// Voucher redemption.

import (
	"database/sql"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/db"
)

// RedeemVoucher claims the voucher once, then recharges its plan (method "Voucher - CODE").
func (s *Service) RedeemVoucher(ctx context.Context, code string, customerID int64) error {
	var p *pending
	err := s.tx(ctx, func(q *db.Queries) error {
		v, err := q.GetVoucherByCode(ctx, code)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrVoucherInvalid
		} else if err != nil {
			return err
		}
		n, err := q.UseVoucher(ctx, db.UseVoucherParams{UsedBy: sql.NullInt64{Int64: customerID, Valid: true}, ID: v.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrVoucherInvalid
		}
		p, err = s.recharge(ctx, q, customerID, v.PlanID, "Voucher - "+code, 0, &couponUse{noTax: true})
		return err // a failed recharge rolls the claim back too
	})
	if err != nil {
		return err
	}
	s.apply(ctx, p)
	return nil
}
