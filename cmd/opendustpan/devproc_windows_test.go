//go:build windows

package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestProcessStartTimeCurrent proves that the start time of this process is
// in the past and not older than the test run could be.
func TestProcessStartTimeCurrent(t *testing.T) {
	t.Parallel()

	began, err := processStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("processStartTime(current) error = %v", err)
	}

	now := time.Now()
	if began.After(now) {
		t.Errorf("start time %v is after now %v", began, now)
	}

	if now.Sub(began) > time.Hour {
		t.Errorf("start time %v is more than one hour before now %v", began, now)
	}
}

// TestProcessStartTimeMissing proves that an ID that no process has gives
// errNoProcess. Windows process IDs are multiples of four, so an odd ID is
// never in use.
func TestProcessStartTimeMissing(t *testing.T) {
	t.Parallel()

	for _, pid := range []int{0, -4, 1, 3} {
		if _, err := processStartTime(pid); !errors.Is(err, errNoProcess) {
			t.Errorf("processStartTime(%d) error = %v, want errNoProcess", pid, err)
		}
	}
}
