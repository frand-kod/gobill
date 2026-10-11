package billing

// Customer portal rules and self-service helpers.

import (
	"errors"

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
	ErrAdminOnlyPlan    = errors.New("Paket ini hanya bisa diberikan admin")
)

// SellablePlan reports whether a customer may take p (buy, friend, extend, auto-renew): an
// admin-audience plan or a plan priced 0 is only given by an admin. Tax and bills only add to the
// price; a coupon that makes the payable amount 0 is allowed (it discounts a plan that is sellable).
func SellablePlan(p db.Plan) bool { return p.Audience == "public" && p.Price > 0 }

func settingOn(v string) bool { return v == "yes" || v == "1" }
