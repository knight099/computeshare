//go:build windows

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsJobProcessHelper(t *testing.T) {
	mode := os.Getenv("OPENCOMPUTE_JOB_PROCESS_HELPER")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	if mode == "parent" {
		job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
		if err != nil {
			os.Exit(2)
		}
		child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "wait"})
		if err != nil {
			os.Exit(3)
		}
		ready := make([]byte, 5)
		if _, err = io.ReadFull(child.output, ready); err != nil || string(ready) != "ready" {
			os.Exit(6)
		}
		fmt.Println(child.pid)
		for {
			time.Sleep(time.Hour)
		}
	}
	expectedMemory := uint64(128 * 1024 * 1024)
	if value := os.Getenv("OPENCOMPUTE_EXPECT_MEMORY_MB"); value != "" {
		mb, _ := strconv.ParseUint(value, 10, 32)
		expectedMemory = mb * 1024 * 1024
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil || limits.BasicLimitInformation.ActiveProcessLimit != 1 || uint64(limits.ProcessMemoryLimit) != expectedMemory || limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		os.Exit(4)
	}
	var minimum, maximum uintptr
	var workingSetFlags uint32
	windows.GetProcessWorkingSetSizeEx(windows.CurrentProcess(), &minimum, &maximum, &workingSetFlags)
	if uint64(maximum) != expectedMemory || workingSetFlags&windows.QUOTA_LIMITS_HARDWS_MAX_ENABLE == 0 {
		os.Exit(8)
	}
	// This check executes before any workload: assignment must be atomic at start.
	if mode == "probe" {
		if os.Getenv("OPENCOMPUTE_WORKER_SECRET_FIXTURE") != "" {
			os.Exit(5)
		}
		fmt.Print("assigned-before-work")
		os.Exit(0)
	}
	fmt.Print("ready")
	if mode == "memory" {
		data := make([]byte, 256*1024*1024)
		for i := 0; i < len(data); i += 4096 {
			data[i] = 1
		}
		sum := sha256.Sum256(data)
		fmt.Printf("allocated:%d:%x", len(data), sum)
		os.Exit(0)
	}
	if mode == "busy" {
		var sum [32]byte
		for {
			sum = sha256.Sum256(sum[:])
		}
	}
	if mode == "exit" {
		var one [1]byte
		_, _ = io.ReadFull(os.Stdin, one[:])
		os.Exit(0)
	}
	if mode == "sentinel" {
		number, _ := strconv.ParseUint(os.Getenv("OPENCOMPUTE_SENTINEL_HANDLE"), 10, 64)
		_ = windows.SetEvent(windows.Handle(number))
		os.Exit(0)
	}
	for {
		time.Sleep(time.Hour)
	}
}
func TestWindowsJobAssignmentPrecedesWorkerInstructions(t *testing.T) {
	t.Setenv("OPENCOMPUTE_WORKER_SECRET_FIXTURE", "must-stay-in-supervisor")
	job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "probe"})
	if err != nil {
		t.Fatal(err)
	}
	defer child.close()
	child.input.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := job.wait(ctx, child)
	if err != nil || code != 0 {
		t.Fatalf("worker membership check failed: code=%d err=%v", code, err)
	}
	output, err := io.ReadAll(child.output)
	if err != nil || string(output) != "assigned-before-work" {
		t.Fatalf("unexpected worker output: %q %v", output, err)
	}
}
func TestWindowsJobCancellationTerminatesItsWorkerPromptly(t *testing.T) {
	job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "wait"})
	if err != nil {
		t.Fatal(err)
	}
	defer child.close()
	child.input.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err = job.wait(ctx, child)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("canceled worker did not stop promptly: %v", err)
	}
	if state, _ := windows.WaitForSingleObject(child.process, 0); state != windows.WAIT_OBJECT_0 {
		t.Fatal("worker survived cancellation")
	}
}
func TestWindowsJobKillsWorkerAfterSupervisorDeath(t *testing.T) {
	parent := exec.Command(os.Args[0], "-test.run=^TestWindowsJobProcessHelper$")
	parent.Env = append(os.Environ(), "OPENCOMPUTE_JOB_PROCESS_HELPER=parent")
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
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor did not create worker")
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(line), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if state, err := windows.WaitForSingleObject(handle, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("worker was not alive before supervisor termination")
	}
	if err = parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	status, err := windows.WaitForSingleObject(handle, 3000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatal("worker survived supervisor death")
	}
}
func TestWindowsFailedProcessCreationKeepsControllerUsable(t *testing.T) {
	job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	if child, err := job.start(context.Background(), "not-an-absolute-executable", nil, nil); err == nil {
		child.close()
		t.Fatal("accepted ambiguous executable")
	}
	if child, err := job.start(context.Background(), filepath.Join(t.TempDir(), "missing.exe"), nil, nil); err == nil {
		child.close()
		t.Fatal("missing executable unexpectedly started")
	}
	if _, _, err = job.limits(); err != nil {
		t.Fatal("failed startup corrupted the owned job handle")
	}
	getCount := kernel.NewProc("GetProcessHandleCount")
	count := func() uint32 {
		var value uint32
		ok, _, err := getCount.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&value)))
		if ok == 0 {
			t.Fatal(err)
		}
		return value
	}
	missing := filepath.Join(t.TempDir(), "missing.exe")
	before := count()
	for i := 0; i < 16; i++ {
		if child, err := job.start(context.Background(), missing, nil, nil); err == nil {
			child.close()
			t.Fatal("missing process started")
		}
	}
	if after := count(); after > before+8 {
		t.Fatalf("failed startups leaked kernel handles: before=%d after=%d", before, after)
	}

}

func TestWindowsPreCanceledStartupDoesNotCreateWorker(t *testing.T) {
	job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	child, err := job.start(ctx, os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "wait"})
	if child != nil {
		child.close()
		t.Fatal("created a worker for canceled lease")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup returned %v", err)
	}
}
func TestWindowsCancelDuringStartupLeavesNoActiveProcess(t *testing.T) {
	for i := 0; i < 8; i++ {
		job, err := newWindowsJob(ResourcePolicy{CPUPercent: 100, MemoryMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(time.Duration(i)*100*time.Microsecond, cancel)
		child, err := job.start(ctx, os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "wait"})
		cancel()
		timer.Stop()
		if child != nil {
			_, err = job.wait(ctx, child)
			child.close()
		}
		if !errors.Is(err, context.Canceled) {
			job.close()
			t.Fatalf("startup cancellation failed: %v", err)
		}
		var ids struct {
			Assigned, Count uint32
			IDs             [1]uintptr
		}
		err = windows.QueryInformationJobObject(job.handle, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&ids)), uint32(unsafe.Sizeof(ids)), nil)
		job.close()
		if err != nil || ids.Count != 0 {
			t.Fatalf("canceled startup retained a worker: %+v %v", ids, err)
		}
	}
}
func nativeWorkerReady(t *testing.T, job *windowsJob, child *windowsChild) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		ready := make([]byte, 5)
		_, err := io.ReadFull(child.output, ready)
		if err == nil && string(ready) != "ready" {
			err = errors.New("unexpected readiness signal")
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			job.close()
			child.close()
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		job.close()
		child.close()
		t.Fatal("worker readiness timed out")
	}
}
func TestWindowsCancellationWinsBeforeWorkerExit(t *testing.T) {
	for i := 0; i < 6; i++ {
		job, err := newWindowsJob(ResourcePolicy{CPUPercent: 100, MemoryMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "exit"})
		if err != nil {
			job.close()
			t.Fatal(err)
		}
		nativeWorkerReady(t, job, child)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		trigger := make(chan struct{})
		time.AfterFunc(time.Millisecond, func() { cancel(); _, _ = child.input.Write([]byte{1}); close(trigger) })
		_, err = job.wait(ctx, child)
		<-trigger
		child.close()
		job.close()
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("accepted an exit after cancellation: %v", err)
		}
	}
}
func TestWindowsUnrelatedInheritableHandleDoesNotReachWorker(t *testing.T) {
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	event, err := windows.CreateEvent(&sa, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "sentinel", "OPENCOMPUTE_SENTINEL_HANDLE": strconv.FormatUint(uint64(event), 10)})
	if err != nil {
		t.Fatal(err)
	}
	defer child.close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := job.wait(ctx, child)
	if err != nil || code != 0 {
		t.Fatalf("handle exclusion fixture failed: %d %v", code, err)
	}
	if status, err := windows.WaitForSingleObject(event, 0); err != nil || status != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("worker inherited unrelated supervisor handle")
	}
}
