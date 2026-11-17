package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LocalSession struct {
	ID          string         `json:"id"`
	Status      string         `json:"status"`
	BootID      string         `json:"boot_id"`
	ExpiresBoot float64        `json:"expires_boot"`
	ExpiresAt   time.Time      `json:"expires_at"`
	Policy      ResourcePolicy `json:"policy"`
}

func (s LocalSession) live(boot string, uptime float64, now time.Time) bool {
	return s.Status == "ACTIVE" && s.BootID == boot && uptime < s.ExpiresBoot && now.Before(s.ExpiresAt)
}
func bootClock() (string, float64, error) {
	id, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return "", 0, e
	}
	data, e := os.ReadFile("/proc/uptime")
	if e != nil {
		return "", 0, e
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return "", 0, errors.New("boot clock unavailable")
	}
	up, e := strconv.ParseFloat(fields[0], 64)
	return strings.TrimSpace(string(id)), up, e
}

type SessionManager struct {
	mu             sync.Mutex
	session        LocalSession
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
	if m.session.Status == "STARTING" {
		m.stopLocal("STOPPED")
	}
	return m, nil
}
func (m *SessionManager) stopLocked(status string) {
	m.generation++
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
func (m *SessionManager) stopLocal(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked(status)
	if m.session.ID != "" {
		_ = withSessionLock(m.dir, func() error { return save(filepath.Join(m.dir, "session.json"), m.session) })
	}
}
func (m *SessionManager) prepareStart(boot string, up float64, duration int, p ResourcePolicy) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := withSessionLock(m.dir, func() error {
		m.stopLocked("STOPPED")
		m.session = LocalSession{randomID(), "STARTING", boot, up + float64(duration), time.Now().Add(time.Duration(duration) * time.Second), p}
		return save(filepath.Join(m.dir, "session.json"), m.session)
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
		m.session = session
		if e := save(filepath.Join(m.dir, "session.json"), session); e != nil {
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
			_ = save(filepath.Join(m.dir, "session.json"), m.session)
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
	Duration int            `json:"duration_seconds"`
	Policy   ResourcePolicy `json:"policy"`
	Accepted bool           `json:"accepted"`
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
	if !executionAvailable(m.root) {
		return errors.New("execution disabled: delegated cgroup v2 CPU/memory/pids limits required")
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
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.generation == generation && m.session.Status == "STARTING" {
			m.stopLocked("STOPPED")
			_ = withSessionLock(m.dir, func() error { return save(filepath.Join(m.dir, "session.json"), m.session) })
		}
	}()
	token, e := deviceToken(ctx, m.state)
	if e != nil {
		return e
	}
	var session struct {
		ID      string    `json:"id"`
		Expires time.Time `json:"expires_at"`
	}
	if e = request(ctx, m.state.Gateway, "/v1/agent/availability-sessions", "POST", token, map[string]interface{}{"device_id": m.state.Device, "duration_seconds": b.Duration, "policy": b.Policy, "consent_version": "sharing-v1", "accepted": true}, &session); e != nil {
		return e
	}
	if session.ID == "" || session.Expires.IsZero() {
		return errors.New("invalid company session response")
	}
	if session.Expires.Before(wallExpiry) {
		wallExpiry = session.Expires
	}
	return m.adoptSession(generation, LocalSession{session.ID, "ACTIVE", boot, up + float64(b.Duration), wallExpiry, b.Policy})
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
