package preferences_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/preferences"
)

var errTxDown = errors.New("the transaction failed")

// fakeTx is a transaction that records what Change does.
type fakeTx struct {
	document string
	updated  int64
	loadErr  error
	saveErr  error
	appendEr error

	saved    []string
	savedMs  int64
	appended []audit.Entry
}

func (f *fakeTx) Load(context.Context, string) (string, int64, error) {
	return f.document, f.updated, f.loadErr
}

func (f *fakeTx) Save(_ context.Context, _, document string, updatedAtMs int64) error {
	f.saved = append(f.saved, document)
	f.savedMs = updatedAtMs

	return f.saveErr
}

func (f *fakeTx) Append(_ context.Context, e audit.Entry) error {
	f.appended = append(f.appended, e)

	return f.appendEr
}

// newFakeTx returns a transaction with no row and no error.
func newFakeTx() *fakeTx {
	return &fakeTx{
		document: "", updated: 0, loadErr: nil, saveErr: nil, appendEr: nil, saved: nil, savedMs: 0, appended: nil,
	}
}

func entryAt(at time.Time) audit.Entry {
	e := blankEntry()
	e.At, e.Namespace, e.Subject = at, audit.ServerScope, "alice"

	return e
}

func TestChangeWritesARealChange(t *testing.T) {
	t.Parallel()

	tx := newFakeTx()
	at := time.Date(2026, time.October, 20, 12, 0, 0, 999999999, time.UTC)
	patch := preferences.Patch{"language": preferences.Text("ar"), "theme": preferences.Text("terminal")}

	res, err := preferences.Change(t.Context(), tx, nil, "prn-alice", patch, entryAt(at), nil)
	if err != nil {
		t.Fatalf("Change: %v", err)
	}

	if len(tx.saved) != 1 || tx.saved[0] != `{"language":"ar","theme":"terminal"}` || tx.savedMs != at.UnixMilli() {
		t.Errorf("saved = %v at %d, want one canonical document at %d", tx.saved, tx.savedMs, at.UnixMilli())
	}

	if len(tx.appended) != 1 || tx.appended[0].Action != preferences.AuditAction {
		t.Fatalf("appended = %+v, want one preferences entry", tx.appended)
	}

	if !slices.Equal(res.Changed, []string{"theme", "language"}) || res.Document.Language != "ar" ||
		res.Document.UpdatedAt.UnixMilli() != at.UnixMilli() {
		t.Errorf("result = %+v, want theme and language changed at the time of the entry", res)
	}
}

func TestChangeWritesNothingForANoOp(t *testing.T) {
	t.Parallel()

	tx := newFakeTx()
	tx.document, tx.updated = `{"language":"ar"}`, 1_000
	patch := preferences.Patch{"language": preferences.Text("ar"), "theme": nil}

	res, err := preferences.Change(t.Context(), tx, nil, "prn-alice", patch, entryAt(time.UnixMilli(5_000)), nil)
	if err != nil {
		t.Fatalf("Change: %v", err)
	}

	if len(tx.saved) != 0 || len(tx.appended) != 0 {
		t.Errorf("saved %v and appended %v, want nothing", tx.saved, tx.appended)
	}

	if len(res.Changed) != 0 || res.Document.UpdatedAt.UnixMilli() != 1_000 || res.Document.Language != "ar" {
		t.Errorf("result = %+v, want no change and the old time", res)
	}
}

func TestChangeStopsOnAnError(t *testing.T) {
	t.Parallel()

	change := preferences.Patch{"theme": preferences.Text("terminal")}
	badDigits := preferences.Patch{"digits": preferences.Text("deva")}

	tests := []struct {
		name  string
		setup func(tx *fakeTx)
		patch preferences.Patch
		want  error
		saved int
		appnd int
	}{
		{
			"a load error",
			func(tx *fakeTx) { tx.loadErr = preferences.ErrPersonNotFound },
			change,
			preferences.ErrPersonNotFound,
			0,
			0,
		},
		{"a patch that fails a check", func(*fakeTx) {}, badDigits, preferences.ErrInvalidValue, 0, 0},
		{"a save error", func(tx *fakeTx) { tx.saveErr = errTxDown }, change, errTxDown, 1, 0},
		{
			"an append error",
			func(tx *fakeTx) { tx.appendEr = audit.ErrInvalidEntry },
			change,
			audit.ErrInvalidEntry,
			1,
			1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tx := newFakeTx()
			tc.setup(tx)

			res, err := preferences.Change(t.Context(), tx, nil, "prn-alice", tc.patch, entryAt(time.UnixMilli(5_000)), nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}

			if res.Document != preferences.Default() || res.Changed != nil {
				t.Errorf("result = %+v, want the zero result with an error", res)
			}

			if len(tx.saved) != tc.saved || len(tx.appended) != tc.appnd {
				t.Errorf(
					"saved %d and appended %d, want %d and %d",
					len(tx.saved),
					len(tx.appended),
					tc.saved,
					tc.appnd,
				)
			}
		})
	}
}

func TestChangeIgnoresAStoredFieldThatFailsACheck(t *testing.T) {
	t.Parallel()

	const stored = `{"timeZone":"Mars/Olympus","theme":"terminal"}`

	// A patch that resets the bad field works, and changes nothing that the
	// person sees.
	tx := newFakeTx()
	tx.document, tx.updated = stored, 1_000

	res, err := preferences.Change(
		t.Context(), tx, nil, "prn-alice", preferences.Patch{"timeZone": nil}, entryAt(time.UnixMilli(5_000)), nil,
	)
	if err != nil || len(res.Changed) != 0 || len(tx.saved) != 0 || res.Document.TimeZone != "" {
		t.Errorf("reset: result = %+v, %v, saved %q; want no change", res, err, tx.saved)
	}

	// Another change works, and the write drops the bad field.
	tx = newFakeTx()
	tx.document, tx.updated = stored, 1_000

	res, err = preferences.Change(
		t.Context(), tx, nil, "prn-alice", preferences.Patch{"colorMode": preferences.Text("dark")},
		entryAt(time.UnixMilli(5_000)), nil,
	)
	if err != nil || !slices.Equal(res.Changed, []string{"colorMode"}) ||
		len(tx.saved) != 1 || tx.saved[0] != `{"colorMode":"dark","theme":"terminal"}` {
		t.Errorf("other change: result = %+v, %v, saved %q; want the bad field gone", res, err, tx.saved)
	}
}

func TestChangeChecksTheVersion(t *testing.T) {
	t.Parallel()

	patch := preferences.Patch{"theme": preferences.Text("terminal")}
	stale, current := int64(999), int64(1_000)

	for name, tc := range map[string]struct {
		ifMatch *int64
		want    error
	}{
		"no precondition": {nil, nil},
		"the version":     {&current, nil},
		"another version": {&stale, preferences.ErrPreconditionFailed},
	} {
		tx := newFakeTx()
		tx.document, tx.updated = `{"language":"ar"}`, 1_000

		_, err := preferences.Change(t.Context(), tx, nil, "prn-alice", patch, entryAt(time.UnixMilli(5_000)), tc.ifMatch)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tc.want)
		}

		if tc.want != nil && (len(tx.saved) != 0 || len(tx.appended) != 0) {
			t.Errorf("%s: wrote %d rows and %d entries, want none", name, len(tx.saved), len(tx.appended))
		}
	}

	// A person that never changed a preference has version 0.
	zero := int64(0)

	if _, err := preferences.Change(
		t.Context(), newFakeTx(), nil, "prn-alice", patch, entryAt(time.UnixMilli(5_000)), &zero,
	); err != nil {
		t.Errorf("version 0 on no row: %v", err)
	}
}

func TestChangeGivesEachChangeANewVersion(t *testing.T) {
	t.Parallel()

	tx := newFakeTx()
	tx.document, tx.updated = `{"language":"ar"}`, 5_000

	// The time of the entry is the time of the last change, or older.
	for _, at := range []int64{5_000, 4_000} {
		tx.saved = nil

		res, err := preferences.Change(
			t.Context(), tx, nil, "prn-alice", preferences.Patch{"theme": preferences.Text("terminal")},
			entryAt(time.UnixMilli(at)), nil,
		)
		if err != nil || tx.savedMs != 5_001 || res.Document.Version() != 5_001 {
			t.Errorf("at %d: saved %d, result %+v, %v; want the version 5001", at, tx.savedMs, res.Document, err)
		}
	}
}
