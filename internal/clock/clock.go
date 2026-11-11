// Package clock is the shared source of the current time and the default zone.
// Stdlib only; tests swap a Func for a fake clock.
package clock

import "time"

// Func returns the current time. A nil Func is the real clock.
type Func func() time.Time

// Now calls f, or time.Now when f is nil.
func (f Func) Now() time.Time {
	if f == nil {
		return time.Now()
	}
	return f()
}

// Jakarta is the default billing zone: Asia/Jakarta, or a fixed WIB (+7) when tzdata is missing.
func Jakarta() *time.Location {
	if l, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return l
	}
	return time.FixedZone("WIB", 7*3600)
}
