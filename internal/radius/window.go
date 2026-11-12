package radius

import "time"

// AppLoc is the app time zone, for plan schedules evaluated outside a Server (CoA). nil = UTC.
var AppLoc func() *time.Location

func appLoc() *time.Location {
	if AppLoc != nil {
		return AppLoc()
	}
	return time.UTC
}
