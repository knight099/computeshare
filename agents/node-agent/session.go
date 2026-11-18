package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type LocalSession struct {
	WaitingIdle bool           `json:"waiting_idle,omitempty"`
	ID          string         `json:"id"`
	Status      string         `json:"status"`
	BootID      string         `json:"boot_id"`
	ExpiresBoot float64        `json:"expires_boot"`
	ExpiresAt   time.Time      `json:"expires_at"`
	Policy      ResourcePolicy `json:"policy"`
	Safety      SafetyPolicy   `json:"safety,omitempty"`
}

func (s LocalSession) live(boot string, uptime float64, now time.Time) bool {
	return s.Status == "ACTIVE" && s.BootID == boot && uptime < s.ExpiresBoot && now.Before(s.ExpiresAt)
}

type SessionManager struct {
	safetyOnce     sync.Once
	sensors        *safetyMonitor
	safetySource   func(context.Context) hostObservation
	backend        executionBackend // immutable after construction; no external override
	mu             sync.Mutex
	session        LocalSession
	startCancel    context.CancelFunc
	cancel         context.CancelFunc
	dir            string
	state          State
	root           string
	generation     uint64
	Connected      bool
	CurrentJob     string
	CurrentAttempt string
	LastJob        string
	JobStatus      string
	LastResult     WorkResult
	LastError      string
}

func newSessionManager(dir string, state State) (*SessionManager, error) {
	m := &SessionManager{dir: dir, state: state, root: os.Getenv("OPENCOMPUTE_CGROUP_ROOT")}
	b, e := readSecret(filepath.Join(dir, "session.json"))
	if e == nil {
		if e = json.Unmarshal(b, &m.session); e != nil {
			return nil, errors.New("invalid local availability state")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if !m.session.Safety.valid() {
		return nil, errors.New("invalid saved safety policy")
	}
	if strings.HasPrefix(m.session.Status, "GUARDED_ACTIVE_") {
		if m.session.Status != guardedSessionStatus || !m.session.Safety.enabled() {
			return nil, errors.New("unsupported guarded session format")
		}
		m.stopLocal("STOPPED")
		m.LastError = "Guarded sharing does not resume after agent restart."
	}
	if m.session.Status == "ACTIVE" && m.session.Safety.enabled() {
		return nil, errors.New("unguarded saved safety policy")
	}
	if m.session.Status == "STARTING" {
		m.stopLocal("STOPPED")
	}
	return m, nil
}
func (m *SessionManager) stopLocked(status string) {
	m.generation++
	if m.startCancel != nil {
		m.startCancel()
		m.startCancel = nil
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.CurrentJob != "" {
		m.JobStatus = "CANCELED"
	}
	if m.session.ID != "" {
		m.session.Status = status
	}
}
func (m *SessionManager) stopLocal(status string) LocalSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.session
	m.stopLocked(status)
	if m.session.ID != "" {
		_ = withSessionLock(m.dir, func() error { return saveLocalSession(m.dir, m.session) })
	}
	return previous
}
func (m *SessionManager) prepareStart(boot string, up float64, duration int, p ResourcePolicy) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := withSessionLock(m.dir, func() error {
		m.stopLocked("STOPPED")
		m.session = LocalSession{ID: randomID(), Status: "STARTING", BootID: boot, ExpiresBoot: up + float64(duration), ExpiresAt: time.Now().Add(time.Duration(duration) * time.Second), Policy: p}
		return saveLocalSession(m.dir, m.session)
	})
	return m.generation, err
}
func (m *SessionManager) adoptSession(generation uint64, session LocalSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return withSessionLock(m.dir, func() error {
		if m.generation != generation || m.session.Status != "STARTING" {
			return errors.New("sharing was stopped while starting")
		}
		if b, e := readSecret(filepath.Join(m.dir, "session.json")); e == nil {
			var disk LocalSession
			if json.Unmarshal(b, &disk) == nil && disk.ID == m.session.ID && disk.Status == "STOPPED" {
				m.stopLocked("STOPPED")
				return errors.New("sharing was stopped while starting")
			}
		}
		boot, up, err := bootClock()
		if err != nil || !session.live(boot, up, time.Now()) {
			return errors.New("sharing expired while starting")
		}
		m.session = session
		m.startCancel = nil
		if e := saveLocalSession(m.dir, session); e != nil {
			m.stopLocked("STOPPED")
			return errors.New("cannot persist local expiry policy")
		}
		m.LastError = ""
		return nil
	})
}
func (m *SessionManager) checkDiskStop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, e := readSecret(filepath.Join(m.dir, "session.json")); e == nil {
		var disk LocalSession
		if json.Unmarshal(b, &disk) == nil && disk.ID == m.session.ID && disk.Status == "STOPPED" && (m.session.Status == "ACTIVE" || m.session.Status == "STARTING") {
			m.stopLocked("STOPPED")
		}
	}
}
func (m *SessionManager) snapshot() LocalSession {
	m.checkSafety()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session.Status == "ACTIVE" {
		boot, up, e := bootClock()
		if e != nil || !m.session.live(boot, up, time.Now()) {
			m.session.Status = "EXPIRED"
			m.generation++
			if m.cancel != nil {
				m.cancel()
				m.cancel = nil
			}
			_ = saveLocalSession(m.dir, m.session)
		}
	}
	return m.session
}
func (m *SessionManager) watch(ctx context.Context) {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopLocal("STOPPED")
			return
		case <-timer.C:
			m.snapshot()
			m.checkDiskStop()
		}
	}
}

type StartSharing struct {
	WaitForIdle bool           `json:"wait_for_idle,omitempty"`
	Duration    int            `json:"duration_seconds"`
	Policy      ResourcePolicy `json:"policy"`
	Accepted    bool           `json:"accepted"`
	Safety      SafetyPolicy   `json:"safety,omitempty"`
}

func (m *SessionManager) start(ctx context.Context, b StartSharing) error {
	if !b.Accepted || !b.Policy.valid() {
		return errors.New("explicit sharing consent and supported CPU/RAM policy required")
	}
	valid := false
	for _, d := range []int{3600, 7200, 14400, 21600, 28800, 43200} {
		if d == b.Duration {
			valid = true
		}
	}
	if !valid {
		return errors.New("unsupported availability duration")
	}
	if !m.execution().available(m.root) {
		return errors.New("execution disabled: " + m.execution().reason())
	}
	if !b.Safety.valid() || b.WaitForIdle && b.Safety.IdleSeconds == 0 {
		return errors.New("idle waiting requires a supported idle safety selection")
	}
	boot, up, e := bootClock()
	if e != nil {
		return errors.New("local expiry clock unavailable")
	}
	wallExpiry := time.Now().Add(time.Duration(b.Duration) * time.Second)
	generation, e := m.prepareStart(boot, up, b.Duration, b.Policy)
	if e != nil {
		return e
	}
	if b.WaitForIdle {
		return m.armIdle(ctx, b, generation, boot, up, wallExpiry)
	}
	return m.completeStart(ctx, b, generation, boot, up, wallExpiry)
}

func (m *SessionManager) abortStart(generation uint64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation == generation && m.session.Status == "STARTING" {
		m.stopLocked("STOPPED")
		if err != nil {
			m.LastError = err.Error()
		}
		_ = withSessionLock(m.dir, func() error { return saveLocalSession(m.dir, m.session) })
	}
}

func (m *SessionManager) completeStart(ctx context.Context, b StartSharing, generation uint64, boot string, up float64, wallExpiry time.Time) (err error) {
	defer func() { m.abortStart(generation, err) }()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := m.requireSafety(ctx, b.Safety); err != nil {
		return err
	}
	m.mu.Lock()
	stopped := m.generation != generation || m.session.Status != "STARTING"
	m.mu.Unlock()
	if stopped {
		return errors.New("sharing was stopped while starting")
	}
	remaining, e := preparedRemaining(boot, up, b.Duration, wallExpiry)
	if e != nil {
		return e
	}
	if remaining > 5*time.Second {
		remaining = 5 * time.Second
	}
	network, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	ctx = network
	token, e := deviceToken(ctx, m.state)
	if e != nil {
		return e
	}
	var session struct {
		ID      string    `json:"id"`
		Expires time.Time `json:"expires_at"`
	}
	if _, err := preparedRemaining(boot, up, b.Duration, wallExpiry); err != nil {
		return err
	}
	payload := map[string]interface{}{"device_id": m.state.Device, "duration_seconds": b.Duration, "policy": b.Policy, "consent_version": "sharing-v1", "accepted": true}
	if b.WaitForIdle {
		payload["consent_expires_at"] = wallExpiry.UTC()
	}
	if e = request(ctx, m.state.Gateway, "/v1/agent/availability-sessions", "POST", token, payload, &session); e != nil {
		return e
	}
	if session.ID == "" || session.Expires.IsZero() {
		return errors.New("invalid company session response")
	}
	if b.WaitForIdle && session.Expires.After(wallExpiry) {
		return errors.New("company did not enforce original consent deadline")
	}
	if session.Expires.Before(wallExpiry) {
		wallExpiry = session.Expires
	}
	if err := m.requireSafety(ctx, b.Safety); err != nil {
		return err
	}
	return m.adoptSession(generation, LocalSession{ID: session.ID, Status: "ACTIVE", BootID: boot, ExpiresBoot: up + float64(b.Duration), ExpiresAt: wallExpiry, Policy: b.Policy, Safety: b.Safety})
}
func (m *SessionManager) begin(job HashJob, cancel context.CancelFunc) bool {
	s := m.snapshot()
	if s.ID != job.SessionID || s.Status != "ACTIVE" || len(job.AttemptID) != 36 || strings.ContainsAny(job.AttemptID, "/\\.") || job.Timeout < 1 || job.Timeout > 30 || job.LeaseExpires.Before(time.Now()) || job.Workload != "synthetic-hash-v1" {
		cancel()
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CurrentJob != "" || m.session.Status != "ACTIVE" || m.session.ID != job.SessionID {
		cancel()
		return false
	}
	m.CurrentJob = job.JobID
	m.CurrentAttempt = job.AttemptID
	m.JobStatus = "RUNNING"
	m.cancel = cancel
	return true
}
func (m *SessionManager) finished(job HashJob, result WorkResult, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastJob = job.JobID
	m.CurrentJob = ""
	m.CurrentAttempt = ""
	m.LastResult = result
	if status == "FAILED" && m.session.Status != "ACTIVE" {
		status = "CANCELED"
	}
	m.JobStatus = status
	m.cancel = nil
}
func (m *SessionManager) disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Connected = false
	if m.cancel != nil {
		m.cancel()
	}
}
