package main

import (
	"errors"
	"path/filepath"
)

// Guarded ACTIVE is deliberately not an ACTIVE status understood by older
// binaries. Neither upgrades nor downgrades may silently ignore selected guards.
func saveLocalSession(dir string, session LocalSession) error {
	if !session.Safety.valid() {
		return errors.New("invalid local safety policy")
	}
	if session.Status == "ACTIVE" && session.Safety.enabled() {
		session.Status = guardedSessionStatus
	}
	return save(filepath.Join(dir, "session.json"), session)
}
