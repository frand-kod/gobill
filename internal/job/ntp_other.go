//go:build !linux

package job

// ntpSynced assumes synced where adjtimex is unavailable.
func ntpSynced() bool { return true }
