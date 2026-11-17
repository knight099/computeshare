//go:build windows

package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"
)

var kernel = windows.NewLazySystemDLL("kernel32.dll")
var tickCount = kernel.NewProc("GetTickCount64")
var memoryStatus = kernel.NewProc("GlobalMemoryStatusEx")

// A process-lifetime identity is stricter than a boot identity: restarting the
// agent cannot resume consent, even during the same Windows boot/Fast Startup.
var windowsLifetimeID = randomID()

func bootClock() (string, float64, error) {
	if e := tickCount.Find(); e != nil {
		return "", 0, e
	}
	ticks, _, _ := tickCount.Call()
	return windowsLifetimeID, float64(ticks) / 1000, nil
}
func capabilities() (Capabilities, error) {
	var status struct {
		Length, Load                                                                                         uint32
		TotalPhys, AvailPhys, TotalPageFile, AvailPageFile, TotalVirtual, AvailVirtual, AvailExtendedVirtual uint64
	}
	status.Length = uint32(unsafe.Sizeof(status))
	if e := memoryStatus.Find(); e != nil {
		return Capabilities{}, e
	}
	ok, _, _ := memoryStatus.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 || status.TotalPhys < 1024*1024 {
		return Capabilities{}, errors.New("cannot detect physical memory")
	}
	return Capabilities{OS: runtime.GOOS, Arch: runtime.GOARCH, CPU: runtime.NumCPU(), Memory: int64(status.TotalPhys / (1024 * 1024)), Version: "0.1.0", Runtimes: []string{}}, nil
}
