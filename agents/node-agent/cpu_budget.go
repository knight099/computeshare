package main

import (
	"errors"
	"math"
	"time"
)

// cpuTimeDelay converts total process CPU time into the earliest permissible
// monotonic wall time. Round upward and reject overflow rather than run early.
func cpuTimeDelay(cpuUsec uint64, elapsed time.Duration, percent int) (time.Duration, error) {
	if percent < 10 || percent > 100 || elapsed < 0 || cpuUsec > (math.MaxInt64-99)/100000 {
		return 0, errors.New("invalid CPU-time budget")
	}
	required := time.Duration((cpuUsec*100000 + uint64(percent) - 1) / uint64(percent))
	if required <= elapsed {
		return 0, nil
	}
	return required - elapsed, nil
}

// Bound saved idle credit to one 100ms scheduling window. A long suspension or
// host scheduling delay must not fund a long CPU burst after the worker resumes.
func cpuTimeDebt(previous time.Duration, cpuDelta uint64, elapsed time.Duration, percent int) (time.Duration, error) {
	const credit = 100 * time.Millisecond
	charge, err := cpuTimeDelay(cpuDelta, 0, percent)
	if err != nil {
		return 0, err
	}
	if elapsed < 0 || previous < -credit || previous > time.Duration(math.MaxInt64)-charge {
		return 0, errors.New("invalid CPU pacing debt")
	}
	total := previous + charge
	if total <= 0 {
		if elapsed >= credit+total {
			return -credit, nil
		}
		return total - elapsed, nil
	}
	if elapsed >= total {
		if elapsed-total >= credit {
			return -credit, nil
		}
		return -(elapsed - total), nil
	}
	return total - elapsed, nil
}
