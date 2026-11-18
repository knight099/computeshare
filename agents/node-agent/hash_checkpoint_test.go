package main

import (
	"context"
	"errors"
	"testing"
)

func TestHashCheckpointPreservesResultAndPropagatesResourceFailure(t *testing.T) {
	job := HashJob{Workload: "synthetic-hash-v1", Seed: "public", Iterations: 10000}
	expected, err := hashWork(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, err := hashWorkWithCheckpoint(context.Background(), job, func(context.Context) error { calls++; return nil })
	if err != nil || result != expected || calls < 2 {
		t.Fatalf("paced hash changed result or skipped checkpoints: calls=%d err=%v", calls, err)
	}
	failure := errors.New("resource accounting unavailable")
	result, err = hashWorkWithCheckpoint(context.Background(), job, func(context.Context) error { return failure })
	if result != "" || !errors.Is(err, failure) {
		t.Fatal("resource failure produced usable output")
	}
	job.Iterations = 1
	calls = 0
	result, err = hashWorkWithCheckpoint(context.Background(), job, func(context.Context) error {
		calls++
		if calls == 2 {
			return failure
		}
		return nil
	})
	if result != "" || !errors.Is(err, failure) {
		t.Fatal("final resource checkpoint was skipped")
	}
}
