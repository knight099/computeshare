//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParentDeathHelper(t *testing.T) {
	mode := os.Getenv("OPENCOMPUTE_SUPERVISION_HELPER")
	if mode == "child" {
		for {
			time.Sleep(time.Minute)
		}
	}
	if mode != "parent" {
		t.Skip("subprocess helper")
	}
	child := exec.Command(os.Args[0], "-test.run=^TestParentDeathHelper$")
	child.Env = append(os.Environ(), "OPENCOMPUTE_SUPERVISION_HELPER=child")
	if err := runParentBound(child, func(p *os.Process) error { fmt.Println(p.Pid); return nil }); err != nil {
		os.Exit(1)
	}
}
func TestWorkerDiesWhenSupervisorIsKilled(t *testing.T) {
	parent := exec.Command(os.Args[0], "-test.run=^TestParentDeathHelper$")
	parent.Env = append(os.Environ(), "OPENCOMPUTE_SUPERVISION_HELPER=parent")
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		if scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	var line string
	select {
	case line = <-lines:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not start worker")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	child, _ := os.FindProcess(pid)
	defer child.Kill()
	if err = parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stat, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(e) {
			return
		}
		end := strings.LastIndex(string(stat), ") ")
		if end >= 0 && len(stat) > end+2 && stat[end+2] == 'Z' {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker remained alive after supervisor SIGKILL")
}
func TestStaleCleanupNeverTouchesOrdinaryDirectories(t *testing.T) {
	dir := t.TempDir()
	child := dir + "/job-11111111-1111-4111-8111-111111111111"
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	_, _ = cleanupStaleWorkloads(dir)
	if _, err := os.Stat(child); err != nil {
		t.Fatal("cleanup touched a directory outside cgroup v2")
	}
}
