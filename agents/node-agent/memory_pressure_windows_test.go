//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsSharedPressureBytes = 256 * 1024 * 1024

// The view is native-owned memory, not a Go allocation. Use the documented
// SliceHeader-on-an-existing-slice interop pattern; retain the mapping/view
// until all accesses finish. No unchecked uintptr-to-pointer conversion.
func mappedWindowsPressureBytes(view uintptr) (data []byte) {
	header := (*reflect.SliceHeader)(unsafe.Pointer(&data))
	header.Data = view
	header.Len = windowsSharedPressureBytes
	header.Cap = windowsSharedPressureBytes
	return data
}

func TestWindowsResidentCapUnderSharedMappingPressure(t *testing.T) {
	if _, err := windowsProcessMemory(0); err == nil {
		t.Fatal("invalid memory-accounting handle accepted")
	}
	mapping, err := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, windowsSharedPressureBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(mapping)
	view, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_WRITE, 0, 0, windowsSharedPressureBytes)
	if err != nil {
		t.Fatal(err)
	}
	data := mappedWindowsPressureBytes(view)
	for i := 0; i < len(data); i += 4096 {
		data[i] = 1
	}
	if err := windows.UnmapViewOfFile(view); err != nil {
		t.Fatal(err)
	}
	for _, memoryMB := range []int{512, 128} {
		t.Run(fmt.Sprint(memoryMB), func(t *testing.T) {
			job, err := newWindowsJob(ResourcePolicy{CPUPercent: 100, MemoryMB: memoryMB})
			if err != nil {
				t.Fatal(err)
			}
			defer job.close()
			child, err := job.start(context.Background(), os.Args[0], []string{"-test.run=^TestWindowsJobProcessHelper$"}, map[string]string{"OPENCOMPUTE_JOB_PROCESS_HELPER": "shared-resident", "OPENCOMPUTE_EXPECT_MEMORY_MB": fmt.Sprint(memoryMB)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { job.close(); child.close() }()
			nativeWorkerReady(t, job, child)
			var remote windows.Handle
			if err := windows.DuplicateHandle(windows.CurrentProcess(), mapping, child.process, &remote, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			output, err := job.communicate(ctx, child, fmt.Appendf(nil, "%d\n", remote))
			if err != nil {
				t.Fatal(err)
			}
			var reported windowsMemoryUsage
			if err := json.Unmarshal(output, &reported); err != nil {
				t.Fatal(err)
			}
			measured, err := windowsProcessMemory(child.process)
			if err != nil {
				t.Fatal(err)
			}
			jobUsage, err := job.usage()
			if err != nil {
				t.Fatal(err)
			}
			limit := uint64(memoryMB) * 1024 * 1024
			t.Logf("shared memory pressure: limit=%d reported_peak_resident=%d post_exit_peak_resident=%d private_commit=%d", limit, reported.PeakResidentBytes, measured.PeakResidentBytes, reported.PrivateCommitBytes)
			if measured.PeakResidentBytes < reported.PeakResidentBytes || measured.PeakResidentBytes > limit || measured.PeakResidentBytes == 0 {
				t.Fatal("resident memory exceeded cap or lost peak accounting")
			}
			if memoryMB == 512 && reported.PeakResidentBytes < 192*1024*1024 {
				t.Fatal("control did not generate sufficient resident pressure")
			}
			if reported.PrivateCommitBytes >= 128*1024*1024 || jobUsage.PeakCommitBytes >= 128*1024*1024 {
				t.Fatal("fixture tested private commit instead of shared resident pressure")
			}
		})
	}
}
