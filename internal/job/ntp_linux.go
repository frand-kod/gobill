//go:build linux

package job

import "golang.org/x/sys/unix"

// ntpSynced is false when the kernel reports STA_UNSYNC (or adjtimex fails).
func ntpSynced() bool {
	var tx unix.Timex
	state, err := unix.Adjtimex(&tx)
	if err != nil {
		return false
	}
	return state != unix.TIME_ERROR && tx.Status&unix.STA_UNSYNC == 0
}
