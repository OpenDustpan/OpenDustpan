package historytest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// Situation is the state of a history item when a redaction asks for it.
// Each tool has its own table of the situations that its items can be in.
type Situation string

// The situations of a history item.
const (
	// SituationOld is an item that is finished, is not the latest state, and
	// whose blame is done. A redaction of it succeeds.
	SituationOld Situation = "old"
	// SituationLatest is the latest state item of its unit. A redaction of
	// it answers history.ErrLatest.
	SituationLatest Situation = "latest"
	// SituationReplayBase is the state item that is the base of a journal
	// that is not replayed yet. A redaction of it answers history.ErrLatest.
	SituationReplayBase Situation = "replay-base"
	// SituationRunning is an item that is not finished: an update that runs,
	// a state version that is pending, or a job log that is open. A
	// redaction of it answers history.ErrInProgress.
	SituationRunning Situation = "running"
	// SituationUpdateActive is an old item of a stack while another update
	// on the stack is active. A redaction of it answers
	// history.ErrInProgress.
	SituationUpdateActive Situation = "update-active"
	// SituationBlamePending is a state item before blame compared it and
	// the next state item. A redaction of it answers history.ErrBlamePending.
	SituationBlamePending Situation = "blame-pending"
)

// PulumiSituations returns the situations of the state items of a Pulumi
// stack: every situation.
func PulumiSituations() []Situation {
	return []Situation{
		SituationOld,
		SituationLatest,
		SituationReplayBase,
		SituationRunning,
		SituationUpdateActive,
		SituationBlamePending,
	}
}

// OpenTofuSituations returns the situations of the state items of an
// OpenTofu workspace.
func OpenTofuSituations() []Situation {
	return []Situation{SituationOld, SituationLatest, SituationRunning, SituationBlamePending}
}

// JobArtifactSituations returns the situations of a stored plan or a job
// log.
func JobArtifactSituations() []Situation {
	return []Situation{SituationOld, SituationRunning}
}

// RedactorFixture is every port that RunRedactor exercises: namespaces for
// the fence, history.Redactor under test, the redaction log and the item
// key index that the redaction writes in its transaction, and the tool
// tables through Seed and Mark.
type RedactorFixture interface {
	namespace.Store
	history.Redactor
	history.RedactionLog
	secret.ItemKeyIndex
	// Seed creates one item of unit in the situation s, with its item key
	// record, under fence, and returns its reference. unit gives the
	// namespace and the unit ID. The fixture sets its own tool and kind in
	// the reference, and the suite uses that reference.
	Seed(t *testing.T, unit history.Unit, s Situation, fence namespace.Fence) history.ItemRef
	// Mark returns the redaction ID that the tool table holds for item, or
	// an empty ID when the item is not redacted.
	Mark(ctx context.Context, item history.ItemRef) (history.RedactionID, error)
	// AuditEntries returns the stored audit entries of ns, oldest first.
	AuditEntries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error)
}

// RedactorFactory returns a fresh, empty fixture for one subtest.
type RedactorFactory func(t *testing.T) RedactorFixture

// RunRedactor runs the conformance suite for history.Redactor against the
// fixtures that newFixture returns. situations is the table of the tool,
// for example PulumiSituations(). A case whose situation is not in the
// table is skipped.
func RunRedactor(t *testing.T, situations []Situation, newFixture RedactorFactory) {
	t.Helper()

	cases := []struct {
		test      func(t *testing.T, f RedactorFixture, s Situation)
		name      string
		situation Situation
	}{
		{name: "an old item is redacted", situation: SituationOld, test: testRedactOld},
		{name: "the latest state item is ErrLatest", situation: SituationLatest, test: refuse(history.ErrLatest)},
		{name: "a replay base is ErrLatest", situation: SituationReplayBase, test: refuse(history.ErrLatest)},
		{
			name:      "an item that is not finished is ErrInProgress",
			situation: SituationRunning,
			test:      refuse(history.ErrInProgress),
		},
		{
			name:      "an old item while an update is active is ErrInProgress",
			situation: SituationUpdateActive,
			test:      refuse(history.ErrInProgress),
		},
		{
			name:      "an item before its blame is ErrBlamePending",
			situation: SituationBlamePending,
			test:      refuse(history.ErrBlamePending),
		},
		{name: "a second redaction is ErrRedacted", situation: SituationOld, test: testRedactTwice},
		{name: "a missing item is ErrItemNotFound", situation: SituationOld, test: testRedactMissing},
		{name: "a stale fence changes nothing", situation: SituationOld, test: testRedactFenced},
		{name: "a failed audit write changes nothing", situation: SituationOld, test: testRedactAuditFails},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !slices.Contains(situations, tc.situation) {
				t.Skipf("the tool has no item in the situation %q", tc.situation)
			}

			tc.test(t, newFixture(t), tc.situation)
		})
	}
}

// redactorUnit is the unit of the cases. The fixture sets its own tool.
func redactorUnit() history.Unit {
	return history.Unit{Namespace: "acme", Tool: "historytest", ID: "unit-one"}
}

// seedRedaction seeds an item in the situation s and returns it with a
// valid redaction of it that names its item key.
func seedRedaction(
	t *testing.T, f RedactorFixture, s Situation, fence namespace.Fence,
) (history.ItemRef, history.Redaction) {
	t.Helper()

	item := f.Seed(t, redactorUnit(), s, fence)

	k, err := itemKeyOf(t, f, item)
	if err != nil {
		t.Fatalf("item key of a seeded item: %v", err)
	}

	r := newRedaction(item, suiteTime())
	r.ItemKey = k.ID

	return item, r
}

// assertUnchanged checks that item has no mark, its item key is not
// shredded, it has no redaction record, and its namespace has no audit
// entry.
func assertUnchanged(t *testing.T, f RedactorFixture, item history.ItemRef) {
	t.Helper()

	if mark, err := f.Mark(t.Context(), item); err != nil || mark != "" {
		t.Errorf("mark = %q, %v, want none", mark, err)
	}

	if _, err := itemKeyOf(t, f, item); err != nil {
		t.Errorf("get item key err = %v, want the key not shredded", err)
	}

	if _, err := f.RedactionOf(t.Context(), item); !errors.Is(err, history.ErrRedactionNotFound) {
		t.Errorf("redaction of item err = %v, want ErrRedactionNotFound", err)
	}

	if entries, err := f.AuditEntries(t.Context(), item.Unit.Namespace); err != nil || len(entries) != 0 {
		t.Errorf("audit entries = %d, %v, want none", len(entries), err)
	}
}

func testRedactOld(t *testing.T, f RedactorFixture, s Situation) {
	t.Helper()
	fence := makeNamespace(t, f)
	item, r := seedRedaction(t, f, s, fence)

	stored, err := f.Redact(t.Context(), r, []string{backupTarget}, changeEntry(item), fence)
	if err != nil {
		t.Fatalf("redact: %v", err)
	}

	if stored.ID != r.ID || stored.AuditHash == "" {
		t.Errorf("stored record = %+v, want ID %s and an audit hash", stored, r.ID)
	}

	if mark, markErr := f.Mark(t.Context(), item); markErr != nil || mark != r.ID {
		t.Errorf("mark = %q, %v, want %s", mark, markErr, r.ID)
	}

	if _, keyErr := itemKeyOf(t, f, item); !errors.Is(keyErr, secret.ErrShredded) {
		t.Errorf("get item key err = %v, want ErrShredded", keyErr)
	}

	assertStoredRecord(t, f, item, stored)
	assertOneAuditEntry(t, f, item, stored.AuditHash)
}

// assertStoredRecord checks that the redaction log holds stored for item,
// with a pending live target, then the pending backup target.
func assertStoredRecord(t *testing.T, f RedactorFixture, item history.ItemRef, stored history.Redaction) {
	t.Helper()

	got, err := f.RedactionOf(t.Context(), item)
	if err != nil {
		t.Fatalf("redaction of item: %v", err)
	}

	names := make([]string, 0, len(got.Targets))
	for _, target := range got.Targets {
		names = append(names, target.Target)

		if target.State != history.ShredPending {
			t.Errorf("target %s = %s, want pending", target.Target, target.State)
		}
	}

	if want := []string{history.LiveTarget, backupTarget}; !slices.Equal(names, want) {
		t.Errorf("targets = %v, want %v", names, want)
	}

	if got.ID != stored.ID || got.ItemKey != stored.ItemKey || got.AuditHash != stored.AuditHash {
		t.Errorf("redaction of item = %+v, want the stored record %+v", got, stored)
	}
}

// assertOneAuditEntry checks that the namespace of item holds one audit
// entry, of the action unit:redact, with the hash hash.
func assertOneAuditEntry(t *testing.T, f RedactorFixture, item history.ItemRef, hash string) {
	t.Helper()

	entries, err := f.AuditEntries(t.Context(), item.Unit.Namespace)
	if err != nil {
		t.Fatalf("audit entries: %v", err)
	}

	if len(entries) != 1 || entries[0].Action != "unit:redact" || entries[0].Hash != hash {
		t.Errorf("audit entries = %+v, want one unit:redact entry with the hash %q", entries, hash)
	}
}

// refuse returns a case that redacts an item in its situation and wants
// the error want with nothing changed.
func refuse(want error) func(t *testing.T, f RedactorFixture, s Situation) {
	return func(t *testing.T, f RedactorFixture, s Situation) {
		t.Helper()
		fence := makeNamespace(t, f)
		item, r := seedRedaction(t, f, s, fence)

		if _, err := f.Redact(t.Context(), r, nil, changeEntry(item), fence); !errors.Is(err, want) {
			t.Errorf("redact err = %v, want %v", err, want)
		}

		assertUnchanged(t, f, item)
	}
}

func testRedactTwice(t *testing.T, f RedactorFixture, s Situation) {
	t.Helper()
	fence := makeNamespace(t, f)
	item, r := seedRedaction(t, f, s, fence)

	first, err := f.Redact(t.Context(), r, nil, changeEntry(item), fence)
	if err != nil {
		t.Fatalf("first redact: %v", err)
	}

	again := newRedaction(item, suiteTime())
	again.ItemKey = r.ItemKey

	got, err := f.Redact(t.Context(), again, nil, changeEntry(item), fence)
	if !errors.Is(err, history.ErrRedacted) {
		t.Fatalf("second redact err = %v, want ErrRedacted", err)
	}

	if got.ID != first.ID {
		t.Errorf("second redact answers %s, want the first record %s", got.ID, first.ID)
	}

	if mark, markErr := f.Mark(t.Context(), item); markErr != nil || mark != first.ID {
		t.Errorf("mark after a second redact = %q, %v, want %s", mark, markErr, first.ID)
	}

	if entries, auditErr := f.AuditEntries(t.Context(), item.Unit.Namespace); auditErr != nil || len(entries) != 1 {
		t.Errorf("audit entries after a second redact = %d, %v, want 1", len(entries), auditErr)
	}
}

func testRedactMissing(t *testing.T, f RedactorFixture, s Situation) {
	t.Helper()
	fence := makeNamespace(t, f)
	item, r := seedRedaction(t, f, s, fence)

	missing := item
	missing.Item = "missing-1"
	r.Item = missing

	if _, err := f.Redact(t.Context(), r, nil, changeEntry(missing), fence); !errors.Is(err, history.ErrItemNotFound) {
		t.Errorf("redact of a missing item err = %v, want ErrItemNotFound", err)
	}

	assertUnchanged(t, f, item)
}

func testRedactFenced(t *testing.T, f RedactorFixture, s Situation) {
	t.Helper()
	fence := makeNamespace(t, f)
	item, r := seedRedaction(t, f, s, fence)

	_, err := f.Redact(t.Context(), r, nil, changeEntry(item), staleFence(fence))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("redact with a stale fence err = %v, want ErrFenced", err)
	}

	assertUnchanged(t, f, item)
}

func testRedactAuditFails(t *testing.T, f RedactorFixture, s Situation) {
	t.Helper()
	fence := makeNamespace(t, f)
	item, r := seedRedaction(t, f, s, fence)

	e := changeEntry(item)
	e.Action = ""

	if _, err := f.Redact(t.Context(), r, nil, e, fence); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Errorf("redact with an invalid audit entry err = %v, want ErrInvalidEntry", err)
	}

	assertUnchanged(t, f, item)
}
