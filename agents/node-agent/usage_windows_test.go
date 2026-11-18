//go:build windows

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

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

func TestWindowsActiveProcessLimitRejectsWorkerDescendant(t *testing.T) {
	// Establish that this exact executable/test selection can start outside
	// the constrained worker; missing files are not evidence of enforcement.
	if err := exec.Command(os.Args[0], "-test.run=^$").Run(); err != nil {
		t.Fatalf("descendant launch positive control failed: %v", err)
	}
	code, err, _, output, _ := runWindowsPressureFixture(t, ResourcePolicy{CPUPercent: 100, MemoryMB: 128}, "process-limit", nil, 5*time.Second)
	if err != nil || code != 0 || !strings.Contains(output, "process-denied:") {
		t.Fatalf("descendant process was not rejected: exit=%d err=%v output=%q", code, err, output)
	}
}

// Compare two independent kernel accounting APIs on the same continuously
// running process. Separate intervals distinguish startup from sustained usage.
func TestWindowsCPUAccountingAcrossContinuousIntervals(t *testing.T) {
	job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "busy"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { job.close(); child.close() }()
	nativeWorkerReady(t, job, child)
	previous, err := job.usage()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	baseline, baselineTime := previous.CPUUsec, started
	queryCycles := windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryProcessCycleTime")
	for interval := 1; interval <= 4; interval++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		<-ctx.Done()
		cancel()
		usage, err := job.usage()
		if err != nil {
			t.Fatal(err)
		}
		sampled := time.Now()
		elapsed := sampled.Sub(started)
		var cycles uint64
		if ok, _, err := queryCycles.Call(uintptr(child.process), uintptr(unsafe.Pointer(&cycles))); ok == 0 {
			t.Fatalf("query process cycles: %v", err)
		}
		t.Logf("continuous CPU: interval=%d wall_seconds=%.3f job_usec=%d cycles=%d interval_cores=%.3f", interval, elapsed.Seconds(), usage.CPUUsec, cycles, float64(usage.CPUUsec-previous.CPUUsec)/elapsed.Seconds()/1000000)
		previous, started = usage, sampled
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := job.wait(ctx, child); !errors.Is(err, context.Canceled) {
		t.Fatalf("continuous worker did not stop: %v", err)
	}
	stopped := time.Now()
	usage, err := job.usage()
	if err != nil {
		t.Fatal(err)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(child.process, &created, &exited, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	processUsec := ((uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)) + (uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime))) / 10
	// Both counters are stable after termination. Supervisor descheduling
	// between live queries must not turn correct accounting into a flaky test.
	if processUsec != usage.CPUUsec {
		t.Fatalf("terminated job/process accounting mismatch: job=%d process=%d", usage.CPUUsec, processUsec)
	}
	used := usage.CPUUsec - baseline
	allowed := uint64(stopped.Sub(baselineTime).Seconds()*0.25*1000000) + 150000
	t.Logf("sustained CPU: used_usec=%d allowed_usec=%d process_usec=%d", used, allowed, processUsec)
	if used > allowed {
		t.Errorf("sustained CPU usage exceeded selected one-core quota plus scheduling tolerance: used=%d allowed=%d", used, allowed)
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
