package historytest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// RedactionStore is every persistence port that RunRedactions exercises:
// namespaces for the fence, and history.RedactionLog under test.
type RedactionStore interface {
	namespace.Store
	history.RedactionLog
}

// RedactionFactory returns a fresh, empty store for one subtest.
type RedactionFactory func(t *testing.T) RedactionStore

// RunRedactions runs the conformance suite for history.RedactionLog.
func RunRedactions(t *testing.T, newStore RedactionFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s RedactionStore)
		name string
	}{
		{name: "append then get by ID and by item", test: testAppendGet},
		{name: "append makes a pending live target first, then each backup target", test: testAppendTargets},
		{name: "a second redaction of one item is ErrRedacted", test: testAppendTwice},
		{name: "an invalid record is refused", test: testAppendInvalid},
		{name: "get of a missing record is ErrRedactionNotFound", test: testGetMissing},
		{name: "list answers the records of one unit newest first", test: testListNewestFirst},
		{name: "pending shreds stay listed until every target is done", test: testPendingUntilDone},
		{name: "finish of a done target keeps the first time", test: testFinishTwice},
		{name: "a failure counts the try and keeps the target pending", test: testNoteFailure},
		{name: "an unknown target is ErrTargetNotFound", test: testUnknownTarget},
		{name: "a stale fence refuses every write", test: testRedactionFenced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// stateItem returns the reference of a state item of the unit unitID in
// the namespace "acme".
func stateItem(unitID, item string) history.ItemRef {
	return history.ItemRef{Unit: unitOf(unitID), Kind: secret.ContentKindState, Item: item}
}

// mustAppend appends r with targets and returns the stored record.
func mustAppend(
	t *testing.T, s RedactionStore, r history.Redaction, targets []string, fence namespace.Fence,
) history.Redaction {
	t.Helper()

	got, err := s.AppendRedaction(t.Context(), r, targets, fence)
	if err != nil {
		t.Fatalf("append redaction %s: %v", r.ID, err)
	}

	return got
}

func testAppendGet(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	r := newRedaction(stateItem("ws-one", "sv-1"), suiteTime())
	mustAppend(t, s, r, nil, fence)

	byID, err := s.GetRedaction(t.Context(), "acme", r.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if byID.ID != r.ID || byID.Reason != r.Reason || byID.Item != r.Item || byID.ContentKey != r.ContentKey ||
		!byID.At.Equal(r.At) || byID.By != r.By || byID.Cause != r.Cause {
		t.Errorf("get = %+v, want %+v", byID, r)
	}

	byItem, err := s.RedactionOf(t.Context(), r.Item)
	if err != nil || byItem.ID != r.ID {
		t.Errorf("redaction of item = %s, %v, want %s", byItem.ID, err, r.ID)
	}

	_, err = s.RedactionOf(
		t.Context(),
		stateItem("ws-one", "sv-2"),
	)
	if !errors.Is(
		err,
		history.ErrRedactionNotFound,
	) {
		t.Errorf("redaction of another item err = %v, want ErrRedactionNotFound", err)
	}
}

func testAppendTargets(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	r := newRedaction(stateItem("ws-one", "sv-1"), suiteTime())

	got := mustAppend(t, s, r, []string{backupTarget, "s3://bucket/prefix"}, fence)

	names := make([]string, 0, len(got.Targets))

	for _, target := range got.Targets {
		names = append(names, target.Target)

		if target.State != history.ShredPending || target.Attempts != 0 {
			t.Errorf("target %s = %+v, want pending with no try", target.Target, target)
		}
	}

	if want := []string{history.LiveTarget, backupTarget, "s3://bucket/prefix"}; !slices.Equal(names, want) {
		t.Errorf("targets = %v, want %v", names, want)
	}

	if !got.Pending() {
		t.Error("a new redaction is not pending")
	}
}

func testAppendTwice(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	item := stateItem("ws-one", "sv-1")
	first := mustAppend(t, s, newRedaction(item, suiteTime()), nil, fence)

	got, err := s.AppendRedaction(t.Context(), newRedaction(item, suiteTime().Add(time.Minute)), nil, fence)
	if !errors.Is(err, history.ErrRedacted) {
		t.Fatalf("second redaction err = %v, want ErrRedacted", err)
	}

	if got.ID != first.ID {
		t.Errorf("second redaction answers %s, want the first %s", got.ID, first.ID)
	}
}

func testAppendInvalid(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)

	cases := []struct {
		edit func(r *history.Redaction)
		want error
		name string
	}{
		{name: "empty reason", edit: func(r *history.Redaction) { r.Reason = " " }, want: history.ErrInvalidReason},
		{
			name: "unknown cause",
			edit: func(r *history.Redaction) { r.Cause = "other" },
			want: history.ErrInvalidRedaction,
		},
		{name: "no actor", edit: func(r *history.Redaction) { r.By.Subject = "" }, want: history.ErrInvalidRedaction},
		{name: "bad item", edit: func(r *history.Redaction) { r.Item.Item = "" }, want: history.ErrInvalidItem},
	}

	for _, tc := range cases {
		r := newRedaction(stateItem("ws-one", "sv-1"), suiteTime())
		tc.edit(&r)

		if _, err := s.AppendRedaction(t.Context(), r, nil, fence); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func testGetMissing(t *testing.T, s RedactionStore) {
	t.Helper()
	makeNamespace(t, s)

	_, err := s.GetRedaction(
		t.Context(),
		"acme",
		history.NewRedactionID(),
	)
	if !errors.Is(
		err,
		history.ErrRedactionNotFound,
	) {
		t.Errorf("get missing err = %v, want ErrRedactionNotFound", err)
	}
}

func testListNewestFirst(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)

	var want []history.RedactionID

	for i := range 3 {
		r := newRedaction(
			stateItem("ws-one", []string{"sv-1", "sv-2", "sv-3"}[i]),
			suiteTime().Add(time.Duration(i)*time.Minute),
		)
		mustAppend(t, s, r, nil, fence)
		want = append([]history.RedactionID{r.ID}, want...)
	}

	mustAppend(t, s, newRedaction(stateItem("ws-two", "sv-1"), suiteTime()), nil, fence)

	first, err := s.ListRedactions(t.Context(), unitOf("ws-one"), nil, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d records, %v, want 2", len(first), err)
	}

	last := first[len(first)-1]

	rest, err := s.ListRedactions(
		t.Context(),
		unitOf("ws-one"),
		&history.RedactionCursor{At: last.At, ID: last.ID},
		2,
	)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	items := slices.Concat(first, rest)
	got := make([]history.RedactionID, 0, len(items))
	for i := range items {
		r := items[i]
		got = append(got, r.ID)
	}

	if !slices.Equal(got, want) {
		t.Errorf("listed = %v, want %v", got, want)
	}
}

func testPendingUntilDone(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	r := mustAppend(t, s, newRedaction(stateItem("ws-one", "sv-1"), suiteTime()), []string{backupTarget}, fence)

	for _, target := range []string{history.LiveTarget, backupTarget} {
		pending, err := s.ListPendingShreds(t.Context(), "", 10)
		if err != nil {
			t.Fatalf("pending: %v", err)
		}

		if len(pending) != 1 || pending[0].ID != r.ID {
			t.Fatalf("pending before %s = %v, want the redaction", target, pending)
		}

		if err := s.FinishShred(t.Context(), r.ID, target, suiteTime().Add(time.Hour), fence); err != nil {
			t.Fatalf("finish %s: %v", target, err)
		}
	}

	pending, err := s.ListPendingShreds(t.Context(), "", 10)
	if err != nil || len(pending) != 0 {
		t.Errorf("pending after every target = %v, %v, want none", pending, err)
	}

	got, err := s.GetRedaction(t.Context(), "acme", r.ID)
	if err != nil || got.Pending() {
		t.Errorf("redaction after every target = %+v, %v, want done", got, err)
	}
}

func testFinishTwice(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	r := mustAppend(t, s, newRedaction(stateItem("ws-one", "sv-1"), suiteTime()), nil, fence)
	first := suiteTime().Add(time.Hour)

	if err := s.FinishShred(t.Context(), r.ID, history.LiveTarget, first, fence); err != nil {
		t.Fatalf("first finish: %v", err)
	}

	if err := s.FinishShred(t.Context(), r.ID, history.LiveTarget, first.Add(time.Hour), fence); err != nil {
		t.Fatalf("second finish: %v", err)
	}

	got, err := s.GetRedaction(t.Context(), "acme", r.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !got.Targets[0].DoneAt.Equal(first) {
		t.Errorf("done at = %v, want the first time %v", got.Targets[0].DoneAt, first)
	}
}

func testNoteFailure(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	r := mustAppend(t, s, newRedaction(stateItem("ws-one", "sv-1"), suiteTime()), []string{backupTarget}, fence)
	at := suiteTime().Add(time.Hour)

	for range 2 {
		err := s.NoteShredFailure(
			t.Context(),
			history.ShredFailure{At: at, ID: r.ID, Target: backupTarget, Text: "target does not answer"},
			fence,
		)
		if err != nil {
			t.Fatalf("note failure: %v", err)
		}
	}

	got, err := s.GetRedaction(t.Context(), "acme", r.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	target := got.Targets[1]
	if target.State != history.ShredPending || target.Attempts != 2 || target.LastError != "target does not answer" ||
		!target.LastTryAt.Equal(at) {
		t.Errorf("target after two failures = %+v, want pending with 2 tries and the text", target)
	}
}

func testUnknownTarget(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	r := mustAppend(t, s, newRedaction(stateItem("ws-one", "sv-1"), suiteTime()), nil, fence)

	err := s.FinishShred(
		t.Context(),
		r.ID,
		"dir:/other",
		suiteTime(),
		fence,
	)
	if !errors.Is(
		err,
		history.ErrTargetNotFound,
	) {
		t.Errorf("finish of an unknown target err = %v, want ErrTargetNotFound", err)
	}

	err = s.FinishShred(t.Context(), history.NewRedactionID(), history.LiveTarget, suiteTime(), fence)
	if !errors.Is(err, history.ErrRedactionNotFound) {
		t.Errorf("finish of an unknown redaction err = %v, want ErrRedactionNotFound", err)
	}
}

func testRedactionFenced(t *testing.T, s RedactionStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	stale := staleFence(fence)
	r := newRedaction(stateItem("ws-one", "sv-1"), suiteTime())

	if _, err := s.AppendRedaction(t.Context(), r, nil, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("append with a stale fence err = %v, want ErrFenced", err)
	}

	if _, err := s.RedactionOf(t.Context(), r.Item); !errors.Is(err, history.ErrRedactionNotFound) {
		t.Errorf("a fenced append left a record: err = %v", err)
	}

	mustAppend(t, s, r, nil, fence)

	err := s.FinishShred(
		t.Context(),
		r.ID,
		history.LiveTarget,
		suiteTime(),
		stale,
	)
	if !errors.Is(
		err,
		namespace.ErrFenced,
	) {
		t.Errorf("finish with a stale fence err = %v, want ErrFenced", err)
	}

	err = s.NoteShredFailure(
		t.Context(),
		history.ShredFailure{At: suiteTime(), ID: r.ID, Target: history.LiveTarget, Text: "x"},
		stale,
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("note failure with a stale fence err = %v, want ErrFenced", err)
	}
}
