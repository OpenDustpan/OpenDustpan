//go:build linux

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

// procClockHz is the unit of the start time in /proc/<pid>/stat. It is 100
// on every Linux platform that Go supports.
const procClockHz = 100

// processStartTime answers the time that the process pid started, from the
// process table.
func processStartTime(pid int) (time.Time, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, errNoProcess
	}

	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %w", errStartUnknown, err)
	}

	system, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %w", errStartUnknown, err)
	}

	return parseProcStart(string(stat), string(system))
}

// parseProcStart computes the start time from the text of /proc/<pid>/stat
// and of /proc/stat. The name of the command may hold spaces and brackets,
// so the fields start after its last closing bracket.
func parseProcStart(stat, system string) (time.Time, error) {
	const startField = 19 // The 22nd field of the line, counted from the third.

	_, afterCommand, found := strings.CutLast(stat, ")")
	if !found {
		return time.Time{}, fmt.Errorf("%w: the stat line has no command", errStartUnknown)
	}

	fields := strings.Fields(afterCommand)
	if len(fields) <= startField {
		return time.Time{}, fmt.Errorf("%w: the stat line is short", errStartUnknown)
	}

	ticks, err := strconv.ParseInt(fields[startField], 10, 64)
	if err != nil || ticks < 0 {
		return time.Time{}, fmt.Errorf("%w: the start field", errStartUnknown)
	}

	for line := range strings.SplitSeq(system, "\n") {
		value, found := strings.CutPrefix(line, "btime ")
		if !found {
			continue
		}

		boot, parseErr := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if parseErr != nil {
			return time.Time{}, fmt.Errorf("%w: the boot time", errStartUnknown)
		}

		return time.Unix(boot+ticks/procClockHz, 0), nil
	}

	return time.Time{}, fmt.Errorf("%w: no boot time", errStartUnknown)
}
