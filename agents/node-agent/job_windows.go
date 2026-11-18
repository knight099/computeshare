//go:build windows

package main

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsCPUEnable  uint32 = 1
	windowsCPUHardCap uint32 = 4
)

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
}

func newWindowsJob(policy ResourcePolicy) (*windowsJob, error) {
	if !policy.valid() {
		return nil, errors.New("unsupported Windows resource policy")
	}
	rate, err := windowsCPUCap(policy.CPUPercent, int(windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS)))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	job := &windowsJob{handle: handle, cpuRate: rate, memoryLimit: uintptr(policy.MemoryMB) * 1024 * 1024}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	limits.BasicLimitInformation.ActiveProcessLimit = 1
	limits.ProcessMemoryLimit = uintptr(policy.MemoryMB) * 1024 * 1024
	limits.JobMemoryLimit = limits.ProcessMemoryLimit
	if _, err = windows.SetInformationJobObject(handle, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		job.close()
		return nil, err
	}
	cpu := windowsCPUControl{Flags: windowsCPUEnable | windowsCPUHardCap, Rate: rate} // ENABLE | HARD_CAP
	if _, err = windows.SetInformationJobObject(handle, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu))); err != nil {
		job.close()
		return nil, err
	}
	// Read back what the kernel accepted before returning a usable resource handle.
	actual, accepted, err := job.limits()
	if err != nil || actual.BasicLimitInformation.LimitFlags&limits.BasicLimitInformation.LimitFlags != limits.BasicLimitInformation.LimitFlags || actual.BasicLimitInformation.ActiveProcessLimit != 1 || actual.ProcessMemoryLimit != limits.ProcessMemoryLimit || actual.JobMemoryLimit != limits.JobMemoryLimit || accepted != cpu {
		job.close()
		return nil, errors.New("Windows resource limit verification failed")
	}
	return job, nil
}
func (j *windowsJob) limits() (windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION, windowsCPUControl, error) {
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	var cpu windowsCPUControl
	if j.handle == 0 {
		return limits, cpu, errors.New("resource handle closed")
	}
	if err := windows.QueryInformationJobObject(j.handle, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		return limits, cpu, err
	}
	err := windows.QueryInformationJobObject(j.handle, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu)), nil)
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
