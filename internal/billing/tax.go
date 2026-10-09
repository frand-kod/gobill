package billing

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

// WithTax is price plus PHP's tax (plan.php:126, order.php:238): when enable_tax is "yes",
// price * tax_rate / 100, where tax_rate "custom" means the custom_tax_rate setting. PHP keeps
// the float; here the tax is rounded half up to whole rupiah. st is the settings map.
func WithTax(st map[string]string, price int64) int64 {
	if st["enable_tax"] != "yes" || price <= 0 {
		return price
	}
	rate := strings.TrimSpace(st["tax_rate"])
	if rate == "custom" {
		rate = strings.TrimSpace(st["custom_tax_rate"])
	}
	r, err := strconv.ParseFloat(rate, 64)
	if err != nil || r <= 0 || math.IsInf(r, 0) {
		return price
	}
	return price + int64(math.Round(float64(price)*r/100))
}

func settingsMap(ctx context.Context, q *db.Queries) map[string]string {
	m := map[string]string{}
	rows, _ := q.ListSettings(ctx)
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	return m
}

type quoted struct {
	price int64
	bills []billItem
	note  string
}

// quote is the one price formula of a paid recharge (see couponUse for cp):
//
//	base  = plan price; the Invoice attribute for Period plans; the coupon price with a coupon
//	price = base + tax(base) + the customer's bills        (PHP: tax is on the plan price only)
//
// The very first Period activation (never had a subscription for that router and type) and
// Recharge Zero cost 0 and carry no bills. A gateway payment records the amount it charged.
func quote(ctx context.Context, q *db.Queries, customerID int64, plan db.Plan, cp *couponUse) (quoted, error) {
	if cp != nil && cp.mode == modeZero {
		return quoted{}, nil
	}
	period := plan.ValidityUnit == "Period"
	if period {
		if had, err := hadSub(ctx, q, customerID, plan.RouterID, plan.Type); err != nil || !had {
			return quoted{}, err
		}
	}
	at := attrs(ctx, q, customerID)
	bills, add := customerBills(at)
	base := plan.Price
	if inv := parseMoney(at["Invoice"]); period && inv > 0 {
		base = inv
	}
	if cp != nil && cp.mode == modeDiscount {
		base = cp.price
	}
	out := quoted{price: base + add, bills: bills}
	if cp == nil || !cp.noTax {
		out.price = WithTax(settingsMap(ctx, q), base) + add
	}
	if cp != nil && cp.mode == modeTotal {
		out.price = cp.price
	}
	if add != 0 {
		out.note = billsNote(bills) + fmt.Sprintf("%s : %d\n", plan.Name, base)
	}
	return out, nil
}

// OrderPrice is what a gateway order for a plan at base price costs: tax on base, plus the
// customer's bills, the same as quote.
func (s *Service) OrderPrice(ctx context.Context, customerID, base int64) int64 {
	return WithTax(settingsMap(ctx, s.Q), base) + s.BillsTotal(ctx, customerID)
}
