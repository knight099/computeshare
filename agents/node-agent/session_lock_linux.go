//go:build linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

func withSessionLock(dir string, action func() error) error {
	f, e := os.OpenFile(filepath.Join(dir, "session.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return action()
}
