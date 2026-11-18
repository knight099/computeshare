package main

import (
	"math"
	"testing"
	"time"
)

func TestCPUTimePacingNeverRunsAheadOfSelectedBudget(t *testing.T) {
	for _, tc := range []struct {
		used    uint64
		elapsed time.Duration
		percent int
		want    time.Duration
	}{
		{0, 0, 25, 0},
		{100000, 100 * time.Millisecond, 25, 300 * time.Millisecond},
		{100000, 500 * time.Millisecond, 25, 0},
		{100001, 0, 10, 1000010 * time.Microsecond},
		{1, 0, 33, 3031 * time.Nanosecond},
		{100000, 0, 100, 100 * time.Millisecond},
	} {
		got, err := cpuTimeDelay(tc.used, tc.elapsed, tc.percent)
		if err != nil || got != tc.want {
			t.Fatalf("used=%d elapsed=%v percent=%d: got=%v err=%v", tc.used, tc.elapsed, tc.percent, got, err)
		}
	}
	for _, tc := range []struct {
		used    uint64
		elapsed time.Duration
		percent int
	}{
		{1, 0, 0}, {1, 0, 9}, {1, 0, 101}, {1, -1, 25}, {math.MaxUint64, 0, 25},
	} {
		if _, err := cpuTimeDelay(tc.used, tc.elapsed, tc.percent); err == nil {
			t.Fatal("invalid or overflowing CPU budget accepted")
		}
	}
}

func TestCPUTimePacingCannotBankIdleOrSuspendTime(t *testing.T) {
	credit, err := cpuTimeDebt(0, 0, time.Hour, 25)
	if err != nil || credit != -100*time.Millisecond {
		t.Fatalf("unbounded idle credit: %v %v", credit, err)
	}
	debt, err := cpuTimeDebt(credit, 250000, 0, 25)
	if err != nil || debt != 900*time.Millisecond {
		t.Fatalf("idle time funded an unrestricted burst: %v %v", debt, err)
	}
	settled, err := cpuTimeDebt(debt, 0, 900*time.Millisecond, 25)
	if err != nil || settled != 0 {
		t.Fatalf("pacing debt did not settle: %v %v", settled, err)
	}
	if _, err := cpuTimeDebt(time.Duration(math.MaxInt64), 1, 0, 25); err == nil {
		t.Fatal("pacing debt overflow accepted")
	}
	if _, err := cpuTimeDebt(-time.Second, 1, 0, 25); err == nil {
		t.Fatal("excess stored credit accepted")
	}
}
