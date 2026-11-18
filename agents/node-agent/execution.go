package main

import "context"

// Internal dependency boundary shared by consent, transport and diagnostics.
// It is never decoded from configuration or company messages. Production uses
// the build-tagged platform backend, including its fail-closed capability gate.
type executionBackend interface {
	available(string) bool
	execute(context.Context, string, HashJob, ResourcePolicy) (WorkResult, error)
	running(string, string) bool
	reason() string
}
type platformExecution struct{}

func (platformExecution) available(root string) bool { return executionAvailable(root) }
func (platformExecution) execute(ctx context.Context, root string, job HashJob, policy ResourcePolicy) (WorkResult, error) {
	return runLimited(ctx, root, job, policy)
}
func (platformExecution) running(root, attempt string) bool { return workerRunning(root, attempt) }
func (platformExecution) reason() string                    { return runtimeUnavailableReason() }
func (m *SessionManager) execution() executionBackend {
	if m.backend != nil {
		return m.backend
	}
	return platformExecution{}
}
