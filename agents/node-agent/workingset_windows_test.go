//go:build windows

package main

import "testing"

func TestWindowsWorkingSetRejectsInvalidHandleAndUnboundedBudget(t *testing.T) {
	for _, size := range []uintptr{0, 1024, 64 * 1024 * 1024} {
		if err := configureWindowsWorkingSet(0, size); err == nil {
			t.Fatal("accepted invalid process or memory budget")
		}
	}
}

func TestWindowsResidentSubBudgetPreservesFaultHeadroom(t *testing.T) {
	for _, budget := range []uintptr{64 * 1024 * 1024, 128 * 1024 * 1024, 512 * 1024 * 1024, 1024 * 1024 * 1024} {
		maximum, err := windowsResidentLimit(budget)
		if err != nil || maximum+64*1024 > budget || maximum < budget-1024*1024 {
			t.Fatalf("missing or excessive resident headroom: selected=%d maximum=%d err=%v", budget, maximum, err)
		}
	}
	for _, budget := range []uintptr{0, 63 * 1024 * 1024, 1024*1024*1024 + 1} {
		if _, err := windowsResidentLimit(budget); err == nil {
			t.Fatal("invalid resident budget accepted")
		}
	}
}
