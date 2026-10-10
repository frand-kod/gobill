//go:build !linux

package job

import "errors"

// DiskFreeMB is unknown outside linux; callers treat the error as "not measured".
func DiskFreeMB(dir string) (int64, error) { return 0, errors.New("disk free: unsupported OS") }
