package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestSafetyPolicyRequiresKnownEligibleFreshSensors(t *testing.T) {
	ac, idle, temp := true, uint64(600), 70000
	safe := hostObservation{AC: &ac, IdleSeconds: &idle, CPUMilliC: &temp}
	policy := SafetyPolicy{ACOnly: true, IdleSeconds: 300, MaxCPUMilliC: 80000}
	if err := policy.evaluate(safe); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*hostObservation){func(s *hostObservation) { s.AC = nil }, func(s *hostObservation) { v := false; s.AC = &v }, func(s *hostObservation) { s.IdleSeconds = nil }, func(s *hostObservation) { v := uint64(299); s.IdleSeconds = &v }, func(s *hostObservation) { s.CPUMilliC = nil }, func(s *hostObservation) { v := 80000; s.CPUMilliC = &v }} {
		sample := safe
		mutate(&sample)
		if policy.evaluate(sample) == nil {
			t.Fatal("unsafe or missing sensor accepted")
		}
	}
	for _, p := range []SafetyPolicy{{IdleSeconds: 1}, {IdleSeconds: 3601}, {MaxCPUMilliC: 49999}, {MaxCPUMilliC: 95001}} {
		if p.valid() {
			t.Fatal("invalid safety selection accepted")
		}
	}
	if err := (SafetyPolicy{}).evaluate(hostObservation{}); err != nil {
		t.Fatal("unguarded policy unexpectedly requires sensors")
	}
}

func TestSafetySamplerBoundsInFlightAndExpiresData(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	var calls atomic.Int32
	monitor := newSafetyMonitor(func(context.Context) hostObservation {
		calls.Add(1)
		<-block
		v := true
		return hostObservation{AC: &v}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if sample := monitor.observe(ctx, true); sample.AC != nil {
		t.Fatal("blocked sensor fabricated a reading")
	}
	for i := 0; i < 100; i++ {
		monitor.observe(context.Background(), false)
	}
	if calls.Load() != 1 {
		t.Fatal("sensor requests grew without bound")
	}
	ac := true
	monitor.mu.Lock()
	monitor.cached = hostObservation{AC: &ac}
	monitor.collected = time.Now().Add(-3 * time.Second)
	monitor.mu.Unlock()
	if monitor.observe(context.Background(), false).AC != nil {
		t.Fatal("stale AC reading accepted")
	}
}

func TestSafetyStopsLocallyAndGuardedStateCannotResume(t *testing.T) {
	ac := false
	dir := testStateDir(t)
	boot, up, err := bootClock()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &SessionManager{dir: dir, session: LocalSession{ID: randomID(), Status: "ACTIVE", BootID: boot, ExpiresBoot: up + 3600, ExpiresAt: time.Now().Add(time.Hour), Policy: ResourcePolicy{CPUPercent: 10, MemoryMB: 128}, Safety: SafetyPolicy{ACOnly: true}}, cancel: cancel, safetySource: func(context.Context) hostObservation { return hostObservation{AC: &ac} }}
	if err := saveLocalSession(dir, manager.session); err != nil {
		t.Fatal(err)
	}
	data, err := readSecret(filepath.Join(dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		Status string `json:"status"`
	}
	json.Unmarshal(data, &legacy)
	if legacy.Status == "ACTIVE" {
		t.Fatal("old client could silently ignore safety constraints")
	}
	restored, err := newSessionManager(dir, State{})
	if err != nil {
		t.Fatal(err)
	}
	if restored.snapshot().Status == "ACTIVE" {
		t.Fatal("guarded sharing resumed without consent")
	}
	manager.health().observe(context.Background(), true)
	if manager.snapshot().Status != "STOPPED" {
		t.Fatal("unsafe power did not stop sharing")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("local safety stop did not cancel computation")
	}
	ac = true
	if manager.snapshot().Status == "ACTIVE" {
		t.Fatal("safe readings automatically resumed sharing")
	}
}

func TestSafetyStartRejectsBeforeNetworkOrStateMutation(t *testing.T) {
	manager := &SessionManager{dir: testStateDir(t), backend: &protocolAcceptanceRuntime{}, safetySource: func(context.Context) hostObservation { return hostObservation{} }}
	err := manager.start(context.Background(), StartSharing{Duration: 3600, Accepted: true, Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, Safety: SafetyPolicy{ACOnly: true}})
	if err == nil || err.Error() != "Sharing requires a known AC power connection." {
		t.Fatalf("unsafe start reached company/state path: %v", err)
	}
	if manager.session.Status != "STOPPED" {
		t.Fatal("unsafe start left pending consent")
	}
}

func TestSafetyBlockedSensorDoesNotBlockLocalStop(t *testing.T) {
	block, started := make(chan struct{}), make(chan struct{})
	defer close(block)
	manager := &SessionManager{dir: testStateDir(t), safetySource: func(context.Context) hostObservation {
		close(started)
		<-block
		return hostObservation{}
	}}
	manager.health().observe(context.Background(), false)
	<-started
	done := make(chan struct{})
	go func() { manager.stopLocal("STOPPED"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sensor blocked local Stop")
	}
}

func TestSafetyStopWinsAgainstPendingSensorStart(t *testing.T) {
	block, started := make(chan struct{}), make(chan struct{})
	manager := &SessionManager{dir: testStateDir(t), backend: &protocolAcceptanceRuntime{}, safetySource: func(context.Context) hostObservation {
		close(started)
		<-block
		ac := true
		return hostObservation{AC: &ac}
	}}
	result := make(chan error, 1)
	go func() {
		result <- manager.start(context.Background(), StartSharing{Duration: 3600, Accepted: true, Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, Safety: SafetyPolicy{ACOnly: true}})
	}()
	<-started
	manager.stopLocal("STOPPED")
	close(block)
	if err := <-result; err == nil || err.Error() != "sharing was stopped while starting" {
		t.Fatalf("pending Start bypassed Stop: %v", err)
	}
	if manager.snapshot().Status != "STOPPED" {
		t.Fatal("sharing resumed after Stop")
	}
}

func TestSafetyFreshnessRejectsWallDiscontinuities(t *testing.T) {
	now := time.Now()
	if safetyAge(now, now.Add(-time.Second)) <= 2*time.Second {
		t.Fatal("backward clock accepted")
	}
	if safetyAge(now, now.Add(3*time.Second)) <= 2*time.Second {
		t.Fatal("suspend-aged observation accepted")
	}
	if safetyAge(now, now.Add(time.Millisecond)) >= 500*time.Millisecond {
		t.Fatal("fresh sample rejected")
	}
}
