//go:build linux

package job

import "golang.org/x/sys/unix"

// DiskFreeMB returns the free space, in MiB, available to unprivileged writers on dir's filesystem.
func DiskFreeMB(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize) / (1 << 20), nil
}
