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
