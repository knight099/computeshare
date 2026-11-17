//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"path/filepath"
)

func withSessionLock(dir string, action func() error) error {
	if e := prepareStateDirectory(dir); e != nil {
		return e
	}
	file, e := openWindowsState(filepath.Join(dir, "session.lock"), windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, windows.OPEN_ALWAYS, false)
	if e != nil {
		return e
	}
	defer file.Close()
	handle := windows.Handle(file.Fd())
	var overlap windows.Overlapped
	if e = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlap); e != nil {
		return e
	}
	defer windows.UnlockFileEx(handle, 0, 1, 0, &overlap)
	return action()
}
