//go:build linux

package main

import (
	"errors"
	"testing"
	"time"
)

// TestParseProcStart proves the parse of /proc/<pid>/stat: the name of the
// command may hold spaces and brackets, and the start field counts clock ticks
// since boot.
func TestParseProcStart(t *testing.T) {
	t.Parallel()

	rest := "S 1 1 1 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 5000 1000 100"
	tests := []struct {
		name string
		stat string
		want time.Time
		bad  bool
	}{
		{name: "plain name", stat: "42 (dev) " + rest, want: time.Unix(1000050, 0), bad: false},
		{name: "name with spaces and brackets", stat: "42 (a) b (c) " + rest, want: time.Unix(1000050, 0), bad: false},
		{name: "no bracket", stat: "42 dev " + rest, want: time.Time{}, bad: true},
		{name: "short line", stat: "42 (dev) S 1 1", want: time.Time{}, bad: true},
		{
			name: "start field not a number", want: time.Time{}, bad: true,
			stat: "42 (dev) S 1 1 1 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 x 1000 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseProcStart(tt.stat, "cpu  1 2 3\nbtime 1000000\nprocesses 5\n")

			if tt.bad {
				if !errors.Is(err, errStartUnknown) {
					t.Fatalf("err = %v, want errStartUnknown", err)
				}

				return
			}

			if err != nil || !got.Equal(tt.want) {
				t.Fatalf("parseProcStart = %v, %v; want %v", got, err, tt.want)
			}
		})
	}

	if _, err := parseProcStart("42 (dev) "+rest, "cpu 1\n"); !errors.Is(err, errStartUnknown) {
		t.Fatalf("no boot time: err = %v, want errStartUnknown", err)
	}
}
