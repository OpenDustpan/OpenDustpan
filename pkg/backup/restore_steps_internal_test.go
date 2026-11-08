package backup

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// cancelLogRecorder is a slog.Handler that records every record, so the tests
// count and read the lines of logCanceled. It is safe for concurrent use.
type cancelLogRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

// Enabled accepts every level, so no log line escapes the capture.
func (r *cancelLogRecorder) Enabled(context.Context, slog.Level) bool { return true }

// Handle records rec.
func (r *cancelLogRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records = append(r.records, rec)

	return nil
}

// WithAttrs returns the same recorder: attributes arrive on the record.
func (r *cancelLogRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

// WithGroup returns the same recorder: the cancel lines hold no group.
func (r *cancelLogRecorder) WithGroup(string) slog.Handler { return r }

// recordsOf returns a copy of the records.
func (r *cancelLogRecorder) recordsOf() []slog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.records)
}

// cancelLogService returns a RestoreService whose logger records the lines.
// A call that needs any other field panics on its nil interface, so the test
// also proves that logCanceled uses nothing else.
func cancelLogService(recorder *cancelLogRecorder) *RestoreService {
	return &RestoreService{
		live: nil, operations: nil, canceler: nil, backups: nil, maintenance: nil, blobs: nil, target: nil,
		key: nil, installation: "", afterApply: nil, admit: nil, allowDev: false, devHint: "", lease: 0,
		lockWait: 0, now: nil, logger: slog.New(recorder), runner: nil,
	}
}

// cancelRecordAttrs returns the attributes of rec, and the count of them.
func cancelRecordAttrs(rec slog.Record) (map[string]string, int) {
	attrs := make(map[string]string, rec.NumAttrs())

	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()

		return true
	})

	return attrs, rec.NumAttrs()
}

// writerCheckOps answers the operation that the writer check reads.
type writerCheckOps struct {
	durable.Store

	op durable.Operation
}

// Get implements durable.Store.
func (o writerCheckOps) Get(context.Context, durable.ID) (durable.Operation, error) {
	return o.op, nil
}

// writerCheckLive stores the audit events that the writer check records. The
// embedded interface is nil: a call that needs another method panics, so the
// test also proves that the check uses nothing else.
type writerCheckLive struct {
	RestoreLive

	events []string
}

// RecordRestoreEvent implements RestoreOperations.
func (l *writerCheckLive) RecordRestoreEvent(_ context.Context, _ durable.StepFence, _ RestoreID, object string) error {
	l.events = append(l.events, object)

	return nil
}

// TestRestoreWriterCheckLogsTheAttempt proves that the check that runs before
// every cancel writes one attempt line with the restore ID, the claim token
// and the unit name, and stores the audit event that names the attempt.
func TestRestoreWriterCheckLogsTheAttempt(t *testing.T) {
	t.Parallel()

	recorder := &cancelLogRecorder{mu: sync.Mutex{}, records: nil}
	live := &writerCheckLive{RestoreLive: nil, events: nil}
	id := MintRestoreID()
	fence := durable.StepFence{Operation: "restore-op", Token: 7, Step: RestoreStepEnterMaintenance}

	service := &RestoreService{
		live: live, operations: writerCheckOps{Store: nil, op: durable.Operation{
			ID: "restore-op", Key: RestoreOperationKey(id), Kind: RestoreOperationKind,
			Version: RestoreOperationVersion, Input: nil, State: durable.StateRunning, Steps: nil, Attempt: 1,
			Failures: 0, Expiries: 0, LastError: "",
			Claim:           durable.Claim{Operation: "restore-op", Owner: "", Token: 7, ExpiresAt: time.Time{}},
			CancelRequested: false, NotBefore: time.Time{}, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
		}},
		canceler: nil, backups: nil, maintenance: nil, blobs: nil, target: nil, key: nil, installation: "",
		afterApply: nil, admit: nil, allowDev: false, devHint: "", lease: 0, lockWait: 0, now: nil,
		logger: slog.New(recorder), runner: nil,
	}

	unit := "pulumi:acme/web/prod"

	if err := service.writer(fence, id).Check(t.Context(), unit); err != nil {
		t.Fatalf("check: %v", err)
	}

	records := recorder.recordsOf()
	if len(records) != 1 {
		t.Fatalf("log lines = %d, want 1 attempt line", len(records))
	}

	rec := records[0]

	if rec.Level != slog.LevelWarn {
		t.Errorf("level = %v, want warn", rec.Level)
	}

	if rec.Message != "restore attempted to cancel the active work" {
		t.Errorf("message = %q, want the attempt message", rec.Message)
	}

	attrs, n := cancelRecordAttrs(rec)
	if n != 3 || attrs["restore_id"] != id.String() || attrs["token"] != strconv.FormatInt(fence.Token, 10) ||
		attrs["unit"] != unit {
		t.Errorf("attrs = %v, want the restore ID, the token and the unit", attrs)
	}

	want := "restore " + id.String() + " claim token " + strconv.FormatInt(fence.Token, 10) +
		" attempted to cancel the active work: " + unit
	if len(live.events) != 1 || live.events[0] != want {
		t.Errorf("audit events = %q, want [%q]", live.events, want)
	}
}

// TestRestoreCancelAuditNamesAnAttempt proves that the audit text of one
// cancel names an attempt: the writer stores the entry before the cancel
// runs, so the text must not say that the cancel completed.
func TestRestoreCancelAuditNamesAnAttempt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		token int64
		unit  string
	}{
		{name: "a stack", token: 7, unit: "stack acme/web update"},
		{name: "a workspace", token: 1, unit: "workspace infra/prod apply"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := MintRestoreID()

			got := canceledAuditObject(id, tc.token, tc.unit)

			want := "restore " + id.String() + " claim token " + strconv.FormatInt(tc.token, 10) +
				" attempted to cancel the active work: " + tc.unit
			if got != want {
				t.Fatalf("canceledAuditObject = %q, want %q", got, want)
			}

			if strings.Contains(got, "canceled the active work") {
				t.Fatalf("canceledAuditObject = %q, want no text that claims the cancel ran", got)
			}
		})
	}
}

// TestRestoreCancelLogWritesOneLinePerUnit proves that logCanceled writes one
// log line for each unit that the cancel confirmed, with the restore ID, the
// claim token and the unit name, so the log records identify each canceled
// stack or workspace.
func TestRestoreCancelLogWritesOneLinePerUnit(t *testing.T) {
	t.Parallel()

	recorder := &cancelLogRecorder{mu: sync.Mutex{}, records: nil}
	service := cancelLogService(recorder)
	in := restoreInput{
		ID: MintRestoreID(), BackupID: "bkp-1", PreBackupID: "", CheckOnly: false, Force: false,
		RequestedBy: "", KeepAccessTables: false,
	}
	fence := durable.StepFence{Operation: "restore-op", Token: 7, Step: RestoreStepEnterMaintenance}
	units := []string{"stack acme/web update", "workspace infra/prod apply", "stack acme/web refresh"}

	service.logCanceled(t.Context(), in, fence, units)

	records := recorder.recordsOf()
	if len(records) != len(units) {
		t.Fatalf("log lines = %d, want %d, one for each unit", len(records), len(units))
	}

	seen := make(map[string]int, len(units))

	for i, rec := range records {
		attrs, n := cancelRecordAttrs(rec)

		if rec.Level != slog.LevelWarn {
			t.Errorf("line %d: level = %v, want warn", i, rec.Level)
		}

		if rec.Message != "restore canceled the active work" {
			t.Errorf("line %d: message = %q, want the cancel message", i, rec.Message)
		}

		if n != 3 || attrs["restore_id"] != in.ID.String() ||
			attrs["token"] != strconv.FormatInt(fence.Token, 10) {
			t.Errorf("line %d: attrs = %v, want the restore ID, the token and one unit", i, attrs)
		}

		unit, ok := attrs["unit"]
		if !ok {
			t.Errorf("line %d: attrs = %v, want a unit name", i, attrs)

			continue
		}

		seen[unit]++
	}

	for _, unit := range units {
		if seen[unit] != 1 {
			t.Errorf("unit %q appears in %d lines, want 1", unit, seen[unit])
		}
	}
}

// TestRestoreCancelLogWithNoUnitsWritesNoLine proves the early return of
// logCanceled: with no unit it writes no line at all.
func TestRestoreCancelLogWithNoUnitsWritesNoLine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		units []string
	}{
		{name: "nil", units: nil},
		{name: "empty", units: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := &cancelLogRecorder{mu: sync.Mutex{}, records: nil}
			service := cancelLogService(recorder)

			service.logCanceled(
				t.Context(),
				restoreInput{
					ID: MintRestoreID(), BackupID: "bkp-1", PreBackupID: "", CheckOnly: false, Force: false,
					RequestedBy: "", KeepAccessTables: false,
				},
				durable.StepFence{Operation: "restore-op", Token: 7, Step: RestoreStepEnterMaintenance},
				tc.units,
			)

			if got := len(recorder.recordsOf()); got != 0 {
				t.Fatalf("log lines = %d, want none", got)
			}
		})
	}
}
