package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPlatformStateDirectoryRejectsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(path, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareStateDirectory(path); err == nil {
		t.Fatal("accepted file as private state directory")
	}
}
func TestPlatformInventoryAndClock(t *testing.T) {
	caps, err := capabilities()
	if err != nil {
		t.Fatal(err)
	}
	if caps.CPU < 1 || caps.Memory < 1 || len(caps.Runtimes) != 0 {
		t.Fatalf("invalid inventory: %+v", caps)
	}
	id, up, err := bootClock()
	if err != nil || id == "" || up < 0 {
		t.Fatalf("invalid lifetime clock: %v", err)
	}
	time.Sleep(time.Millisecond)
	next, after, err := bootClock()
	if err != nil || next != id || after < up {
		t.Fatal("platform clock moved backwards or changed identity")
	}
}

func testStateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := prepareStateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}
