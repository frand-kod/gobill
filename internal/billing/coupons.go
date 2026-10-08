package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

var (
	ErrCouponNotFound = errors.New("coupon not found")
	ErrCouponInactive = errors.New("coupon is not active")
	ErrCouponDate     = errors.New("coupon is not valid for today")
	ErrCouponUsed     = errors.New("coupon usage limit reached")
	ErrCouponMin      = errors.New("the order amount does not meet the minimum requirement for this coupon")
	ErrCouponTooBig   = errors.New("discount value exceeds the plan price")
)

type couponUse struct {
	code  string
	price int64
}

// Discount validates c for an order of price and returns the discounted price (old order.php:
// percent is capped by max_discount when set; a discount >= price is refused, so the result is > 0).
func Discount(c db.Coupon, price int64, today string) (int64, error) {
	switch {
	case c.Status != "active":
		return 0, ErrCouponInactive
	case today < c.StartDate || today > c.EndDate:
		return 0, ErrCouponDate
	case c.MaxUsage > 0 && c.Used >= c.MaxUsage:
		return 0, ErrCouponUsed
	case price < c.MinOrder:
		return 0, ErrCouponMin
	}
	d := c.Value
	if c.Type == "percent" {
		d = (c.Value*price + 50) / 100 // integer rupiah, rounded half up
		if c.MaxDiscount > 0 && d > c.MaxDiscount {
			d = c.MaxDiscount
		}
	}
	if d >= price {
		return 0, ErrCouponTooBig
	}
	return price - d, nil
}

// RechargeWithBalanceCoupon is RechargeWithBalance with a coupon: the discounted price is debited
// and recorded (note "Coupon CODE"), and usage is claimed in the same transaction.
// Old PHP offers coupons on the portal order only, never on admin recharge or balance top-ups.
func (s *Service) RechargeWithBalanceCoupon(ctx context.Context, customerID, planID int64, code string) error {
	var p *pending
	err := s.tx(ctx, func(q *db.Queries) error {
		if n, err := q.LockCouponByCode(ctx, code); err != nil {
			return err
		} else if n == 0 {
			return ErrCouponNotFound
		}
		plan, err := q.GetPlan(ctx, planID)
		if err != nil {
			return err
		}
		if plan.Type == "Balance" {
			return errors.New("coupon not available for balance")
		}
		c, err := q.GetCouponByCode(ctx, code)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCouponNotFound
		} else if err != nil {
			return err
		}
		price, err := Discount(c, plan.Price, s.now().Format("2006-01-02"))
		if err != nil {
			return err
		}
		if n, err := q.UseCoupon(ctx, c.ID); err != nil {
			return err
		} else if n == 0 {
			return ErrCouponUsed
		}
		cust, err := q.GetCustomer(ctx, customerID)
		if err != nil {
			return err
		}
		if cust.Balance < price {
			return ErrInsufficientBalance
		}
		if _, err := q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: -price, ID: customerID}); err != nil {
			return fmt.Errorf("debit balance: %w", err)
		}
		p, err = s.recharge(ctx, q, customerID, planID, "Customer - Balance", 0, &couponUse{c.Code, price})
		return err
	})
	if err != nil {
		return err
	}
	s.apply(ctx, p)
	return nil
}
