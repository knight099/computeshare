package main

import (
	"context"
	"testing"
	"time"
)

func TestAvailabilityUsesBootClockAndNeverExtendsAfterWallClockRollback(t *testing.T) {
	s := LocalSession{ID: "test", Status: "ACTIVE", BootID: "boot-a", ExpiresBoot: 21610, ExpiresAt: time.Unix(21610, 0), Policy: ResourcePolicy{CPUPercent: 25, MemoryMB: 128}}
	if s.live("boot-a", 21611, time.Unix(1, 0)) {
		t.Fatal("wall clock rollback extended six-hour window")
	}
	if s.live("boot-b", 100, time.Unix(100, 0)) {
		t.Fatal("session resumed after reboot")
	}
	if !s.live("boot-a", 100, time.Unix(100, 0)) {
		t.Fatal("valid availability rejected")
	}
}
func TestStopCancelsLocalExecutionWithoutNetwork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &SessionManager{dir: t.TempDir(), session: LocalSession{ID: "test-session", Status: "ACTIVE"}, cancel: cancel}
	m.stopLocal("STOPPED")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Stop depended on network")
	}
	if m.session.Status != "STOPPED" {
		t.Fatal("local state not stopped")
	}
}
func TestExecutionFailsClosedWithoutRealDelegatedCgroup(t *testing.T) {
	if executionAvailable(t.TempDir()) {
		t.Fatal("ordinary directory mistaken for an enforceable cgroup")
	}
}
func TestSyntheticWorkloadRejectsUnsafeAndUnboundedInput(t *testing.T) {
	for _, j := range []HashJob{{Workload: "shell", Seed: "public", Iterations: 1}, {Workload: "synthetic-hash-v1", Seed: "public", Iterations: 1000001}, {Workload: "synthetic-hash-v1", Seed: "", Iterations: 1}} {
		if _, err := hashWork(context.Background(), j); err == nil {
			t.Fatal("accepted unsupported workload input")
		}
	}
}

func TestSyntheticKnownVector(t *testing.T) {
	got, err := hashWork(context.Background(), HashJob{Workload: "synthetic-hash-v1", Seed: "public", Iterations: 2})
	if err != nil || got != "315fc33dbdb63592565e8ed77eff5d3cf512638ea9b26631b7277cc73a3fc395" {
		t.Fatalf("incorrect deterministic result: %s %v", got, err)
	}
}
func TestExpiredSessionCancelsOfflineAndCannotResume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &SessionManager{dir: t.TempDir(), session: LocalSession{ID: "expired-session", Status: "ACTIVE", BootID: "old-boot", ExpiresAt: time.Now().Add(time.Hour)}, cancel: cancel}
	if m.snapshot().Status != "EXPIRED" {
		t.Fatal("offline expiry not enforced")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("expiry did not cancel running work")
	}
	restored, err := newSessionManager(m.dir, State{})
	if err != nil || restored.snapshot().Status != "EXPIRED" {
		t.Fatal("expired session revived after restart")
	}
}
