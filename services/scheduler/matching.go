package main

import (
	"sort"
	"time"
)

type Job struct {
	ID             string `json:"id"`
	CreatedAt      string `json:"created_at"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	CPUCores       int    `json:"cpu_cores"`
	MemoryMB       int    `json:"memory_mb"`
}
type Device struct {
	DeviceID         string  `json:"device_id"`
	SessionID        string  `json:"session_id"`
	Revision         int     `json:"revision"`
	RequestedAt      string  `json:"requested_at"`
	RemainingSeconds float64 `json:"remaining_seconds"`
	CPUCores         int     `json:"cpu_cores"`
	MemoryMB         int     `json:"memory_mb"`
}
type Snapshot struct {
	Jobs    []Job    `json:"jobs"`
	Devices []Device `json:"devices"`
}
type Claim struct {
	JobID           string `json:"job_id"`
	DeviceID        string `json:"device_id"`
	SessionID       string `json:"session_id"`
	RequestRevision int    `json:"request_revision"`
}

func before(a, b, aid, bid string) bool {
	at, ae := time.Parse(time.RFC3339Nano, a)
	bt, be := time.Parse(time.RFC3339Nano, b)
	if ae == nil && be == nil && !at.Equal(bt) {
		return at.Before(bt)
	}
	return aid < bid
}
func selectClaims(snapshot Snapshot) []Claim {
	jobs := append([]Job(nil), snapshot.Jobs...)
	devices := append([]Device(nil), snapshot.Devices...)
	sort.Slice(jobs, func(i, j int) bool { return before(jobs[i].CreatedAt, jobs[j].CreatedAt, jobs[i].ID, jobs[j].ID) })
	sort.Slice(devices, func(i, j int) bool {
		return before(devices[i].RequestedAt, devices[j].RequestedAt, devices[i].DeviceID, devices[j].DeviceID)
	})
	used := map[string]bool{}
	claims := []Claim{}
	for _, j := range jobs {
		for _, d := range devices {
			if !used[d.DeviceID] && j.TimeoutSeconds > 0 && j.TimeoutSeconds <= 30 && j.CPUCores > 0 && j.MemoryMB > 0 && d.CPUCores >= j.CPUCores && d.MemoryMB >= j.MemoryMB && d.RemainingSeconds > float64(j.TimeoutSeconds+5) {
				claims = append(claims, Claim{j.ID, d.DeviceID, d.SessionID, d.Revision})
				used[d.DeviceID] = true
				break
			}
		}
	}
	return claims
}
