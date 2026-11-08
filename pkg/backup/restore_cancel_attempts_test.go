package backup_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// The messages of the two log lines of a forced cancel. Check writes the
// attempt line before the cancel runs, so it names every attempt; logCanceled
// writes the canceled line only for the work that the cancel confirmed.
const (
	attemptCancelMessage   = "restore attempted to cancel the active work"
	confirmedCancelMessage = "restore canceled the active work"
)

// errCancelFailed is the failure of the cancel of one unit in the tests.
var errCancelFailed = errors.New("the cancel of the update failed")

// cancelLogRecords is a slog.Handler that records every record, so the tests
// read the line of each cancel attempt. It is safe for concurrent use.
type cancelLogRecords struct {
	mu      sync.Mutex
	records []slog.Record
}

// Enabled accepts every level, so no log line escapes the capture.
func (r *cancelLogRecords) Enabled(context.Context, slog.Level) bool { return true }

// Handle records rec.
func (r *cancelLogRecords) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records = append(r.records, rec)

	return nil
}

// WithAttrs returns the same recorder: attributes arrive on the record.
func (r *cancelLogRecords) WithAttrs([]slog.Attr) slog.Handler { return r }

// WithGroup returns the same recorder: the cancel lines hold no group.
func (r *cancelLogRecords) WithGroup(string) slog.Handler { return r }

// unitCounts returns the number of records with message per unit attribute.
func (r *cancelLogRecords) unitCounts(message string) map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make(map[string]int)

	for i := range r.records {
		rec := r.records[i]
		if rec.Message != message {
			continue
		}

		rec.Attrs(func(a slog.Attr) bool {
			if a.Key == "unit" {
				out[a.Value.String()]++
			}

			return true
		})
	}

	return out
}

// cancelScript is the active work of one forced enter and the outcome of
// each cancel: the units that ended on their own, and the unit whose cancel
// fails with failErr. It runs the sequence of a forced enter: check the
// claim of each unit through the writer, then cancel it.
type cancelScript struct {
	units    []string
	ended    map[string]bool
	failUnit string
	failErr  error
}

// run cancels the work through w and returns the units it canceled. Work
// that ended on its own stays out of the result. A cancel that fails stops
// the enter and names no canceled unit, as the maintenance service does.
func (s cancelScript) run(ctx context.Context, w maintenance.StateWriter) ([]string, error) {
	canceled := make([]string, 0, len(s.units))

	for _, unit := range s.units {
		if err := w.Check(ctx, unit); err != nil {
			return nil, fmt.Errorf("check the claim before the cancel of %s: %w", unit, err)
		}

		if unit == s.failUnit {
			return nil, s.failErr
		}

		if s.ended[unit] {
			continue
		}

		canceled = append(canceled, unit)
	}

	slices.Sort(canceled)

	return canceled, nil
}

// forcedRestore is a restore with force, so the enter cancels active work.
func forcedRestore() backup.RestoreRequest {
	req := plainRestore()
	req.Force = true

	return req
}

// runRestoreOnce runs the restore of the setup until it ends and returns the
// final row: one worker run takes every step, so the test needs no poll.
func runRestoreOnce(t *testing.T, setup *restoreServiceSetup, id backup.RestoreID) backup.RestoreRecord {
	t.Helper()

	setup.regs.runWorker(t.Context())

	row, err := setup.regs.GetRestore(t.Context(), id)
	if err != nil {
		t.Fatalf("read restore %s: %v", id, err)
	}

	return row
}

// TestRestoreEnterLogsEveryAttemptPastTheEventLimit proves that a forced
// enter that attempts to cancel more units than a restore holds audit events
// writes one attempt line for each unit: the log names every attempt that
// the summary event counts.
func TestRestoreEnterLogsEveryAttemptPastTheEventLimit(t *testing.T) {
	t.Parallel()

	logs := &cancelLogRecords{mu: sync.Mutex{}, records: nil}

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
		cfg.Logger = slog.New(logs)
	})

	units := make([]string, 0, backup.MaxRestoreEvents+2)
	for i := range backup.MaxRestoreEvents + 2 {
		units = append(units, "pulumi:acme/web-"+strconv.Itoa(i)+"/prod")
	}

	setup.maint.setCancelScript(&cancelScript{units: units, ended: nil, failUnit: "", failErr: nil})

	id, err := startRestoreOf(t, setup, forcedRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := runRestoreOnce(t, setup, id)
	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore = %q (%s), want succeeded", row.State, row.Error)
	}

	attempts := logs.unitCounts(attemptCancelMessage)
	if len(attempts) != len(units) {
		t.Fatalf("attempt lines = %d, want %d, one for each unit", len(attempts), len(units))
	}

	for _, unit := range units {
		if attempts[unit] != 1 {
			t.Fatalf("unit %q has %d attempt lines, want 1", unit, attempts[unit])
		}
	}

	events, err := setup.live.RestoreEvents(t.Context(), id)
	if err != nil {
		t.Fatalf("read the events: %v", err)
	}

	if len(events) != backup.MaxRestoreEvents {
		t.Fatalf("events = %d, want the limit %d", len(events), backup.MaxRestoreEvents)
	}

	omitted := len(units) - (backup.MaxRestoreEvents - 1)

	if summary := events[len(events)-1]; !strings.Contains(summary, strconv.Itoa(omitted)+" more") {
		t.Fatalf("summary event = %q, want the count %d of the omitted attempts", summary, omitted)
	}
}

// TestRestoreEnterNamesAnAttemptWhenTheWorkEndedOnItsOwn proves that work
// that ends before its cancel runs still gets the attempt line and the
// attempt audit entry, and that no line claims the restore canceled it.
func TestRestoreEnterNamesAnAttemptWhenTheWorkEndedOnItsOwn(t *testing.T) {
	t.Parallel()

	logs := &cancelLogRecords{mu: sync.Mutex{}, records: nil}

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
		cfg.Logger = slog.New(logs)
	})

	unit := "pulumi:acme/web/prod"

	setup.maint.setCancelScript(&cancelScript{
		units: []string{unit}, ended: map[string]bool{unit: true}, failUnit: "", failErr: nil,
	})

	id, err := startRestoreOf(t, setup, forcedRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := runRestoreOnce(t, setup, id)
	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore = %q (%s), want succeeded", row.State, row.Error)
	}

	events, err := setup.live.RestoreEvents(t.Context(), id)
	if err != nil {
		t.Fatalf("read the events: %v", err)
	}

	found := false

	for _, event := range events {
		if strings.Contains(event, "canceled the active work") {
			t.Fatalf("audit event %q claims that the cancel ran", event)
		}

		if strings.Contains(event, "attempted to cancel the active work") && strings.HasSuffix(event, unit) {
			found = true
		}
	}

	if !found {
		t.Fatalf("events %q hold no attempt entry for %q", events, unit)
	}

	if got := logs.unitCounts(attemptCancelMessage)[unit]; got != 1 {
		t.Fatalf("attempt lines for %q = %d, want 1", unit, got)
	}

	if got := logs.unitCounts(confirmedCancelMessage)[unit]; got != 0 {
		t.Fatalf("confirmed cancel lines for %q = %d, want none", unit, got)
	}
}

// TestRestoreEnterLogsEachAttemptBeforeAMidListCancelFailure proves that
// every attempt gets its line at the attempt: work that ends on its own, and
// a cancel that fails in the middle of the list, each name one attempt line
// even though neither is in the confirmed result.
func TestRestoreEnterLogsEachAttemptBeforeAMidListCancelFailure(t *testing.T) {
	t.Parallel()

	logs := &cancelLogRecords{mu: sync.Mutex{}, records: nil}

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
		cfg.Logger = slog.New(logs)
	})

	ended := "pulumi:acme/web-a/prod"
	failed := "pulumi:acme/web-b/prod"

	setup.maint.setCancelScript(&cancelScript{
		units:    []string{ended, failed},
		ended:    map[string]bool{ended: true},
		failUnit: failed,
		// A permanent error ends the enter after one attempt, so the test
		// reads one line for each unit.
		failErr: durable.Permanent(errCancelFailed),
	})

	id, err := startRestoreOf(t, setup, forcedRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := runRestoreOnce(t, setup, id)
	if row.State != backup.RestoreFailed {
		t.Fatalf("restore = %q (%s), want failed", row.State, row.Error)
	}

	attempts := logs.unitCounts(attemptCancelMessage)
	if len(attempts) != 2 || attempts[ended] != 1 || attempts[failed] != 1 {
		t.Fatalf("attempt lines = %v, want one for %q and one for %q", attempts, ended, failed)
	}

	if confirmed := logs.unitCounts(confirmedCancelMessage); len(confirmed) != 0 {
		t.Fatalf("confirmed cancel lines = %v, want none after a failed enter", confirmed)
	}
}
