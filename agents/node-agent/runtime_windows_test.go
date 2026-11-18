//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "internal-hash-worker" {
		// Distinguish an explicit rejection by the real worker function from
		// timeout/forced termination or a launch error in negative tests.
		if err := internalHashWorker(); err != nil {
			os.Exit(37)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func windowsAssignmentFixture() HashJob {
	return HashJob{Workload: "synthetic-hash-v1", Seed: "public", Iterations: 2, AttemptID: randomID(), JobID: randomID(), SessionID: randomID(), Epoch: 1, Timeout: 10, LeaseExpires: time.Now().Add(20 * time.Second)}
}

func TestWindowsBoundedAdapterUsesRealEntrypointAndKernelMeasurements(t *testing.T) {
	if executionAvailable("") {
		t.Fatal("private adapter prematurely enabled execution")
	}
	result, err := runWindowsHash(context.Background(), windowsAssignmentFixture(), ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	if err != nil || result.Result != "315fc33dbdb63592565e8ed77eff5d3cf512638ea9b26631b7277cc73a3fc395" || result.MemoryBytes == 0 || result.MemoryBytes > 128*1024*1024 {
		t.Fatalf("bounded worker failed: %+v %v", result, err)
	}
}

func TestWindowsAdapterRejectsInvalidAssignmentsBeforeLaunch(t *testing.T) {
	for _, mutate := range []func(*HashJob){
		func(j *HashJob) { j.AttemptID = "../../worker" }, func(j *HashJob) { j.JobID = "" }, func(j *HashJob) { j.SessionID = "invalid" }, func(j *HashJob) { j.Epoch = 0 }, func(j *HashJob) { j.Timeout = 0 }, func(j *HashJob) { j.Timeout = 31 }, func(j *HashJob) { j.LeaseExpires = time.Now().Add(-time.Second) }, func(j *HashJob) { j.Iterations = 1000001 }, func(j *HashJob) { j.Seed = "$(command)" }, func(j *HashJob) { j.Workload = "shell" },
	} {
		job := windowsAssignmentFixture()
		mutate(&job)
		if result, err := runWindowsHash(context.Background(), job, ResourcePolicy{CPUPercent: 25, MemoryMB: 128}); err == nil || result != (WorkResult{}) {
			t.Fatal("invalid assignment returned success")
		}
	}
}

func TestWindowsAdapterFencesOverlapAndReleasesAfterLocalStop(t *testing.T) {
	work := windowsAssignmentFixture()
	work.Iterations = 1000000
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := runWindowsHash(ctx, work, ResourcePolicy{CPUPercent: 10, MemoryMB: 128})
		if result != (WorkResult{}) {
			done <- errors.New("canceled worker returned a result")
			return
		}
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !windowsWork.running(work.AttemptID) {
		select {
		case err := <-done:
			t.Fatalf("worker ended before Stop: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("worker never became active")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := runWindowsHash(context.Background(), windowsAssignmentFixture(), ResourcePolicy{CPUPercent: 25, MemoryMB: 128}); !errors.Is(err, errWindowsWorkerBusy) {
		cancel()
		<-done
		t.Fatalf("overlapping worker was accepted: %v", err)
	}
	started := time.Now()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop not propagated: %v", err)
	}
	if time.Since(started) > 2*time.Second || windowsWork.running(work.AttemptID) {
		t.Fatal("Stop left an active worker")
	}
	if _, err := runWindowsHash(context.Background(), windowsAssignmentFixture(), ResourcePolicy{CPUPercent: 25, MemoryMB: 128}); err != nil {
		t.Fatalf("Stop leaked the worker slot: %v", err)
	}
}

func TestWindowsWorkerRejectsSpoofedContextWithoutKernelLimits(t *testing.T) {
	policy, _ := json.Marshal(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	command := exec.Command(os.Args[0], "internal-hash-worker")
	input, _ := json.Marshal(windowsAssignmentFixture())
	command.Stdin = bytes.NewReader(input)
	command.Env = append(os.Environ(), "GOMAXPROCS=1", "OPENCOMPUTE_WORKER_POLICY="+string(policy), "OPENCOMPUTE_WORKER_HOST_CPUS="+strconv.Itoa(int(windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS))))
	output, err := command.CombinedOutput()
	var rejected *exec.ExitError
	if !errors.As(err, &rejected) || rejected.ExitCode() != 37 || len(output) != 0 {
		t.Fatalf("unprotected worker accepted forged environment: %v", err)
	}
}

func TestWindowsWorkerRejectsMalformedOrMismatchedEnforcedInput(t *testing.T) {
	for _, mode := range []string{"unknown-field", "trailing-value", "invalid-workload", "mismatched-policy", "mismatched-topology"} {
		t.Run(mode, func(t *testing.T) {
			policy := ResourcePolicy{CPUPercent: 25, MemoryMB: 128}
			job, err := newWindowsJob(policy)
			if err != nil {
				t.Fatal(err)
			}
			defer job.close()
			declared := policy
			if mode == "mismatched-policy" {
				declared.CPUPercent = 100
			}
			env := windowsWorkerEnvironment(declared, job.hostCPUs)
			if mode == "mismatched-topology" {
				env["OPENCOMPUTE_WORKER_HOST_CPUS"] = "0"
			}
			child, err := job.start(context.Background(), os.Args[0], []string{"internal-hash-worker"}, env)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { job.close(); child.close() }()
			work := windowsAssignmentFixture()
			if mode == "invalid-workload" {
				work.Iterations = 1000001
			}
			input, _ := json.Marshal(work)
			if mode == "unknown-field" {
				input = append(input[:len(input)-1], []byte(",\"command\":\"shell\"}")...)
			}
			if mode == "trailing-value" {
				input = append(input, []byte(" {}")...)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// The payload fits the native pipe buffer even if rejected before
			// reading. Do not cancel the process just because its stdin closed.
			_, _ = child.input.Write(input)
			child.input.Close()
			output := make(chan []byte, 1)
			go func() { data, _ := io.ReadAll(io.LimitReader(child.output, 8193)); output <- data }()
			code, err := job.wait(ctx, child)
			child.output.Close()
			data := <-output
			if err != nil || code != 37 || len(data) != 0 {
				t.Fatalf("expected explicit worker rejection, got code=%d err=%v", code, err)
			}
		})
	}
}

func TestWindowsAdapterRejectsForwardWallClockLeaseJump(t *testing.T) {
	work := windowsAssignmentFixture()
	work.Iterations = 1000000
	var jumped atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := func() time.Time {
		now := time.Now()
		if jumped.Load() {
			return now.Add(time.Hour)
		}
		return now
	}
	done := make(chan error, 1)
	go func() {
		result, err := runWindowsHashWithClock(ctx, work, ResourcePolicy{CPUPercent: 10, MemoryMB: 128}, clock)
		if result != (WorkResult{}) {
			done <- errors.New("expired absolute lease returned a result")
			return
		}
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !windowsWork.running(work.AttemptID) {
		select {
		case err := <-done:
			t.Fatalf("worker ended before clock jump: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("worker did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	jumped.Store(true)
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("absolute lease jump was not fenced: %v", err)
	}
	if windowsWork.running(work.AttemptID) {
		t.Fatal("expired lease retained a live worker")
	}
}

func TestWindowsAdapterEnforcesLeaseDuringWork(t *testing.T) {
	work := windowsAssignmentFixture()
	work.Iterations = 1000000
	work.LeaseExpires = time.Now().Add(100 * time.Millisecond)
	result, err := runWindowsHash(context.Background(), work, ResourcePolicy{CPUPercent: 10, MemoryMB: 128})
	if result != (WorkResult{}) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mid-flight lease expiry returned usable work: %v", err)
	}
}
