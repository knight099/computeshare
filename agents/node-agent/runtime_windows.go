//go:build windows

package main

import (
	"context"
	"errors"
)

// Inventory/enrollment cannot grant execution. Windows has no enabled backend
// until native Job Object resource and supervisor-death acceptance is complete.
func executionAvailable(string) bool { return false }
func runLimited(context.Context, string, HashJob, ResourcePolicy) (WorkResult, error) {
	return WorkResult{}, errors.New("Windows compute is unavailable until native enforcement is verified")
}
func cleanupStaleWorkloads(string) (int, error) { return 0, nil }

func runtimeUnavailableReason() string {
	return "Windows enrollment preview: compute remains disabled until native resource enforcement is verified."
}
func workerRunning(string, string) bool { return false }

func internalHashWorker() error { return errors.New("Windows compute is disabled") }
