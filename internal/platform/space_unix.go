//go:build !windows

package platform

import (
	"path/filepath"
	"syscall"
)

func freeSpace(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func parentDir(path string) string { return filepath.Dir(path) }
