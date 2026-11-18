//go:build windows

package main

import (
	"context"
	"errors"
)

// Inventory/enrollment cannot grant execution. Windows has no enabled backend
// until native Job Object resource and supervisor-death acceptance is complete.
func executionAvailable(string) bool { return false }
func runLimited(ctx context.Context, root string, job HashJob, policy ResourcePolicy) (WorkResult, error) {
	if !executionAvailable(root) {
		return WorkResult{}, errors.New("Windows compute is unavailable until native enforcement is verified")
	}
	return runWindowsHash(ctx, job, policy)
}
func cleanupStaleWorkloads(string) (int, error) { return 0, nil }

func runtimeUnavailableReason() string {
	return "Windows enrollment preview: compute remains disabled until native resource enforcement is verified."
}
func workerRunning(_ string, attempt string) bool { return windowsWork.running(attempt) }

func internalHashWorker() error { return windowsHashWorker() }
