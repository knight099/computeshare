//go:build windows

package main

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsCPUEnable  uint32 = 1
	windowsCPUHardCap uint32 = 4
)

var (
	windowsQueryJobProc = kernel.NewProc("QueryInformationJobObject")
	windowsSetJobProc   = kernel.NewProc("SetInformationJobObject")
)

// Keep the native buffer pointer typed through Go calls. x/sys's job APIs take
// uintptr, which loses the lifetime/relocation information for a stack buffer.
// LazyProc.Call's uintptrescapes contract makes the immediate conversion safe.
func queryWindowsJobInformation(handle windows.Handle, class int32, buffer unsafe.Pointer, size uint32) error {
	if err := windowsQueryJobProc.Find(); err != nil {
		return err
	}
	if ok, _, err := windowsQueryJobProc.Call(uintptr(handle), uintptr(class), uintptr(buffer), uintptr(size), 0); ok == 0 {
		return err
	}
	return nil
}

func setWindowsJobInformation(handle windows.Handle, class uint32, buffer unsafe.Pointer, size uint32) error {
	if err := windowsSetJobProc.Find(); err != nil {
		return err
	}
	if ok, _, err := windowsSetJobProc.Call(uintptr(handle), uintptr(class), uintptr(buffer), uintptr(size)); ok == 0 {
		return err
	}
	return nil
}

// CPU rate control is a percentage of total host CPU in 1/100th-percent units.
// Consent specifies a percentage of one core; round down, never above that cap.
func windowsCPUCap(percent, cores int) (uint32, error) {
	if percent < 10 || percent > 100 || cores < 1 || percent*100 < cores {
		return 0, errors.New("one-core CPU cap cannot be enforced")
	}
	return uint32(percent * 100 / cores), nil
}

type windowsCPUControl struct{ Flags, Rate uint32 }
type windowsJob struct {
	handle      windows.Handle
	cpuRate     uint32
	memoryLimit uintptr
	hostCPUs    uint32
}

func newWindowsJob(policy ResourcePolicy) (*windowsJob, error) {
	if !policy.valid() {
		return nil, errors.New("unsupported Windows resource policy")
	}
	host := windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)
	rate, err := windowsCPUCap(policy.CPUPercent, int(host))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	job := &windowsJob{handle: handle, cpuRate: rate, memoryLimit: uintptr(policy.MemoryMB) * 1024 * 1024, hostCPUs: host}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = 1
	limits.ProcessMemoryLimit = uintptr(policy.MemoryMB) * 1024 * 1024
	limits.JobMemoryLimit = limits.ProcessMemoryLimit
	if err = setWindowsJobInformation(handle, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), uint32(unsafe.Sizeof(limits))); err != nil {
		job.close()
		return nil, err
	}
	cpu := windowsCPUControl{Flags: windowsCPUEnable | windowsCPUHardCap, Rate: rate} // ENABLE | HARD_CAP
	if err = setWindowsJobInformation(handle, windows.JobObjectCpuRateControlInformation, unsafe.Pointer(&cpu), uint32(unsafe.Sizeof(cpu))); err != nil {
		job.close()
		return nil, err
	}
	// Read back what the kernel accepted before returning a usable resource handle.
	actual, accepted, err := job.limits()
	if err != nil || actual.BasicLimitInformation.LimitFlags&limits.BasicLimitInformation.LimitFlags != limits.BasicLimitInformation.LimitFlags || actual.BasicLimitInformation.ActiveProcessLimit != 1 || actual.ProcessMemoryLimit != limits.ProcessMemoryLimit || actual.JobMemoryLimit != limits.JobMemoryLimit || accepted != cpu {
		job.close()
		return nil, fmt.Errorf("Windows resource limit verification failed: query=%v flags=%x expected=%x processes=%d process_memory=%d job_memory=%d cpu=%+v expected_cpu=%+v", err, actual.BasicLimitInformation.LimitFlags, limits.BasicLimitInformation.LimitFlags, actual.BasicLimitInformation.ActiveProcessLimit, actual.ProcessMemoryLimit, actual.JobMemoryLimit, accepted, cpu)
	}
	return job, nil
}
func (j *windowsJob) limits() (windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION, windowsCPUControl, error) {
	if j.handle == 0 {
		return windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}, windowsCPUControl{}, errors.New("resource handle closed")
	}
	return queryWindowsJobLimits(j.handle)
}

// Handle zero queries the calling worker's immediate job. Parent operations
// continue to reject closed owned handles before reaching this kernel boundary.
func queryWindowsJobLimits(handle windows.Handle) (windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION, windowsCPUControl, error) {
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	var cpu windowsCPUControl
	if err := queryWindowsJobInformation(handle, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), uint32(unsafe.Sizeof(limits))); err != nil {
		return limits, cpu, err
	}
	err := queryWindowsJobInformation(handle, windows.JobObjectCpuRateControlInformation, unsafe.Pointer(&cpu), uint32(unsafe.Sizeof(cpu)))
	return limits, cpu, err
}
func (j *windowsJob) close() error {
	if j.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(j.handle)
	j.handle = 0
	return err
}
