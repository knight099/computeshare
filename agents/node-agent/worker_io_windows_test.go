//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsWorkerCommunicationIsBoundedAndReapsOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name, mode                                string
		input                                     []byte
		wantSize                                  int
		wantError                                 error
		cancelBefore, brokenInput, retainedOutput bool
	}{
		{name: "echo", mode: "echo", input: bytes.Repeat([]byte("a"), 4096), wantSize: 4096},
		{name: "empty", mode: "echo"},
		{name: "exact output limit", mode: "output-boundary", wantSize: 8192},
		{name: "oversized input", mode: "wait", input: make([]byte, 4097), wantError: errWindowsWorkerInputLimit},
		{name: "oversized output", mode: "output-overflow", wantError: errWindowsWorkerOutputLimit},
		{name: "partial output timeout", mode: "partial-output", wantError: context.DeadlineExceeded},
		{name: "canceled", mode: "echo", wantError: context.Canceled, cancelBefore: true},
		{name: "broken input", mode: "wait", input: []byte("input"), brokenInput: true},
		{name: "worker closed input", mode: "closed-input", input: []byte("input")},
		{name: "retained output after exit", mode: "echo", retainedOutput: true, wantError: context.DeadlineExceeded},
		{name: "failed process output", mode: "failed-output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, err := newWindowsJob(ResourcePolicy{CPUPercent: 100, MemoryMB: 128})
			if err != nil {
				t.Fatal(err)
			}
			defer job.close()
			child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": tc.mode})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { job.close(); child.close() }()
			nativeWorkerReady(t, job, child)
			if tc.retainedOutput {
				// Model a retained writer without granting the worker extra
				// handles or loosening its active-process limit.
				child.output.Close()
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Close()
				child.output = reader
				if _, err := io.WriteString(writer, "partial"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.brokenInput {
				child.input.Close()
			}
			duration := 3 * time.Second
			if tc.mode == "partial-output" || tc.retainedOutput {
				duration = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			started := time.Now()
			output, err := job.communicate(ctx, child, tc.input)
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("wanted %v, got %v", tc.wantError, err)
				}
			} else if tc.brokenInput || tc.mode == "closed-input" || tc.mode == "failed-output" {
				if err == nil {
					t.Fatal("failed pipe/process returned success")
				}
			} else if err != nil || len(output) != tc.wantSize {
				t.Fatalf("unexpected response: length=%d err=%v", len(output), err)
			}
			if tc.mode == "echo" && err == nil && !bytes.Equal(output, tc.input) {
				t.Fatal("pipe exchange changed input")
			}
			if err != nil && len(output) != 0 {
				t.Fatal("failed exchange returned partial output")
			}
			if time.Since(started) > 2*time.Second {
				t.Fatal("worker communication did not stop promptly")
			}
			if state, err := windows.WaitForSingleObject(child.process, 0); err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatalf("exchange left a live worker: state=%d err=%v", state, err)
			}
		})
	}
}
