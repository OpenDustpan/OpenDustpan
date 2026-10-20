package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

var errProcTable = errors.New("process table unreadable")

// TestDevAddrLive proves the rule that says whether dev.addr names a running
// process: the ID must exist, and the process must have started no later than
// the start time in the file (plus the tolerance).
func TestDevAddrLive(t *testing.T) {
	t.Parallel()

	written := time.Date(2026, time.October, 19, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		pid   int
		start func(int) (time.Time, error)
		want  bool
	}{
		{
			name: "same process", pid: 42, want: true,
			start: func(int) (time.Time, error) { return written.Add(-time.Second), nil },
		},
		{
			name: "start within the tolerance", pid: 42, want: true,
			start: func(int) (time.Time, error) { return written.Add(devStartTolerance), nil },
		},
		{
			name: "later process with a reused ID", pid: 42, want: false,
			start: func(int) (time.Time, error) { return written.Add(devStartTolerance + time.Second), nil },
		},
		{
			name: "no process has the ID", pid: 42, want: false,
			start: func(int) (time.Time, error) { return time.Time{}, errNoProcess },
		},
		{
			name: "start time not known", pid: 42, want: true,
			start: func(int) (time.Time, error) { return time.Time{}, errStartUnknown },
		},
		{
			name: "process table fails", pid: 42, want: true,
			start: func(int) (time.Time, error) { return time.Time{}, errProcTable },
		},
		{name: "ID 0", pid: 0, want: false, start: func(int) (time.Time, error) { return written, nil }},
		{name: "negative ID", pid: -1, want: false, start: func(int) (time.Time, error) { return written, nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			addr := devAddr{Started: written, Addr: "127.0.0.1:1", Cert: "", PID: tt.pid}
			if got := devAddrLive(addr, tt.start); got != tt.want {
				t.Fatalf("devAddrLive = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestProcessStartTimeOfThisProcess proves that the process table gives a
// start time for this process that is in the past and not older than a day
// before the test binary was linked, or that the platform gives none.
func TestProcessStartTimeOfThisProcess(t *testing.T) {
	t.Parallel()

	began, err := processStartTime(os.Getpid())
	if errors.Is(err, errStartUnknown) {
		t.Skip("this platform gives no start time")
	}

	if err != nil {
		t.Fatalf("processStartTime: %v", err)
	}

	if began.After(time.Now().Add(devStartTolerance)) || began.Before(time.Now().Add(-24*time.Hour)) {
		t.Fatalf("start time %v is not within the last day", began)
	}
}

// TestReadDevAddrBoundsTheRead proves that readDevAddr reads a bounded prefix
// of the file and answers false for a file that is not one JSON object.
func TestReadDevAddrBoundsTheRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if _, ok := readDevAddr(dir); ok {
		t.Fatal("readDevAddr of a missing file = true")
	}

	huge := make([]byte, devAddrMaxBytes*2)
	for i := range huge {
		huge[i] = ' '
	}

	copy(huge, `{"pid":1}`)

	if err := os.WriteFile(dir+"/"+devAddrName, huge, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if addr, ok := readDevAddr(dir); !ok || addr.PID != 1 {
		t.Fatalf("readDevAddr = %+v, %t; want pid 1 from the first bytes", addr, ok)
	}

	if err := os.WriteFile(dir+"/"+devAddrName, []byte(`{"pid":1} {"pid":2}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}
