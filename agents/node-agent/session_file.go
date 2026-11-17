package main

import (
	"encoding/json"
	"path/filepath"
)

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
