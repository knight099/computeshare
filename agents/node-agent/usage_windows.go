//go:build windows

package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

// Memory is private committed bytes, not physical resident working-set bytes.
// Do not relabel this as a RAM limit or claim hostile-code isolation.
type windowsResourceUsage struct{ CPUUsec, PeakCommitBytes uint64 }

func windowsProcessCPUUsec(process windows.Handle) (uint64, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	kernelTime := uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)
	userTime := uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime)
	if userTime > ^uint64(0)-kernelTime {
		return 0, errors.New("process CPU accounting overflow")
	}
	return (kernelTime + userTime) / 10, nil
}

func (j *windowsJob) usage() (windowsResourceUsage, error) {
	var result windowsResourceUsage
	if j.handle == 0 {
		return result, errors.New("resource handle closed")
	}
	var cpu struct {
		User, Kernel, PeriodUser, PeriodKernel int64
		Faults, Total, Active, Terminated      uint32
	}
	if err := windows.QueryInformationJobObject(j.handle, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu)), nil); err != nil {
		return result, err
	}
	if cpu.User < 0 || cpu.Kernel < 0 {
		return result, errors.New("invalid kernel CPU accounting")
	}
	result.CPUUsec = (uint64(cpu.User) + uint64(cpu.Kernel)) / 10
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(j.handle, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		return result, err
	}
	result.PeakCommitBytes = uint64(limits.PeakProcessMemoryUsed)
	return result, nil
}
