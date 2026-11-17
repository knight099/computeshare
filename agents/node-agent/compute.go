package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type ResourcePolicy struct {
	CPUPercent int  `json:"cpu_percent"`
	MemoryMB   int  `json:"memory_mb"`
	GPU        bool `json:"gpu_enabled"`
	IdleOnly   bool `json:"idle_only"`
}

func (p ResourcePolicy) valid() bool {
	return p.CPUPercent >= 10 && p.CPUPercent <= 100 && p.MemoryMB >= 64 && p.MemoryMB <= 1024 && !p.GPU && !p.IdleOnly
}

type HashJob struct {
	Workload     string    `json:"workload_type"`
	Seed         string    `json:"seed"`
	Iterations   int       `json:"iterations"`
	AttemptID    string    `json:"attempt_id,omitempty"`
	JobID        string    `json:"job_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Epoch        int       `json:"epoch,omitempty"`
	LeaseExpires time.Time `json:"lease_expires_at,omitempty"`
	Timeout      int       `json:"timeout_seconds,omitempty"`
}

func hashWork(ctx context.Context, j HashJob) (string, error) {
	if j.Workload != "synthetic-hash-v1" || j.Iterations < 1 || j.Iterations > 1000000 || len(j.Seed) < 1 || len(j.Seed) > 64 {
		return "", errors.New("unsupported synthetic workload")
	}
	for _, c := range j.Seed {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(" _.-", c)) {
			return "", errors.New("invalid public seed")
		}
	}
	data := []byte(j.Seed)
	for i := 0; i < j.Iterations; i++ {
		if i%4096 == 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			default:
			}
		}
		sum := sha256.Sum256(data)
		data = sum[:]
	}
	return hex.EncodeToString(data), nil
}
func hashWorker() error {
	var j HashJob
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&j); err != nil {
		return err
	}
	result, err := hashWork(context.Background(), j)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"result": result})
}

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

type WorkResult struct {
	Result      string `json:"result"`
	CPUUsec     uint64 `json:"cpu_usage_usec"`
	MemoryBytes uint64 `json:"memory_peak_bytes"`
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
