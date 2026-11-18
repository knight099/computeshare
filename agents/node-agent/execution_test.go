package main

import "testing"

func TestSessionRuntimeDefaultsToPlatformGate(t *testing.T) {
	m := &SessionManager{}
	if m.execution().available(m.root) != executionAvailable(m.root) {
		t.Fatal("runtime selection bypassed platform gate")
	}
	if m.execution().reason() != runtimeUnavailableReason() {
		t.Fatal("runtime diagnostics diverged")
	}
}
