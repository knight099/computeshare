//go:build windows

package main

import (
	"context"
	"golang.org/x/sys/windows"
	"unsafe"
)

type windowsPowerStatus struct {
	AC, BatteryFlags, BatteryPercent, Saver uint8
	LifeSeconds, FullSeconds                uint32
}

var powerStatusProc = kernel.NewProc("GetSystemPowerStatus")
var inputInfoProc = windows.NewLazySystemDLL("user32.dll").NewProc("GetLastInputInfo")
var metricsProc = windows.NewLazySystemDLL("user32.dll").NewProc("GetSystemMetrics")
var consoleSessionProc = kernel.NewProc("WTSGetActiveConsoleSessionId")
var sessionIDProc = kernel.NewProc("ProcessIdToSessionId")

func windowsPowerObservation(status windowsPowerStatus) hostObservation {
	result := hostObservation{}
	if status.AC == 0 || status.AC == 1 {
		value := status.AC == 1
		result.AC = &value
	}
	if status.BatteryFlags != 255 && status.BatteryFlags&128 == 0 && status.BatteryPercent <= 100 {
		value := status.BatteryPercent
		result.BatteryPercent = &value
	}
	return result
}
func windowsIdleDuration(now uint64, last uint32, interactive bool) *uint64 {
	if !interactive {
		return nil
	}
	elapsed := uint32(now) - last
	if uint64(elapsed) > now || elapsed > 86400000 {
		return nil
	}
	seconds := uint64(elapsed) / 1000
	return &seconds
}
func platformHostObservation(ctx context.Context) hostObservation {
	result := hostObservation{}
	if ctx.Err() != nil {
		return result
	}
	if powerStatusProc.Find() == nil {
		var status windowsPowerStatus
		if ok, _, _ := powerStatusProc.Call(uintptr(unsafe.Pointer(&status))); ok != 0 {
			result = windowsPowerObservation(status)
		}
	}
	if ctx.Err() != nil || consoleSessionProc.Find() != nil || sessionIDProc.Find() != nil || metricsProc.Find() != nil || inputInfoProc.Find() != nil || tickCount.Find() != nil {
		return result
	}
	var session uint32
	if ok, _, _ := sessionIDProc.Call(uintptr(windows.GetCurrentProcessId()), uintptr(unsafe.Pointer(&session))); ok == 0 || session == 0 {
		return result
	}
	if active, _, _ := consoleSessionProc.Call(); uint32(active) != session {
		return result
	}
	if remote, _, _ := metricsProc.Call(0x1000); remote != 0 {
		return result
	}
	info := struct{ Size, Tick uint32 }{Size: 8}
	if ok, _, _ := inputInfoProc.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		return result
	}
	now, _, _ := tickCount.Call()
	result.IdleSeconds = windowsIdleDuration(uint64(now), info.Tick, true)
	// Windows exposes no universal reliable CPU-temperature API. Do not infer
	// CPU temperature from ACPI/ambient zones or pretend a driver is installed.
	return result
}
