//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestWindowsHashPacingPreservesKnownResultWithinCPUConsent(t *testing.T) {
	work := HashJob{Workload: "synthetic-hash-v1", Seed: "public", Iterations: 1000000}
	// Independently generated with Python hashlib.sha256 for this public seed.
	const expected = "1d38182d44573b3b477a40b3b725e19b64bfd0235a3484e687022d28a090022c"
	input, err := json.Marshal(work)
	if err != nil {
		t.Fatal(err)
	}
	for _, percent := range []int{10, 25, 50, 100} {
		t.Run(strconv.Itoa(percent), func(t *testing.T) {
			job, err := newWindowsJob(ResourcePolicy{CPUPercent: percent, MemoryMB: 128})
			if err != nil {
				t.Fatal(err)
			}
			defer job.close()
			started := time.Now()
			child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "paced-hash", "OPENCOMPUTE_EXPECT_CPU_PERCENT": strconv.Itoa(percent)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { job.close(); child.close() }()
			nativeWorkerReady(t, job, child)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			output, err := job.communicate(ctx, child, input)
			if err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(started)
			var result WorkResult
			if err := json.Unmarshal(output, &result); err != nil || result.Result != expected {
				t.Fatalf("paced worker changed hash: %v", err)
			}
			usage, err := job.usage()
			if err != nil {
				t.Fatal(err)
			}
			allowed := uint64(elapsed.Seconds()*float64(percent)/100*1000000) + 150000
			t.Logf("paced hash: percent=%d cpu_usec=%d allowed_usec=%d wall_seconds=%.3f", percent, usage.CPUUsec, allowed, elapsed.Seconds())
			if usage.CPUUsec == 0 || usage.CPUUsec > allowed {
				t.Fatalf("paced hash exceeded CPU consent: used=%d allowed=%d", usage.CPUUsec, allowed)
			}
		})
	}
}

func TestWindowsCPUPacerRejectsInvalidSelectionAndStops(t *testing.T) {
	for _, percent := range []int{0, 9, 101} {
		if _, err := newWindowsCPUPacer(percent); err == nil {
			t.Fatal("invalid CPU selection accepted")
		}
	}
	pacer, err := newWindowsCPUPacer(10)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pacer.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("pacing ignored cancellation: %v", err)
	}
	if _, err := windowsProcessCPUUsec(0); err == nil {
		t.Fatal("invalid process accounting accepted")
	}
	pacer.debt = 5 * time.Second
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := pacer.wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("active pacing sleep ignored deadline: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("unexpected pacing cancellation latency: %v", elapsed)
	}
}

func TestWindowsCPUPacingDoesNotSpendLongIdleCreditAfterResume(t *testing.T) {
	job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "pause-busy"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { job.close(); child.close() }()
	nativeWorkerReady(t, job, child)
	resumed := make(chan error, 1)
	go func() {
		var marker [7]byte
		_, err := io.ReadFull(child.output, marker[:])
		if err == nil && string(marker[:]) != "resumed" {
			err = errors.New("invalid resume marker")
		}
		resumed <- err
	}()
	select {
	case err := <-resumed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		job.close()
		child.output.Close()
		<-resumed
		t.Fatal("worker did not resume")
	}
	baseline, err := job.usage()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := job.wait(ctx, child); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resumed worker did not stop: %v", err)
	}
	stopped := time.Now()
	usage, err := job.usage()
	if err != nil {
		t.Fatal(err)
	}
	allowed := uint64(stopped.Sub(started).Seconds()*0.25*1000000) + 150000
	if used := usage.CPUUsec - baseline.CPUUsec; used > allowed {
		t.Fatalf("resumed worker spent unbounded idle credit: used=%d allowed=%d", used, allowed)
	}
}
