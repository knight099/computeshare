//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var errWindowsWorkerBusy = errors.New("a Windows worker is already active")

// One process-wide resource slot, including startup. The mutex also serializes
// process queries with handle closure; this is physical state, not consent.
var windowsWork windowsWorkSlot

type windowsWorkSlot struct {
	sync.Mutex
	attempt string
	child   *windowsChild
}

func (w *windowsWorkSlot) running(attempt string) bool {
	w.Lock()
	defer w.Unlock()
	if attempt == "" || w.attempt != attempt || w.child == nil {
		return false
	}
	state, err := windows.WaitForSingleObject(w.child.process, 0)
	return err == nil && state == uint32(windows.WAIT_TIMEOUT)
}

func validWindowsWorkID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range []byte(id) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validateWindowsAssignment(job HashJob, policy ResourcePolicy) error {
	return validateWindowsAssignmentAt(job, policy, time.Now())
}

func windowsLeaseExpired(lease, now time.Time) bool {
	return !now.UTC().Before(lease.UTC())
}

func validateWindowsAssignmentAt(job HashJob, policy ResourcePolicy, now time.Time) error {
	if !policy.valid() || !validWindowsWorkID(job.AttemptID) || !validWindowsWorkID(job.JobID) || !validWindowsWorkID(job.SessionID) || job.Epoch < 1 || job.Timeout < 1 || job.Timeout > 30 || windowsLeaseExpired(job.LeaseExpires, now) {
		return errors.New("invalid Windows workload assignment")
	}
	return validateHashWork(job)
}

func windowsWorkerEnvironment(policy ResourcePolicy, host uint32) map[string]string {
	encoded, _ := json.Marshal(policy)
	return map[string]string{"OPENCOMPUTE_WORKER_POLICY": string(encoded), "OPENCOMPUTE_WORKER_HOST_CPUS": strconv.FormatUint(uint64(host), 10), "GOMEMLIMIT": strconv.Itoa(policy.MemoryMB-16) + "MiB"}
}

func decodeWindowsWorkerValue(data []byte, value interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("worker message must contain exactly one JSON value")
	}
	return nil
}

// runWindowsHash is the bounded adapter behind the unavailable capability gate.
// Only the current executable and a fixed internal entrypoint can be launched.
func runWindowsHash(parent context.Context, work HashJob, policy ResourcePolicy) (WorkResult, error) {
	return runWindowsHashWithClock(parent, work, policy, time.Now)
}

// Wall time is injected at this boundary to test absolute lease corrections
// without changing the host clock. Deadlines still use independent Go timers.
func runWindowsHashWithClock(parent context.Context, work HashJob, policy ResourcePolicy, wallNow func() time.Time) (WorkResult, error) {
	if err := parent.Err(); err != nil {
		return WorkResult{}, err
	}
	if err := validateWindowsAssignmentAt(work, policy, wallNow()); err != nil {
		return WorkResult{}, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(work.Timeout)*time.Second)
	defer cancel()
	ctx, leaseCancel := context.WithDeadline(ctx, work.LeaseExpires)
	defer leaseCancel()
	input, err := json.Marshal(work)
	if err != nil || len(input) > 4096 {
		return WorkResult{}, errWindowsWorkerInputLimit
	}
	windowsWork.Lock()
	if windowsWork.attempt != "" {
		windowsWork.Unlock()
		return WorkResult{}, errWindowsWorkerBusy
	}
	windowsWork.attempt = work.AttemptID
	windowsWork.Unlock()
	defer func() {
		windowsWork.Lock()
		if windowsWork.child != nil {
			windowsWork.child.close()
		}
		windowsWork.child = nil
		windowsWork.attempt = ""
		windowsWork.Unlock()
	}()
	job, err := newWindowsJob(policy)
	if err != nil {
		return WorkResult{}, err
	}
	defer job.close()
	executable, err := os.Executable()
	if err != nil {
		return WorkResult{}, err
	}
	started := time.Now()
	child, err := job.start(ctx, executable, []string{"internal-hash-worker"}, windowsWorkerEnvironment(policy, job.hostCPUs))
	if err != nil {
		return WorkResult{}, err
	}
	windowsWork.Lock()
	windowsWork.child = child
	windowsWork.Unlock()
	output, err := job.communicate(ctx, child, input)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return WorkResult{}, cause
		}
		if windowsLeaseExpired(work.LeaseExpires, wallNow()) {
			return WorkResult{}, context.DeadlineExceeded
		}
		return WorkResult{}, err
	}
	stopped := time.Now()
	if windowsLeaseExpired(work.LeaseExpires, wallNow()) {
		return WorkResult{}, context.DeadlineExceeded
	}
	if err := ctx.Err(); err != nil {
		return WorkResult{}, err
	}
	if windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS) != job.hostCPUs {
		return WorkResult{}, errors.New("CPU topology changed")
	}
	var result WorkResult
	if err := decodeWindowsWorkerValue(output, &result); err != nil {
		return WorkResult{}, err
	}
	if decoded, err := hex.DecodeString(result.Result); err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != result.Result {
		return WorkResult{}, errors.New("invalid worker result")
	}
	usage, err := job.usage()
	if err != nil {
		return WorkResult{}, err
	}
	memory, err := windowsProcessMemory(child.process)
	if err != nil {
		return WorkResult{}, err
	}
	limit := uint64(policy.MemoryMB) * 1024 * 1024
	allowedCPU := uint64(stopped.Sub(started).Seconds()*float64(policy.CPUPercent)/100*1000000) + 150000
	if memory.PeakResidentBytes == 0 || memory.PeakResidentBytes > limit || usage.PeakCommitBytes > limit || usage.CPUUsec > allowedCPU {
		return WorkResult{}, errors.New("worker exceeded selected resource bounds")
	}
	// Ignore child-supplied usage. The owned kernel handles provide endpoint
	// CPU and resident peak; API records still classify host telemetry untrusted.
	result.CPUUsec = usage.CPUUsec
	result.MemoryBytes = memory.PeakResidentBytes
	if err := ctx.Err(); err != nil {
		return WorkResult{}, err
	}
	if windowsLeaseExpired(work.LeaseExpires, wallNow()) {
		return WorkResult{}, context.DeadlineExceeded
	}
	return result, nil
}
