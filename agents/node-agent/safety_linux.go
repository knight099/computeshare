//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func safetyText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 257))
	if err != nil || len(data) > 256 {
		return "", errors.New("sensor text unavailable")
	}
	return strings.TrimSpace(string(data)), nil
}
func readLinuxPower(root string) (*bool, *uint8) {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) > 32 {
		return nil, nil
	}
	online, offline, uncertain, discharging := false, false, false, false
	batteries := 0
	capacityKnown := true
	minimum := uint8(100)
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name())
		kind, err := safetyText(filepath.Join(dir, "type"))
		if err != nil {
			uncertain = true
			continue
		}
		if kind == "Mains" || strings.HasPrefix(kind, "USB") || kind == "Wireless" {
			value, err := safetyText(filepath.Join(dir, "online"))
			if err != nil || value != "0" && value != "1" {
				uncertain = true
			} else if value == "1" {
				online = true
			} else {
				offline = true
			}
		}
		if kind == "Battery" {
			batteries++
			status, _ := safetyText(filepath.Join(dir, "status"))
			if status == "Discharging" {
				discharging = true
			}
			value, err := safetyText(filepath.Join(dir, "capacity"))
			n, e := strconv.Atoi(value)
			if err != nil || e != nil || n < 0 || n > 100 {
				capacityKnown = false
			} else if uint8(n) < minimum {
				minimum = uint8(n)
			}
		}
	}
	var ac *bool
	if discharging {
		value := false
		ac = &value
	} else if online {
		value := true
		ac = &value
	} else if offline && !uncertain {
		value := false
		ac = &value
	}
	var battery *uint8
	if batteries > 0 && capacityKnown {
		battery = &minimum
	}
	return ac, battery
}
func readLinuxCPUTemperature(thermalRoot, hwmonRoot string) *int {
	hottest := -1
	invalid := false
	read := func(path string) {
		text, err := safetyText(path)
		n, e := strconv.Atoi(text)
		if err != nil || e != nil || n < 0 || n > 125000 {
			invalid = true
			return
		}
		if n > hottest {
			hottest = n
		}
	}
	zones, err := os.ReadDir(thermalRoot)
	if err == nil {
		if len(zones) > 64 {
			return nil
		}
		for _, zone := range zones {
			dir := filepath.Join(thermalRoot, zone.Name())
			kind, _ := safetyText(filepath.Join(dir, "type"))
			if kind == "x86_pkg_temp" || kind == "cpu-thermal" || kind == "cpu_thermal" {
				read(filepath.Join(dir, "temp"))
			}
		}
	}
	devices, err := os.ReadDir(hwmonRoot)
	if err == nil {
		if len(devices) > 32 {
			return nil
		}
		for _, device := range devices {
			dir := filepath.Join(hwmonRoot, device.Name())
			driver, _ := safetyText(filepath.Join(dir, "name"))
			if driver != "coretemp" && driver != "k10temp" && driver != "zenpower" && driver != "cpu_thermal" {
				continue
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) > 512 {
				return nil
			}
			count := 0
			for _, file := range files {
				name := file.Name()
				if strings.HasPrefix(name, "temp") && strings.HasSuffix(name, "_input") {
					index := strings.TrimSuffix(strings.TrimPrefix(name, "temp"), "_input")
					if _, err := strconv.ParseUint(index, 10, 16); err != nil {
						continue
					}
					count++
					if count > 128 {
						return nil
					}
					read(filepath.Join(dir, name))
				}
			}
			if count == 0 {
				invalid = true
			}
		}
	}
	if invalid || hottest < 0 {
		return nil
	}
	return &hottest
}
func parseLinuxIdle(text, session string, uid int, nowMicro uint64) *uint64 {
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			if _, duplicate := values[key]; duplicate {
				return nil
			}
			values[key] = value
		}
	}
	if values["Id"] == "" || (session != "self" && values["Id"] != session) || values["User"] != strconv.Itoa(uid) || values["Remote"] != "no" || values["Active"] != "yes" || (values["Type"] != "x11" && values["Type"] != "wayland") || values["Seat"] == "" {
		return nil
	}
	if values["IdleHint"] == "no" {
		value := uint64(0)
		return &value
	}
	if values["IdleHint"] != "yes" {
		return nil
	}
	since, err := strconv.ParseUint(values["IdleSinceHintMonotonic"], 10, 64)
	if err != nil || since == 0 || since > nowMicro {
		return nil
	}
	seconds := (nowMicro - since) / 1000000
	if seconds > 86400 {
		return nil
	}
	return &seconds
}

type safetyOutput struct{ buffer bytes.Buffer }

func (b *safetyOutput) Write(data []byte) (int, error) {
	if b.buffer.Len()+len(data) > 4096 {
		return 0, errors.New("sensor response too large")
	}
	return b.buffer.Write(data)
}
func readLinuxIdle(ctx context.Context) *uint64 {
	// Resolve the caller's real logind session, never an environment-selected VT.
	session := "self"
	c, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(c, "/usr/bin/loginctl", "show-session", session, "--no-pager", "--property=Id,User,Remote,Active,Type,Seat,IdleHint,IdleSinceHintMonotonic")
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	var output safetyOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	command.WaitDelay = 100 * time.Millisecond
	if command.Run() != nil {
		return nil
	}
	var now unix.Timespec
	if unix.ClockGettime(unix.CLOCK_MONOTONIC, &now) != nil {
		return nil
	}
	return parseLinuxIdle(output.buffer.String(), session, os.Geteuid(), uint64(now.Sec)*1000000+uint64(now.Nsec)/1000)
}
func platformHostObservation(ctx context.Context) hostObservation {
	ac, battery := readLinuxPower("/sys/class/power_supply")
	if ctx.Err() != nil {
		return hostObservation{}
	}
	temperature := readLinuxCPUTemperature("/sys/class/thermal", "/sys/class/hwmon")
	if ctx.Err() != nil {
		return hostObservation{}
	}
	return hostObservation{AC: ac, BatteryPercent: battery, CPUMilliC: temperature, IdleSeconds: readLinuxIdle(ctx)}
}
