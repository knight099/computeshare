package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

const guardedSessionStatus = "GUARDED_ACTIVE_V1"

type SafetyPolicy struct {
	ACOnly       bool `json:"ac_only,omitempty"`
	IdleSeconds  int  `json:"idle_seconds,omitempty"`
	MaxCPUMilliC int  `json:"max_cpu_millic,omitempty"`
}

func (p SafetyPolicy) enabled() bool { return p.ACOnly || p.IdleSeconds != 0 || p.MaxCPUMilliC != 0 }
func (p SafetyPolicy) valid() bool {
	return (p.IdleSeconds == 0 || p.IdleSeconds >= 60 && p.IdleSeconds <= 3600) && (p.MaxCPUMilliC == 0 || p.MaxCPUMilliC >= 50000 && p.MaxCPUMilliC <= 95000)
}
func (p SafetyPolicy) evaluate(s hostObservation) error {
	if !p.valid() {
		return errors.New("unsupported local safety limits")
	}
	if p.ACOnly && (s.AC == nil || !*s.AC) {
		return errors.New("Sharing requires a known AC power connection.")
	}
	if p.IdleSeconds != 0 && (s.IdleSeconds == nil || *s.IdleSeconds < uint64(p.IdleSeconds) || *s.IdleSeconds > 86400) {
		return errors.New("Sharing requires a known idle desktop session.")
	}
	if p.MaxCPUMilliC != 0 && (s.CPUMilliC == nil || *s.CPUMilliC < 0 || *s.CPUMilliC > 125000 || *s.CPUMilliC >= p.MaxCPUMilliC) {
		return errors.New("Sharing requires a known CPU temperature below the selected limit.")
	}
	return nil
}

type hostObservation struct {
	Fresh          bool    `json:"fresh"`
	AC             *bool   `json:"ac_online"`
	BatteryPercent *uint8  `json:"battery_percent"`
	IdleSeconds    *uint64 `json:"idle_seconds"`
	CPUMilliC      *int    `json:"cpu_millic"`
}
type safetyMonitor struct {
	mu        sync.Mutex
	source    func(context.Context) hostObservation
	cached    hostObservation
	collected time.Time
	pending   chan struct{}
}

func newSafetyMonitor(source func(context.Context) hostObservation) *safetyMonitor {
	return &safetyMonitor{source: source}
}

// Use both elapsed clocks: Go monotonic time can exclude suspend, while wall
// time can jump. A backward wall correction invalidates the observation.
func safetyAge(collected, now time.Time) time.Duration {
	monotonic := now.Sub(collected)
	wall := now.Round(0).Sub(collected.Round(0))
	if monotonic < 0 || wall < 0 {
		return 3 * time.Second
	}
	if wall > monotonic {
		return wall
	}
	return monotonic
}
func (m *safetyMonitor) observe(ctx context.Context, wait bool) hostObservation {
	m.mu.Lock()
	if m.collected.IsZero() || safetyAge(m.collected, time.Now()) >= 500*time.Millisecond {
		if m.pending == nil {
			m.pending = make(chan struct{})
			done := m.pending
			go func() {
				started := time.Now()
				c, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				sample := m.source(c)
				if c.Err() != nil {
					sample = hostObservation{}
				}
				m.mu.Lock()
				m.cached = sample
				m.collected = started
				m.pending = nil
				close(done)
				m.mu.Unlock()
			}()
		}
	}
	pending := m.pending
	m.mu.Unlock()
	if wait && pending != nil {
		select {
		case <-pending:
		case <-ctx.Done():
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.collected.IsZero() || safetyAge(m.collected, time.Now()) > 2*time.Second {
		return hostObservation{}
	}
	result := m.cached
	result.Fresh = true
	return result
}
func (m *SessionManager) health() *safetyMonitor {
	m.safetyOnce.Do(func() {
		source := m.safetySource
		if source == nil {
			source = platformHostObservation
		}
		m.sensors = newSafetyMonitor(source)
	})
	return m.sensors
}
func (m *SessionManager) requireSafety(ctx context.Context, p SafetyPolicy) error {
	if !p.valid() {
		return errors.New("unsupported local safety limits")
	}
	if !p.enabled() {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	sample := m.health().observe(c, true)
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.evaluate(sample)
}
func (m *SessionManager) checkSafety() {
	m.mu.Lock()
	session, generation := m.session, m.generation
	m.mu.Unlock()
	if session.Status != "ACTIVE" || !session.Safety.enabled() {
		return
	}
	if err := session.Safety.evaluate(m.health().observe(context.Background(), false)); err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.generation != generation || m.session.ID != session.ID || m.session.Status != "ACTIVE" {
			return
		}
		m.stopLocked("STOPPED")
		m.LastError = err.Error() + " Start sharing again explicitly when ready."
		_ = withSessionLock(m.dir, func() error { return saveLocalSession(m.dir, m.session) })
	}
}
