package billing

import (
	"context"
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

// charge is the tax-inclusive price of a plan that is paid for (cash, balance); nil when tax
// changes nothing, so recharge records the plain plan price. Balance top-ups are never taxed.
func charge(ctx context.Context, q *db.Queries, plan db.Plan) *couponUse {
	if plan.Type == "Balance" {
		return nil
	}
	if p := WithTax(settingsMap(ctx, q), plan.Price); p != plan.Price {
		return &couponUse{price: p}
	}
	return nil
}
