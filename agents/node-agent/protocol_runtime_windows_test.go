//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type protocolAcceptanceRuntime struct{ platformExecution }

func (protocolAcceptanceRuntime) available(string) bool { return true }
func (protocolAcceptanceRuntime) execute(ctx context.Context, _ string, j HashJob, p ResourcePolicy) (WorkResult, error) {
	return runWindowsHash(ctx, j, p)
}

func TestWindowsProtocolRestartFixture(t *testing.T) {
	dir := os.Getenv("OPENCOMPUTE_PROTOCOL_RESTART_FIXTURE_DIR")
	if dir == "" {
		t.Skip("restart subprocess fixture")
	}
	manager, err := newSessionManager(dir, State{})
	if err != nil {
		t.Fatal(err)
	}
	if session := manager.snapshot(); session.Status != "EXPIRED" {
		t.Fatalf("restart resumed saved consent: %s", session.Status)
	}
}
func TestWindowsRestartCannotResumeSavedSharing(t *testing.T) {
	dir := testStateDir(t)
	boot, up, err := bootClock()
	if err != nil {
		t.Fatal(err)
	}
	session := LocalSession{ID: randomID(), Status: "ACTIVE", BootID: boot, ExpiresBoot: up + 3600, ExpiresAt: time.Now().Add(time.Hour), Policy: ResourcePolicy{CPUPercent: 10, MemoryMB: 128}}
	if err := save(filepath.Join(dir, "session.json"), session); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsProtocolRestartFixture$")
	child.Env = append(os.Environ(), "OPENCOMPUTE_PROTOCOL_RESTART_FIXTURE_DIR="+dir)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("restart acceptance failed: %v %s", err, output)
	}
}
