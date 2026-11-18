package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"
)

// Consent is reserved before returning to the dashboard. Its original clocks
// include waiting; every transition reuses the existing generation fence.
func (m *SessionManager) armIdle(parent context.Context, b StartSharing, generation uint64, boot string, up float64, expiry time.Time) error {
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	if m.generation != generation || m.session.Status != "STARTING" || ctx.Err() != nil {
		m.mu.Unlock()
		cancel()
		m.abortStart(generation, errors.New("sharing was stopped while starting"))
		return errors.New("sharing was stopped while starting")
	}
	m.startCancel = cancel
	m.LastError = ""
	m.session.WaitingIdle = true
	m.session.Safety = b.Safety
	err := withSessionLock(m.dir, func() error {
		data, err := readSecret(filepath.Join(m.dir, "session.json"))
		if err != nil {
			return err
		}
		var disk LocalSession
		if json.Unmarshal(data, &disk) != nil || disk.ID != m.session.ID || disk.Status != "STARTING" {
			return errors.New("sharing was stopped while starting")
		}
		return saveLocalSession(m.dir, m.session)
	})
	m.mu.Unlock()
	if err != nil {
		cancel()
		m.abortStart(generation, err)
		return err
	}
	go func() {
		defer cancel()
		err := m.awaitIdle(ctx, b, generation, boot, up, expiry)
		m.abortStart(generation, err)
	}()
	return nil
}

func (m *SessionManager) awaitIdle(ctx context.Context, b StartSharing, generation uint64, boot string, up float64, expiry time.Time) error {
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		m.checkDiskStop()
		m.mu.Lock()
		current := m.generation == generation && m.session.Status == "STARTING"
		m.mu.Unlock()
		if !current {
			return errors.New("idle wait canceled")
		}
		if _, err := preparedRemaining(boot, up, b.Duration, expiry); err != nil {
			return err
		}
		c, cancel := context.WithTimeout(ctx, time.Second)
		sample := m.health().observe(c, true)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		other := b.Safety
		other.IdleSeconds = 0
		if err := other.evaluate(sample); err != nil {
			return err
		}
		if sample.IdleSeconds == nil || *sample.IdleSeconds > 86400 {
			return errors.New("Idle sensor unavailable. Start sharing again explicitly when ready.")
		}
		if *sample.IdleSeconds >= uint64(b.Safety.IdleSeconds) {
			network, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return m.completeStart(network, b, generation, boot, up, expiry)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Both clocks bind the original click, including time spent waiting/suspended.
func preparedRemaining(boot string, up float64, duration int, expiry time.Time) (time.Duration, error) {
	currentBoot, currentUp, err := bootClock()
	if err != nil || currentBoot != boot {
		return 0, errors.New("local consent clock unavailable")
	}
	remaining := time.Until(expiry)
	bootRemaining := time.Duration((up + float64(duration) - currentUp) * float64(time.Second))
	if bootRemaining < remaining {
		remaining = bootRemaining
	}
	if remaining <= 0 {
		return 0, errors.New("sharing expired while starting")
	}
	return remaining, nil
}
