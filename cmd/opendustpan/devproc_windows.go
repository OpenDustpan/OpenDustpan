//go:build windows

package main

import "time"

// processStartTime answers errStartUnknown: dev reads no process table on
// Windows yet, so the caller treats the process as live.
func processStartTime(int) (time.Time, error) {
	return time.Time{}, errStartUnknown
}
