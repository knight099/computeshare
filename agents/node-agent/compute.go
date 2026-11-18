package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

type ResourcePolicy struct {
	CPUPercent int  `json:"cpu_percent"`
	MemoryMB   int  `json:"memory_mb"`
	GPU        bool `json:"gpu_enabled"`
	IdleOnly   bool `json:"idle_only"`
}

func (p ResourcePolicy) valid() bool {
	return p.CPUPercent >= 10 && p.CPUPercent <= 100 && p.MemoryMB >= 64 && p.MemoryMB <= 1024 && !p.GPU && !p.IdleOnly
}

type HashJob struct {
	Workload     string    `json:"workload_type"`
	Seed         string    `json:"seed"`
	Iterations   int       `json:"iterations"`
	AttemptID    string    `json:"attempt_id,omitempty"`
	JobID        string    `json:"job_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Epoch        int       `json:"epoch,omitempty"`
	LeaseExpires time.Time `json:"lease_expires_at,omitempty"`
	Timeout      int       `json:"timeout_seconds,omitempty"`
}

func hashWork(ctx context.Context, j HashJob) (string, error) {
	return hashWorkWithCheckpoint(ctx, j, nil)
}

// Keep one algorithm/validator; runtime-specific resource pacing may pause or
// fail at bounded checkpoints, but can never change the workload or result.
func hashWorkWithCheckpoint(ctx context.Context, j HashJob, checkpoint func(context.Context) error) (string, error) {
	if j.Workload != "synthetic-hash-v1" || j.Iterations < 1 || j.Iterations > 1000000 || len(j.Seed) < 1 || len(j.Seed) > 64 {
		return "", errors.New("unsupported synthetic workload")
	}
	for _, c := range j.Seed {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(" _.-", c)) {
			return "", errors.New("invalid public seed")
		}
	}
	data := []byte(j.Seed)
	for i := 0; i < j.Iterations; i++ {
		if i%4096 == 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			default:
			}
			if checkpoint != nil {
				if err := checkpoint(ctx); err != nil {
					return "", err
				}
			}
		}
		sum := sha256.Sum256(data)
		data = sum[:]
	}
	if checkpoint != nil {
		if err := checkpoint(ctx); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(data), nil
}
func hashWorker() error {
	var j HashJob
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&j); err != nil {
		return err
	}
	result, err := hashWork(context.Background(), j)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"result": result})
}

type WorkResult struct {
	Result      string `json:"result"`
	CPUUsec     uint64 `json:"cpu_usage_usec"`
	MemoryBytes uint64 `json:"memory_peak_bytes"`
}
