//go:build windows

package main

import (
	"context"
	"testing"
)

func TestWindowsSafetyDecodingIsConservative(t *testing.T) {
	observation := windowsPowerObservation(windowsPowerStatus{AC: 1, BatteryFlags: 0, BatteryPercent: 75})
	if observation.AC == nil || !*observation.AC || observation.BatteryPercent == nil || *observation.BatteryPercent != 75 {
		t.Fatal("valid native power status lost")
	}
	unknown := windowsPowerObservation(windowsPowerStatus{AC: 255, BatteryFlags: 255, BatteryPercent: 255})
	if unknown.AC != nil || unknown.BatteryPercent != nil {
		t.Fatal("unknown power interpreted as safe")
	}
	if idle := windowsIdleDuration((1<<32)+1000, uint32((1<<32)-59000), true); idle == nil || *idle != 60 {
		t.Fatal("idle clock wrap failed")
	}
	if windowsIdleDuration(1000, 2000, true) != nil || windowsIdleDuration(100000, 0, false) != nil {
		t.Fatal("future/noninteractive input data accepted")
	}
	sample := platformHostObservation(context.Background())
	if sample.CPUMilliC != nil {
		t.Fatal("Windows fabricated unsupported CPU temperature")
	}
	if sample.BatteryPercent != nil && *sample.BatteryPercent > 100 {
		t.Fatal("invalid native battery reading")
	}
}
