//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func executionAvailable(root string) bool {
	if root == "" {
		return false
	}
	clean, err := filepath.EvalSymlinks(root)
	if err != nil || clean != filepath.Clean(root) {
		return false
	}
	rel, err := filepath.Rel("/sys/fs/cgroup", clean)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	var stat syscall.Statfs_t
	if syscall.Statfs(clean, &stat) != nil || stat.Type != 0x63677270 {
		return false
	}
	controls, err := os.ReadFile(filepath.Join(clean, "cgroup.subtree_control"))
	if err != nil {
		return false
	}
	for _, required := range []string{"cpu", "memory", "pids"} {
		found := false
		for _, v := range strings.Fields(string(controls)) {
			if v == required {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return syscall.Access(clean, 2) == nil
}

func runLimited(ctx context.Context, root string, job HashJob, policy ResourcePolicy) (WorkResult, error) {
	if !executionAvailable(root) || !policy.valid() {
		return WorkResult{}, errors.New("delegated CPU/memory controls unavailable")
	}
	if len(job.AttemptID) != 36 || strings.ContainsAny(job.AttemptID, "/\\.") {
		return WorkResult{}, errors.New("invalid attempt identity")
	}
	group := filepath.Join(root, "job-"+job.AttemptID)
	if err := os.Mkdir(group, 0700); err != nil {
		return WorkResult{}, errors.New("cannot create worker cgroup")
	}
	defer os.Remove(group)
	for name, value := range map[string]string{"cpu.max": fmt.Sprintf("%d 100000", policy.CPUPercent*1000), "memory.max": strconv.Itoa(policy.MemoryMB * 1024 * 1024), "memory.swap.max": "0", "pids.max": "32"} {
		if err := os.WriteFile(filepath.Join(group, name), []byte(value), 0600); err != nil {
			return WorkResult{}, errors.New("cannot enforce worker resource limits")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return WorkResult{}, err
	}
	cmd := exec.CommandContext(ctx, executable, "internal-hash-worker")
	cmd.Env = []string{"GOMAXPROCS=1", "GOMEMLIMIT=" + strconv.Itoa(policy.MemoryMB-16) + "MiB"}
	input, err := cmd.StdinPipe()
	if err != nil {
		return WorkResult{}, err
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	err = runParentBound(cmd, func(process *os.Process) error {
		defer input.Close()
		if e := os.WriteFile(filepath.Join(group, "cgroup.procs"), []byte(strconv.Itoa(process.Pid)), 0600); e != nil {
			return errors.New("cannot attach worker to cgroup")
		}
		return json.NewEncoder(input).Encode(job)
	})
	if err != nil {
		return WorkResult{}, errors.New("worker canceled, limited or failed")
	}
	var result WorkResult
	if err = json.Unmarshal(output.Bytes(), &result); err != nil || len(result.Result) != 64 {
		return WorkResult{}, errors.New("invalid worker output")
	}
	cpu, _ := os.ReadFile(filepath.Join(group, "cpu.stat"))
	fields := strings.Fields(string(cpu))
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == "usage_usec" {
			result.CPUUsec, _ = strconv.ParseUint(fields[i+1], 10, 64)
		}
	}
	memory, _ := os.ReadFile(filepath.Join(group, "memory.peak"))
	result.MemoryBytes, _ = strconv.ParseUint(strings.TrimSpace(string(memory)), 10, 64)
	return result, nil
}

func runtimeUnavailableReason() string {
	return "Delegated Linux cgroup v2 CPU/memory/pids limits are required; see the contributor guide."
}
func workerRunning(root, attempt string) bool {
	if attempt == "" || !executionAvailable(root) {
		return false
	}
	procs, _ := os.ReadFile(filepath.Join(root, "job-"+attempt, "cgroup.procs"))
	return strings.TrimSpace(string(procs)) != ""
}

func internalHashWorker() error { return hashWorker() }
