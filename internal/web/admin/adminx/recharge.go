package adminx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
)

// TitleWords is PHP ucwords: the first letter of every word upper-case.
func TitleWords(s string) string {
	b := []byte(s)
	for i := range b {
		if (i == 0 || b[i-1] == ' ') && b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}

// ChargePlan activates plan for c as the Recharge page does: method is Zero, Balance or a payment
// method, label its display name. devFailed reports that the money is saved and only the router
// missed the change (see RechargeFailMsg for err).
func (s *Base) ChargePlan(r *http.Request, c db.Customer, plan db.Plan, method, label string) (devFailed bool, err error) {
	if s.Billing == nil {
		return false, errors.New("billing service not configured")
	}
	admin := AdminFrom(r)
	// PHP records "<method> - <admin name>" (Recharge Zero for free ones); balance payments keep
	// "Customer - Balance" so the dashboard can leave them out (S1).
	rec := TitleWords(label) + " - " + admin.Fullname
	ctx, failed := billing.TrackDeviceFailure(r.Context())
	switch method {
	case MethodZero:
		err = s.Billing.RechargeZero(ctx, c.ID, plan.ID, "Recharge Zero - "+admin.Fullname, admin.ID)
	case "Balance":
		err = s.Billing.RechargeWithBalance(ctx, c.ID, plan.ID, admin.ID)
	default:
		err = s.Billing.Recharge(ctx, c.ID, plan.ID, rec, admin.ID)
	}
	return *failed, err
}

// RechargeFailMsg is the message for a recharge that charged nothing, the texts the Recharge page shows.
func RechargeFailMsg(err error, c db.Customer, plan db.Plan) string {
	switch {
	case errors.Is(err, billing.ErrInsufficientBalance):
		return MsgNoBalance
	case errors.Is(err, billing.ErrInactive):
		return "account is not active"
	}
	slog.Error("recharge", "customer", c.Username, "plan", plan.Name, "err", err)
	return MsgRechargeFail
}
