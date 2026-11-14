package main

import (
	"encoding/json"
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
func stopFromCLI(dir string) error {
	return withSessionLock(dir, func() error {
		b, e := readSecret(filepath.Join(dir, "session.json"))
		if e != nil {
			return e
		}
		var s LocalSession
		if e = json.Unmarshal(b, &s); e != nil {
			return e
		}
		s.Status = "STOPPED"
		return save(filepath.Join(dir, "session.json"), s)
	})
}
