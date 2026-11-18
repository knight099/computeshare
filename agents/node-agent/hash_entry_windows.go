//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/sys/windows"
)

func verifyWindowsWorkerLimits(policy ResourcePolicy, host uint32) error {
	if !policy.valid() || runtime.GOARCH != "amd64" || runtime.GOMAXPROCS(0) != 1 || host == 0 || windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS) != host {
		return errors.New("invalid worker resource context")
	}
	limits, cpu, err := queryWindowsJobLimits(0)
	if err != nil {
		return err
	}
	rate, err := windowsCPUCap(policy.CPUPercent, int(host))
	if err != nil {
		return err
	}
	flags := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY | windows.JOB_OBJECT_LIMIT_JOB_MEMORY)
	budget := uintptr(policy.MemoryMB) * 1024 * 1024
	if limits.BasicLimitInformation.LimitFlags&flags != flags || limits.BasicLimitInformation.ActiveProcessLimit != 1 || limits.ProcessMemoryLimit != budget || limits.JobMemoryLimit != budget || cpu.Flags != windowsCPUEnable|windowsCPUHardCap || cpu.Rate != rate {
		return errors.New("worker kernel limits do not match selected policy")
	}
	var minimum, maximum uintptr
	var workingSetFlags uint32
	windows.GetProcessWorkingSetSizeEx(windows.CurrentProcess(), &minimum, &maximum, &workingSetFlags)
	residentLimit, err := windowsResidentLimit(budget)
	if err != nil {
		return err
	}
	if maximum != residentLimit || workingSetFlags&windows.QUOTA_LIMITS_HARDWS_MAX_ENABLE == 0 {
		return errors.New("worker resident limit missing")
	}
	return nil
}

func windowsHashWorker() error {
	policyData := os.Getenv("OPENCOMPUTE_WORKER_POLICY")
	if len(policyData) == 0 || len(policyData) > 1024 {
		return errors.New("missing worker resource context")
	}
	var policy ResourcePolicy
	if err := decodeWindowsWorkerValue([]byte(policyData), &policy); err != nil {
		return err
	}
	host, err := strconv.ParseUint(os.Getenv("OPENCOMPUTE_WORKER_HOST_CPUS"), 10, 32)
	if err != nil {
		return err
	}
	if err := verifyWindowsWorkerLimits(policy, uint32(host)); err != nil {
		return err
	}
	for _, pipe := range []*os.File{os.Stdin, os.Stdout} {
		kind, err := windows.GetFileType(windows.Handle(pipe.Fd()))
		if err != nil || kind != windows.FILE_TYPE_PIPE {
			return errors.New("worker requires bounded parent pipes")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type message struct {
		data []byte
		err  error
	}
	input := make(chan message, 1)
	go func() { data, err := io.ReadAll(io.LimitReader(os.Stdin, 4097)); input <- message{data, err} }()
	var data []byte
	select {
	case result := <-input:
		if result.err != nil {
			return result.err
		}
		data = result.data
	case <-ctx.Done():
		os.Stdin.Close()
		<-input
		return ctx.Err()
	}
	if len(data) > 4096 {
		return errWindowsWorkerInputLimit
	}
	var job HashJob
	if err := decodeWindowsWorkerValue(data, &job); err != nil {
		return err
	}
	if err := validateWindowsAssignment(job, policy); err != nil {
		return err
	}
	ctx, timeoutCancel := context.WithTimeout(ctx, time.Duration(job.Timeout)*time.Second)
	defer timeoutCancel()
	ctx, leaseCancel := context.WithDeadline(ctx, job.LeaseExpires)
	defer leaseCancel()
	result, err := windowsHashWorkOnHost(ctx, job, policy.CPUPercent, uint32(host))
	if err != nil {
		return err
	}
	if err := verifyWindowsWorkerLimits(policy, uint32(host)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(WorkResult{Result: result})
}
