package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
)

// Linux parent-death signals track the spawning thread; keep it alive until Wait.
func runParentBound(cmd *exec.Cmd, setup func(*os.Process) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := setup(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

var ownedGroupName = regexp.MustCompile(`^job-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func cleanupStaleCgroups(root string) (int, error) {
	if !executionAvailable(root) {
		return 0, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || !ownedGroupName.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		events, e := os.ReadFile(filepath.Join(dir, "cgroup.events"))
		if e != nil {
			continue
		}
		empty := false
		for _, line := range strings.Split(string(events), "\n") {
			if strings.TrimSpace(line) == "populated 0" {
				empty = true
			}
		}
		if empty {
			if e = os.Remove(dir); e == nil {
				removed++
			} else if !errors.Is(e, syscall.EBUSY) && !errors.Is(e, syscall.ENOTEMPTY) {
				return removed, e
			}
		}
	}
	return removed, nil
}
