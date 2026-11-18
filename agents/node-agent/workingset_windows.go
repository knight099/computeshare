//go:build windows

package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
)

// Job memory limits cap private commit. A separate hard working-set maximum
// bounds resident memory, including shared executable/mapped pages.
func configureWindowsWorkingSet(process windows.Handle, limit uintptr) error {
	if process == 0 || limit < 64*1024*1024 || limit > 1024*1024*1024 {
		return errors.New("invalid working-set process or budget")
	}
	var minimum, maximum uintptr
	var flags uint32
	windows.GetProcessWorkingSetSizeEx(process, &minimum, &maximum, &flags)
	if minimum == 0 || maximum == 0 {
		return errors.New("cannot inspect working-set limits")
	}
	if minimum > 1024*1024 {
		minimum = 1024 * 1024
	}
	flags = windows.QUOTA_LIMITS_HARDWS_MIN_DISABLE | windows.QUOTA_LIMITS_HARDWS_MAX_ENABLE
	if err := windows.SetProcessWorkingSetSizeEx(process, minimum, limit, flags); err != nil {
		return fmt.Errorf("enforce worker working set: %w", err)
	}
	minimum = 0
	maximum = 0
	flags = 0
	windows.GetProcessWorkingSetSizeEx(process, &minimum, &maximum, &flags)
	if maximum != limit || flags&windows.QUOTA_LIMITS_HARDWS_MAX_ENABLE == 0 {
		return errors.New("working-set hard limit verification failed")
	}
	return nil
}
