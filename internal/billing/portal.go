package billing

// Customer portal rules and self-service helpers.

import (
	"errors"
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
