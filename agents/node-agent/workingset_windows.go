//go:build windows

package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
)

// Job memory limits cap private commit. A separate hard working-set maximum
// bounds resident memory, including shared executable/mapped pages.
// Native pressure demonstrated peak working-set accounting briefly above its
// configured maximum during faults. Reserve two 64KiB allocation granules
// within consent; the measured peak must still stay below the selected budget.
func windowsResidentLimit(budget uintptr) (uintptr, error) {
	if budget < 64*1024*1024 || budget > 1024*1024*1024 {
		return 0, errors.New("invalid resident budget")
	}
	return budget - 128*1024, nil
}

func configureWindowsWorkingSet(process windows.Handle, limit uintptr) error {
	maximumAllowed, err := windowsResidentLimit(limit)
	if process == 0 || err != nil {
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
	if err := windows.SetProcessWorkingSetSizeEx(process, minimum, maximumAllowed, flags); err != nil {
		return fmt.Errorf("enforce worker working set: %w", err)
	}
	minimum = 0
	maximum = 0
	flags = 0
	windows.GetProcessWorkingSetSizeEx(process, &minimum, &maximum, &flags)
	if maximum != maximumAllowed || flags&windows.QUOTA_LIMITS_HARDWS_MAX_ENABLE == 0 {
		return errors.New("working-set hard limit verification failed")
	}
	return nil
}
