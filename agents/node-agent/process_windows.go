//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsJobListAttribute = 0x0002000D

type windowsChild struct {
	process       windows.Handle
	pid           uint32
	input, output *os.File
}

// start is an internal kernel boundary. The production workload adapter must
// supply only the current agent executable and its fixed built-in worker mode.
// Neither executable nor arguments are accepted from company job payloads.
func (j *windowsJob) start(ctx context.Context, executable string, args []string, extraEnv map[string]string) (*windowsChild, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if j.handle == 0 || !filepath.IsAbs(executable) {
		return nil, errors.New("valid owned job and absolute executable required")
	}
	app, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return nil, err
	}
	escaped := []string{windows.EscapeArg(executable)}
	for _, arg := range args {
		escaped = append(escaped, windows.EscapeArg(arg))
	}
	command, err := windows.UTF16PtrFromString(strings.Join(escaped, " "))
	if err != nil {
		return nil, err
	}
	environment := []string{"SystemRoot=" + os.Getenv("SystemRoot"), "GOMAXPROCS=1"}
	seen := map[string]bool{"SYSTEMROOT": true, "GOMAXPROCS": true}
	for key, value := range extraEnv {
		upper := strings.ToUpper(key)
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) || seen[upper] {
			return nil, errors.New("invalid worker environment")
		}
		seen[upper] = true
		environment = append(environment, key+"="+value)
	}
	sort.Slice(environment, func(a, b int) bool { return strings.ToUpper(environment[a]) < strings.ToUpper(environment[b]) })
	env := utf16.Encode([]rune(strings.Join(environment, "\x00") + "\x00\x00"))
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err = windows.CreatePipe(&inRead, &inWrite, &sa, 4096); err != nil {
		return nil, err
	}
	defer windows.CloseHandle(inRead)
	success := false
	defer func() {
		if !success {
			windows.CloseHandle(inWrite)
		}
	}()
	if err = windows.CreatePipe(&outRead, &outWrite, &sa, 4096); err != nil {
		return nil, err
	}
	defer windows.CloseHandle(outWrite)
	defer func() {
		if !success {
			windows.CloseHandle(outRead)
		}
	}()
	if err = windows.SetHandleInformation(inWrite, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		return nil, err
	}
	if err = windows.SetHandleInformation(outRead, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		return nil, err
	}
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	jobs := []windows.Handle{j.handle}
	if err = attributes.Update(windowsJobListAttribute, unsafe.Pointer(&jobs[0]), unsafe.Sizeof(jobs[0])); err != nil {
		return nil, err
	}
	handles := []windows.Handle{inRead, outWrite}
	if err = attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return nil, err
	}
	var startup windows.StartupInfoEx
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput = inRead
	startup.StdOutput = outWrite
	startup.StdErr = outWrite
	startup.ProcThreadAttributeList = attributes.List()
	var process windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NO_WINDOW)
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = windows.CreateProcess(app, command, nil, nil, true, flags, &env[0], nil, &startup.StartupInfo, &process); err != nil {
		return nil, err
	}
	windows.CloseHandle(process.Thread)
	if canceled := ctx.Err(); canceled != nil {
		stopErr := windows.TerminateJobObject(j.handle, 1)
		if stopErr != nil {
			j.close()
		}
		state, waitErr := windows.WaitForSingleObject(process.Process, 1500)
		windows.CloseHandle(process.Process)
		if stopErr != nil {
			return nil, stopErr
		}
		if waitErr != nil {
			return nil, waitErr
		}
		if state != windows.WAIT_OBJECT_0 {
			j.close()
			return nil, errors.New("worker did not terminate during canceled startup")
		}
		return nil, canceled
	}
	success = true
	return &windowsChild{process: process.Process, pid: process.ProcessId, input: os.NewFile(uintptr(inWrite), "worker-input"), output: os.NewFile(uintptr(outRead), "worker-output")}, nil
}
func (j *windowsJob) wait(ctx context.Context, child *windowsChild) (uint32, error) {
	if j.handle == 0 || child.process == 0 {
		return 0, errors.New("worker resource handle closed")
	}
	for {
		if err := ctx.Err(); err != nil {
			if stopErr := windows.TerminateJobObject(j.handle, 1); stopErr != nil {
				return 0, stopErr
			}
			state, stopErr := windows.WaitForSingleObject(child.process, 1500)
			if stopErr != nil {
				return 0, stopErr
			}
			if state != windows.WAIT_OBJECT_0 {
				return 0, errors.New("worker did not terminate after cancellation")
			}
			return 0, err
		}
		state, err := windows.WaitForSingleObject(child.process, 25)
		if err != nil {
			return 0, err
		}
		if state == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		if state != windows.WAIT_OBJECT_0 {
			return 0, errors.New("unexpected worker wait result")
		}
		var code uint32
		err = windows.GetExitCodeProcess(child.process, &code)
		if canceled := ctx.Err(); canceled != nil {
			return 0, canceled
		}
		return code, err
	}
}
func (c *windowsChild) close() {
	if c.input != nil {
		c.input.Close()
	}
	if c.output != nil {
		c.output.Close()
	}
	if c.process != 0 {
		windows.CloseHandle(c.process)
		c.process = 0
	}
}
