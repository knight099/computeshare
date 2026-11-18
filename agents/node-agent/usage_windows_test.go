//go:build windows

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func runWindowsPressureFixture(t *testing.T, policy ResourcePolicy, mode string, extra map[string]string, duration time.Duration) (uint32, error, windowsResourceUsage, string, time.Duration) {
	t.Helper()
	started := time.Now()
	job, err := newWindowsJob(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	env := map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": mode, "OPENCOMPUTE_EXPECT_MEMORY_MB": strconv.Itoa(policy.MemoryMB)}
	for k, v := range extra {
		env[k] = v
	}
	child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, env)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { job.close(); child.close() }()
	nativeWorkerReady(t, job, child)
	output := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(child.output); output <- data }()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	code, waitErr := job.wait(ctx, child)
	if state, _ := windows.WaitForSingleObject(child.process, 0); state != windows.WAIT_OBJECT_0 {
		job.close()
		t.Fatal("pressure fixture left an active worker")
	}
	usage, err := job.usage()
	if err != nil {
		t.Fatal(err)
	}
	return code, waitErr, usage, string(<-output), time.Since(started)
}
func TestWindowsPrivateCommitCapRejectsAllocationThatSucceedsUnderLargerCap(t *testing.T) {
	extra := map[string]string{"GOMEMLIMIT": "512MiB"}
	code, err, usage, output, _ := runWindowsPressureFixture(t, ResourcePolicy{CPUPercent: 100, MemoryMB: 512}, "memory", extra, 10*time.Second)
	if err != nil || code != 0 || !strings.Contains(output, "allocated:268435456") || usage.PeakCommitBytes < 256*1024*1024 || usage.PeakCommitBytes > 512*1024*1024 {
		t.Fatalf("control allocation failed: exit=%d err=%v peak=%d", code, err, usage.PeakCommitBytes)
	}
	code, err, usage, _, _ = runWindowsPressureFixture(t, ResourcePolicy{CPUPercent: 100, MemoryMB: 128}, "memory", extra, 10*time.Second)
	if err != nil || code == 0 || usage.PeakCommitBytes > 128*1024*1024 {
		t.Fatalf("allocation escaped lower memory cap: exit=%d err=%v peak=%d", code, err, usage.PeakCommitBytes)
	}
}
func TestWindowsBusyWorkerStaysWithinOneCoreQuotaAndStops(t *testing.T) {
	_, err, usage, _, elapsed := runWindowsPressureFixture(t, ResourcePolicy{CPUPercent: 25, MemoryMB: 128}, "busy", nil, time.Second)
	if !errors.Is(err, context.DeadlineExceeded) || usage.CPUUsec == 0 {
		t.Fatalf("busy worker/stop fixture failed: %v cpu=%d", err, usage.CPUUsec)
	}
	allowed := uint64(elapsed.Seconds()*0.25*1000000) + 150000
	if usage.CPUUsec > allowed {
		t.Fatalf("CPU usage exceeded selected one-core quota plus scheduling tolerance: used=%d allowed=%d", usage.CPUUsec, allowed)
	}
}

// Diagnostics do not replace the strict acceptance assertion above.
func TestWindowsCPUQuotaLongWindowDiagnostics(t *testing.T) {
	host := windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)
	t.Logf("CPU topology: active=%d maximum=%d process=%d environment=%s", host, windows.GetMaximumProcessorCount(windows.ALL_PROCESSOR_GROUPS), runtime.NumCPU(), os.Getenv("NUMBER_OF_PROCESSORS"))
	for _, percent := range []int{10, 25, 50} {
		rate, err := windowsCPUCap(percent, int(host))
		if err != nil {
			t.Fatal(err)
		}
		_, waitErr, usage, _, elapsed := runWindowsPressureFixture(t, ResourcePolicy{CPUPercent: percent, MemoryMB: 128}, "busy", nil, 4*time.Second)
		if !errors.Is(waitErr, context.DeadlineExceeded) {
			t.Fatal(waitErr)
		}
		t.Logf("CPU diagnostic: selected=%d rate=%d wall_seconds=%.3f cpu_usec=%d utilization_cores=%.3f", percent, rate, elapsed.Seconds(), usage.CPUUsec, float64(usage.CPUUsec)/elapsed.Seconds()/1000000)
	}
}
