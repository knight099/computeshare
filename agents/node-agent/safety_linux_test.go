//go:build linux

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxSafetyReadersDistinguishUnknownAndUnsafe(t *testing.T) {
	root := t.TempDir()
	if ac, _ := readLinuxPower(root); ac != nil {
		t.Fatal("empty power directory fabricated mains")
	}
	put := func(path, value string) {
		p := filepath.Join(root, path)
		os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put("AC/type", "Mains")
	put("AC/online", "1")
	put("BAT/type", "Battery")
	put("BAT/status", "Charging")
	put("BAT/capacity", "75")
	ac, percent := readLinuxPower(root)
	if ac == nil || !*ac || percent == nil || *percent != 75 {
		t.Fatal("known AC/battery not read")
	}
	put("BAT/status", "Discharging")
	ac, _ = readLinuxPower(root)
	if ac == nil || *ac {
		t.Fatal("discharging battery accepted as safe mains")
	}
	put("BAT/capacity", "255")
	_, percent = readLinuxPower(root)
	if percent != nil {
		t.Fatal("invalid capacity accepted")
	}
	put("thermal/zone/type", "acpitz")
	put("thermal/zone/temp", "30000")
	if readLinuxCPUTemperature(filepath.Join(root, "thermal"), filepath.Join(root, "hwmon")) != nil {
		t.Fatal("ambient ACPI zone labeled CPU temperature")
	}
	put("hwmon/h0/name", "coretemp")
	put("hwmon/h0/temp1_input", "65000")
	put("hwmon/h0/temp2_input", "83000")
	temp := readLinuxCPUTemperature(filepath.Join(root, "thermal"), filepath.Join(root, "hwmon"))
	if temp == nil || *temp != 83000 {
		t.Fatal("hottest CPU sensor not selected")
	}
	put("hwmon/h0/temp2_input", "999999")
	if readLinuxCPUTemperature(filepath.Join(root, "thermal"), filepath.Join(root, "hwmon")) != nil {
		t.Fatal("bad CPU sensor silently ignored")
	}
}
func TestLinuxIdleUsesOwnLocalSessionAndMonotonicTimestamp(t *testing.T) {
	text := "Id=c1\nUser=1000\nRemote=no\nActive=yes\nType=wayland\nSeat=seat0\nIdleHint=yes\nIdleSinceHintMonotonic=1000000\n"
	idle := parseLinuxIdle(text, "c1", 1000, 61000000)
	if idle == nil || *idle != 60 {
		t.Fatal("valid idle timestamp not parsed")
	}
	if parseLinuxIdle(text, "c2", 1000, 61000000) != nil || parseLinuxIdle(text, "c1", 1001, 61000000) != nil || parseLinuxIdle(text, "c1", 1000, 999999) != nil {
		t.Fatal("foreign/future idle data accepted")
	}
}

func TestLinuxIdleRejectsInactiveAndTTY(t *testing.T) {
	base := "Id=c1\nUser=1000\nRemote=no\nActive=yes\nType=wayland\nSeat=seat0\nIdleHint=yes\nIdleSinceHintMonotonic=1000000\n"
	for _, text := range []string{strings.Replace(base, "Active=yes", "Active=no", 1), strings.Replace(base, "Type=wayland", "Type=tty", 1), strings.Replace(base, "Seat=seat0", "Seat=", 1)} {
		if parseLinuxIdle(text, "c1", 1000, 61000000) != nil {
			t.Fatal("inactive/non-graphical idle accepted")
		}
	}
}
func TestSensorOutputLimitIncludesIOCopy(t *testing.T) {
	var output safetyOutput
	source := io.LimitReader(strings.NewReader(strings.Repeat("x", 4097)), 4097)
	if _, err := io.Copy(&output, source); err == nil || output.buffer.Len() > 4096 {
		t.Fatal("sensor output limit bypassed")
	}
}
