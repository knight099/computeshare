//go:build linux

package main

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
)

func capabilities() (Capabilities, error) {
	if runtime.GOOS != "linux" {
		return Capabilities{}, errors.New("this release supports Linux enrollment only")
	}
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Capabilities{}, errors.New("cannot detect memory")
	}
	var memory int64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			memory, _ = strconv.ParseInt(f[1], 10, 64)
			memory /= 1024
		}
	}
	if memory < 1 {
		return Capabilities{}, errors.New("invalid memory capability")
	}
	return Capabilities{OS: runtime.GOOS, Arch: runtime.GOARCH, CPU: runtime.NumCPU(), Memory: memory, Version: "0.1.0", Runtimes: []string{}}, nil
}
func bootClock() (string, float64, error) {
	id, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return "", 0, e
	}
	data, e := os.ReadFile("/proc/uptime")
	if e != nil {
		return "", 0, e
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return "", 0, errors.New("boot clock unavailable")
	}
	up, e := strconv.ParseFloat(fields[0], 64)
	return strings.TrimSpace(string(id)), up, e
}
