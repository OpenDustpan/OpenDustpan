package pulumijournal_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// boundReplayer replays base and entries with the fixed clock and version.
func boundReplayer(t *testing.T, base string, entries []byte) error {
	t.Helper()
	now := func() time.Time { return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC) }
	open := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(base)), nil
	}
	var out bytes.Buffer
	return pulumijournal.NewReplayer(now, "3.268.0").Replay(t.Context(), open, [][]byte{entries}, &out)
}

// beginOnlyEntries returns a batch body with n begin entries and no states.
func beginOnlyEntries(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(
			&b,
			`{"version":1,"kind":0,"sequenceID":%d,"operationID":%d,"removeOld":null,"removeNew":null}`,
			i+1,
			i+1,
		)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// TestReplayRefusesTooManyEntries checks that a replay with more entries than
// the bound allows fails with ErrTooManyEntries, while the bound itself passes.
//
//nolint:paralleltest // The test lowers the entry bound, so it runs alone.
func TestReplayRefusesTooManyEntries(t *testing.T) {
	old := pulumijournal.MaxEntriesPerUpdate
	pulumijournal.MaxEntriesPerUpdate = 30
	t.Cleanup(func() { pulumijournal.MaxEntriesPerUpdate = old })
	if err := boundReplayer(t, `{"resources":[]}`, beginOnlyEntries(30)); err != nil {
		t.Fatalf("replay with 30 entries: err = %v, want nil", err)
	}
	if err := boundReplayer(t, `{"resources":[]}`, beginOnlyEntries(31)); !errors.Is(
		err,
		pulumijournal.ErrTooManyEntries,
	) {
		t.Fatalf("replay with 31 entries: err = %v, want ErrTooManyEntries", err)
	}
}

// oneSuccessEntry returns a batch body with one success entry carrying state.
func oneSuccessEntry(state string) []byte {
	return []byte(
		`{"entries":[{"version":1,"kind":1,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,"state":` + state + `}]}`,
	)
}

// TestReplayRefusesNewStateTooLarge checks that new states at the byte bound
// pass and one more byte fails with ErrNewStateTooLarge.
//
//nolint:paralleltest // The test lowers the state bound, so it runs alone.
func TestReplayRefusesNewStateTooLarge(t *testing.T) {
	state := `{"urn":"urn:pulumi:dev::web::t:mod:T::r","custom":true,"type":"t:mod:T","pad":"` + strings.Repeat(
		"x",
		64,
	) + `"}`

	old := pulumijournal.MaxNewStateBytesPerUpdate
	t.Cleanup(func() { pulumijournal.MaxNewStateBytesPerUpdate = old })

	pulumijournal.MaxNewStateBytesPerUpdate = len(state)
	if err := boundReplayer(t, `{"resources":[]}`, oneSuccessEntry(state)); err != nil {
		t.Fatalf("replay with new states at the bound: err = %v, want nil", err)
	}

	pulumijournal.MaxNewStateBytesPerUpdate = len(state) - 1
	if err := boundReplayer(t, `{"resources":[]}`, oneSuccessEntry(state)); !errors.Is(
		err,
		pulumijournal.ErrNewStateTooLarge,
	) {
		t.Fatalf("replay with one byte over the bound: err = %v, want ErrNewStateTooLarge", err)
	}
}

// TestReplayRefusesLinkIndexTooLarge checks that a replay that adds more links
// than the bound allows fails with ErrLinkIndexTooLarge, while the bound
// itself passes.
//
//nolint:paralleltest // The test lowers the link bound, so it runs alone.
func TestReplayRefusesLinkIndexTooLarge(t *testing.T) {
	old := pulumijournal.MaxLinkIndexEntriesPerUpdate
	pulumijournal.MaxLinkIndexEntriesPerUpdate = 10
	t.Cleanup(func() { pulumijournal.MaxLinkIndexEntriesPerUpdate = old })

	if err := boundReplayer(t, `{"resources":[]}`, benchCreateEntries(10)); err != nil {
		t.Fatalf("replay that adds 10 links: err = %v, want nil", err)
	}
	if err := boundReplayer(t, `{"resources":[]}`, benchCreateEntries(11)); !errors.Is(
		err,
		pulumijournal.ErrLinkIndexTooLarge,
	) {
		t.Fatalf("replay that adds 11 links: err = %v, want ErrLinkIndexTooLarge", err)
	}
}

// refreshReplaceEntries returns a batch body that refreshes the first n base
// resources: a begin and a refresh-success entry with a new state for each one.
func refreshReplaceEntries(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		seq := int64(2*i + 1)
		op := int64(i + 1)
		fmt.Fprintf(
			&b,
			`{"version":1,"kind":0,"sequenceID":%d,"operationID":%d,"removeOld":null,"removeNew":null},`,
			seq,
			op,
		)
		fmt.Fprintf(
			&b,
			`{"version":1,"kind":3,"sequenceID":%d,"operationID":%d,"removeOld":%d,"removeNew":null,"state":%s}`,
			seq+1,
			op,
			i,
			benchStateChange(i),
		)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// TestReplayCountsRefreshReplacementsTowardLinkBound checks that a base
// resource that a refresh replaces counts as an added link: replacements at
// the bound pass and one more fails with ErrLinkIndexTooLarge.
//
//nolint:paralleltest // The test lowers the link bound, so it runs alone.
func TestReplayCountsRefreshReplacementsTowardLinkBound(t *testing.T) {
	old := pulumijournal.MaxLinkIndexEntriesPerUpdate
	pulumijournal.MaxLinkIndexEntriesPerUpdate = 5
	t.Cleanup(func() { pulumijournal.MaxLinkIndexEntriesPerUpdate = old })

	base := benchBase(10)
	if err := boundReplayer(t, base, refreshReplaceEntries(5)); err != nil {
		t.Fatalf("replay that replaces 5 resources with link bound 5: err = %v, want nil", err)
	}
	if err := boundReplayer(t, base, refreshReplaceEntries(6)); !errors.Is(
		err,
		pulumijournal.ErrLinkIndexTooLarge,
	) {
		t.Fatalf("replay that replaces 6 resources with link bound 5: err = %v, want ErrLinkIndexTooLarge", err)
	}
}

// TestReplayCountsMigrationAndPatchBytes checks that the states of a migration
// entry and of its patches count toward the new-state bound: an entry whose
// own state fits the bound fails with ErrNewStateTooLarge when its migration
// states and patches put it over.
//
//nolint:paralleltest // The test lowers the state bound, so it runs alone.
func TestReplayCountsMigrationAndPatchBytes(t *testing.T) {
	old := pulumijournal.MaxNewStateBytesPerUpdate
	pulumijournal.MaxNewStateBytesPerUpdate = 1000
	t.Cleanup(func() { pulumijournal.MaxNewStateBytesPerUpdate = old })

	migrated := `{"urn":"urn:pulumi:dev::web::t:mod:T::m1","custom":true,"type":"t:mod:T","pad":"` + strings.Repeat(
		"x",
		500,
	) + `"}`
	patched := `{"urn":"urn:pulumi:dev::web::t:mod:T::m2","custom":true,"type":"t:mod:T","pad":"` + strings.Repeat(
		"y",
		500,
	) + `"}`
	body := []byte(
		`{"entries":[{"version":2,"kind":10,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,"state":{},"states":[` +
			migrated + `],"baseStatePatches":[{"index":0,"state":` + patched + `}]}]}`,
	)

	if err := boundReplayer(t, `{"resources":[]}`, body); !errors.Is(
		err,
		pulumijournal.ErrNewStateTooLarge,
	) {
		t.Fatalf("replay with migration states over the bound: err = %v, want ErrNewStateTooLarge", err)
	}
}

// TestReplayCountsNewStatePatchBytes checks that the states of new-state
// patches count toward the new-state bound when nothing else carries bytes:
// a patch at the exact limit passes, and one more byte fails with
// ErrNewStateTooLarge. Removing the patch bytes from the count passes the
// over-limit case.
//
//nolint:paralleltest // The test lowers the state bound, so it runs alone.
func TestReplayCountsNewStatePatchBytes(t *testing.T) {
	ctx := t.Context()
	if err := ctx.Err(); err != nil {
		t.Fatalf("test context: %v", err)
	}

	patch := `{"urn":"urn:pulumi:dev::web::t:mod:T::p","custom":true,"type":"t:mod:T","pad":"` +
		strings.Repeat("x", 64) + `"}`
	body := []byte(
		`{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,` +
			`"newStatePatches":[{"operationID":1,"state":` + patch + `}]}]}`,
	)

	old := pulumijournal.MaxNewStateBytesPerUpdate
	t.Cleanup(func() { pulumijournal.MaxNewStateBytesPerUpdate = old })

	pulumijournal.MaxNewStateBytesPerUpdate = len(patch)
	if err := boundReplayer(t, `{"resources":[]}`, body); err != nil {
		t.Fatalf("replay with new-state patches at the bound: err = %v, want nil", err)
	}

	pulumijournal.MaxNewStateBytesPerUpdate = len(patch) - 1
	if err := boundReplayer(t, `{"resources":[]}`, body); !errors.Is(
		err,
		pulumijournal.ErrNewStateTooLarge,
	) {
		t.Fatalf("replay with new-state patches one byte over: err = %v, want ErrNewStateTooLarge", err)
	}
}

// TestReplayNoOpOnLargeBasePasses checks that a no-op update on a base with
// more resources than the link-index bound still completes: the bound counts
// only the links the update adds.
//
//nolint:paralleltest // The test lowers the link bound, so it runs alone.
func TestReplayNoOpOnLargeBasePasses(t *testing.T) {
	old := pulumijournal.MaxLinkIndexEntriesPerUpdate
	pulumijournal.MaxLinkIndexEntriesPerUpdate = 5
	t.Cleanup(func() { pulumijournal.MaxLinkIndexEntriesPerUpdate = old })

	base := benchBase(20)
	if err := boundReplayer(t, base, beginOnlyEntries(2)); err != nil {
		t.Fatalf("no-op replay on 20 resources with link bound 5: err = %v, want nil", err)
	}
}

// TestReplayRefusesCanceledContext checks that a replay with a canceled
// context stops with the context error.
func TestReplayRefusesCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	now := func() time.Time { return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC) }
	open := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"resources":[]}`)), nil
	}
	var out bytes.Buffer
	err := pulumijournal.NewReplayer(now, "3.268.0").Replay(ctx, open, [][]byte{benchCreateEntries(10)}, &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("replay with a canceled context: err = %v, want context.Canceled", err)
	}
}
