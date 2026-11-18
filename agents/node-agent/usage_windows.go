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

type windowsMemoryUsage struct {
	PeakResidentBytes  uint64 `json:"peak_resident_bytes"`
	PrivateCommitBytes uint64 `json:"private_commit_bytes"`
}

var processMemoryInfo = kernel.NewProc("K32GetProcessMemoryInfo")

// PROCESS_MEMORY_COUNTERS_EX exposes distinct resident and private-commit
// counters. Query the owned process; never relabel commit as physical RAM.
func windowsProcessMemory(process windows.Handle) (windowsMemoryUsage, error) {
	var counters struct {
		Size, PageFaults                                         uint32
		PeakWorkingSet, WorkingSet                               uintptr
		PeakPagedPool, PagedPool, PeakNonPagedPool, NonPagedPool uintptr
		PageFile, PeakPageFile, PrivateUsage                     uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	if err := processMemoryInfo.Find(); err != nil {
		return windowsMemoryUsage{}, err
	}
	if ok, _, err := processMemoryInfo.Call(uintptr(process), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size)); ok == 0 {
		return windowsMemoryUsage{}, err
	}
	return windowsMemoryUsage{PeakResidentBytes: uint64(counters.PeakWorkingSet), PrivateCommitBytes: uint64(counters.PrivateUsage)}, nil
}

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
	if err := queryWindowsJobInformation(j.handle, windows.JobObjectBasicAccountingInformation, unsafe.Pointer(&cpu), uint32(unsafe.Sizeof(cpu))); err != nil {
		return result, err
	}
	if cpu.User < 0 || cpu.Kernel < 0 {
		return result, errors.New("invalid kernel CPU accounting")
	}
	result.CPUUsec = (uint64(cpu.User) + uint64(cpu.Kernel)) / 10
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := queryWindowsJobInformation(j.handle, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), uint32(unsafe.Sizeof(limits))); err != nil {
		return result, err
	}
	result.PeakCommitBytes = uint64(limits.PeakProcessMemoryUsed)
	return result, nil
}
