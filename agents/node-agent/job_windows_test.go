//go:build windows

package main

import (
	"encoding/json"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"unsafe"
)

func TestWindowsCPUCapIsConservativePerCore(t *testing.T) {
	for _, cores := range []int{1, 2, 3, 8, 64, 256, 4096} {
		for _, percent := range []int{10, 25, 100} {
			rate, err := windowsCPUCap(percent, cores)
			if percent*100 < cores {
				if err == nil {
					t.Fatal("accepted an unrepresentable CPU cap")
				}
				continue
			}
			if err != nil || rate < 1 || rate > 10000 || uint64(rate)*uint64(cores) > uint64(percent*100) {
				t.Fatalf("cap exceeds selected one-core quota: percent=%d cores=%d rate=%d err=%v", percent, cores, rate, err)
			}
		}
	}
	for _, input := range [][2]int{{0, 1}, {101, 1}, {25, 0}} {
		if _, err := windowsCPUCap(input[0], input[1]); err == nil {
			t.Fatal("invalid CPU selection accepted")
		}
	}
}
func TestWindowsJobLimitsAreConfiguredBeforeAnyProcess(t *testing.T) {
	policy := ResourcePolicy{CPUPercent: 25, MemoryMB: 64}
	job, err := newWindowsJob(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	limits, cpu, err := job.limits()
	if err != nil {
		t.Fatal(err)
	}
	required := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_JOB_MEMORY)
	if limits.BasicLimitInformation.LimitFlags&required != required || limits.BasicLimitInformation.ActiveProcessLimit != 1 {
		t.Fatal("missing lifetime/process enforcement")
	}
	if limits.ProcessMemoryLimit != 64*1024*1024 || limits.JobMemoryLimit != 64*1024*1024 {
		t.Fatal("selected memory caps were not applied")
	}
	if cpu.Flags != 5 || cpu.Rate != job.cpuRate {
		t.Fatal("hard CPU cap was not applied")
	}
}
func TestWindowsJobRejectsUnenforceablePolicies(t *testing.T) {
	for _, policy := range []ResourcePolicy{{CPUPercent: 0, MemoryMB: 64}, {CPUPercent: 25, MemoryMB: 1}, {CPUPercent: 25, MemoryMB: 64, GPU: true}, {CPUPercent: 25, MemoryMB: 64, IdleOnly: true}} {
		job, err := newWindowsJob(policy)
		if err == nil {
			job.close()
			t.Fatal("unsupported policy accepted")
		}
	}
}

func TestWindowsCPUQuotaAffinityHelper(t *testing.T) {
	if os.Getenv("OPENCOMPUTE_CPU_AFFINITY_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer job.close()
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		Rate    uint32
		Host    uint32
		Process int
	}{job.cpuRate, windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS), runtime.NumCPU()})
	os.Exit(0)
}
func TestWindowsOneCoreAffinityDoesNotInflateCPUQuota(t *testing.T) {
	if windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS) < 2 {
		t.Skip("requires multiple host CPUs")
	}
	var mask, systemMask uintptr
	get := kernel.NewProc("GetProcessAffinityMask")
	set := kernel.NewProc("SetProcessAffinityMask")
	ok, _, err := get.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&mask)), uintptr(unsafe.Pointer(&systemMask)))
	if ok == 0 || mask == 0 {
		t.Fatalf("affinity fixture unavailable: %v", err)
	}
	one := mask & (^mask + 1)
	if ok, _, err = set.Call(uintptr(windows.CurrentProcess()), one); ok == 0 {
		t.Fatal(err)
	}
	defer func() {
		if ok, _, err := set.Call(uintptr(windows.CurrentProcess()), mask); ok == 0 {
			t.Errorf("failed to restore affinity: %v", err)
		}
	}()
	child := exec.Command(os.Args[0], "-test.run=^TestWindowsCPUQuotaAffinityHelper$")
	child.Env = append(os.Environ(), "OPENCOMPUTE_CPU_AFFINITY_HELPER=1")
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("affinity subprocess failed: %v %s", err, output)
	}
	var result struct {
		Rate    uint32
		Host    uint32
		Process int
	}
	if err = json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if result.Process != 1 || result.Host < 2 || uint64(result.Rate)*uint64(result.Host) > 2500 {
		t.Fatalf("process affinity inflated one-core quota: %+v", result)
	}
}

func TestWindowsJobOwnsAndClosesItsKernelHandle(t *testing.T) {
	job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 64})
	if err != nil {
		t.Fatal(err)
	}
	handle := job.handle
	if err = job.close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = job.limits(); err == nil {
		t.Fatal("queried limits through a closed resource handle")
	}
	if err = job.close(); err != nil {
		t.Fatal("double close must be harmless")
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err = queryWindowsJobInformation(handle, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), uint32(unsafe.Sizeof(limits))); err == nil {
		t.Fatal("closed Job Object kernel handle remained usable")
	}
}

func TestWindowsJobConfigurationFromFreshGoroutines(t *testing.T) {
	// Exercise small fresh stacks, not only the already-expanded test stack.
	// Native buffer addresses must stay valid through lazy syscall resolution.
	for i := 0; i < 16; i++ {
		result := make(chan error, 1)
		go func() {
			job, err := newWindowsJob(ResourcePolicy{CPUPercent: 25, MemoryMB: 128})
			if err == nil {
				_, _, err = job.limits()
				job.close()
			}
			result <- err
		}()
		if err := <-result; err != nil {
			t.Fatalf("fresh-stack kernel configuration failed: %v", err)
		}
	}
}
