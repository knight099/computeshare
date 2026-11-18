//go:build linux

package main

import (
	"context"
	"sync/atomic"
)

type protocolAcceptanceRuntime struct {
	platformExecution
	active atomic.Bool
}

func (*protocolAcceptanceRuntime) available(string) bool { return true }
func (r *protocolAcceptanceRuntime) execute(ctx context.Context, _ string, j HashJob, _ ResourcePolicy) (WorkResult, error) {
	r.active.Store(true)
	defer r.active.Store(false)
	if j.Iterations == 1000000 {
		<-ctx.Done()
		return WorkResult{}, ctx.Err()
	}
	result, err := hashWork(ctx, j)
	return WorkResult{Result: result}, err
}

func (r *protocolAcceptanceRuntime) running(string, string) bool { return r.active.Load() }
