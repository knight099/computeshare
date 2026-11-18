//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

var (
	errWindowsWorkerInputLimit  = errors.New("worker input exceeds 4096 bytes")
	errWindowsWorkerOutputLimit = errors.New("worker output exceeds 8192 bytes")
)

// communicate owns one exchange with an already assigned worker. The caller
// retains handle ownership and must close the child/job after this returns.
// This is deliberately separate from capability enablement and job validation.
func (j *windowsJob) communicate(parent context.Context, child *windowsChild, input []byte) ([]byte, error) {
	if j.handle == 0 || child == nil || child.process == 0 || child.input == nil || child.output == nil {
		return nil, errors.New("live owned worker and pipes required")
	}
	if err := parent.Err(); err != nil {
		return nil, errors.Join(err, j.stopProcess(child.process))
	}
	if len(input) > 4096 {
		return nil, errors.Join(errWindowsWorkerInputLimit, j.stopProcess(child.process))
	}
	// Even a caller without a deadline cannot leave an unresponsive worker
	// holding the supervisor's pipes indefinitely. Earlier lease/Stop wins.
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	type response struct {
		data []byte
		err  error
	}
	read := make(chan response, 1)
	written := make(chan error, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(child.output, 8193))
		if len(data) > 8192 {
			err = errWindowsWorkerOutputLimit
		}
		if err != nil {
			cancel()
		}
		read <- response{data, err}
	}()
	go func() {
		n, err := child.input.Write(input)
		if err == nil && n != len(input) {
			err = io.ErrShortWrite
		}
		err = errors.Join(err, child.input.Close())
		if err != nil {
			cancel()
		}
		written <- err
	}()
	code, waitErr := j.wait(ctx, child)
	if waitErr != nil {
		// wait may itself fail at a kernel boundary. Close the pipes as well as
		// terminating the job: Go cancels outstanding Windows pipe I/O on Close.
		if j.handle != 0 {
			waitErr = errors.Join(waitErr, j.stopProcess(child.process))
		}
		child.input.Close()
		child.output.Close()
	}
	var output response
	var writeErr error
	// A process exit alone does not prove pipe EOF: another handle can retain
	// an endpoint. Keep the deadline active until both operations have joined.
	done := ctx.Done()
	for read != nil || written != nil {
		select {
		case output = <-read:
			read = nil
		case writeErr = <-written:
			written = nil
		case <-done:
			waitErr = errors.Join(waitErr, ctx.Err())
			child.input.Close()
			child.output.Close()
			done = nil
		}
	}
	if err := parent.Err(); err != nil {
		return nil, errors.Join(err, waitErr)
	}
	if errors.Is(output.err, errWindowsWorkerOutputLimit) {
		return nil, errors.Join(errWindowsWorkerOutputLimit, waitErr)
	}
	if err := errors.Join(ctx.Err(), waitErr, writeErr, output.err); err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("worker exited with code %d", code)
	}
	return output.data, nil
}
