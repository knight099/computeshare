//go:build windows

package main

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// Pacing supplements the kernel cycle cap for the fixed, trusted hash worker.
// It is not a security boundary for arbitrary code. Include all process CPU,
// even startup/GC before construction, against a conservative monotonic start.
type windowsCPUPacer struct {
	percent int
	sampled time.Time
	used    uint64
	debt    time.Duration
}

func newWindowsCPUPacer(percent int) (*windowsCPUPacer, error) {
	if percent < 10 || percent > 100 {
		return nil, errors.New("unsupported CPU-time selection")
	}
	return &windowsCPUPacer{percent: percent, sampled: time.Now()}, nil
}

func (p *windowsCPUPacer) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		used, err := windowsProcessCPUUsec(windows.CurrentProcess())
		if err != nil {
			return err
		}
		now := time.Now()
		if used < p.used {
			return errors.New("process CPU accounting moved backward")
		}
		debt, err := cpuTimeDebt(p.debt, used-p.used, now.Sub(p.sampled), p.percent)
		if err != nil {
			return err
		}
		p.used, p.sampled, p.debt = used, now, debt
		if debt <= 0 {
			return nil
		}
		timer := time.NewTimer(debt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func windowsHashWork(ctx context.Context, job HashJob, percent int) (string, error) {
	return windowsHashWorkOnHost(ctx, job, percent, windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS))
}

func windowsHashWorkOnHost(ctx context.Context, job HashJob, percent int, host uint32) (string, error) {
	pacer, err := newWindowsCPUPacer(percent)
	if err != nil {
		return "", err
	}
	return hashWorkWithCheckpoint(ctx, job, func(ctx context.Context) error {
		if !job.LeaseExpires.IsZero() && windowsLeaseExpired(job.LeaseExpires, time.Now()) {
			return context.DeadlineExceeded
		}
		if host == 0 || windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS) != host {
			return errors.New("CPU topology changed")
		}
		if err := pacer.wait(ctx); err != nil {
			return err
		}
		if !job.LeaseExpires.IsZero() && windowsLeaseExpired(job.LeaseExpires, time.Now()) {
			return context.DeadlineExceeded
		}
		if windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS) != host {
			return errors.New("CPU topology changed")
		}
		return nil
	})
}
