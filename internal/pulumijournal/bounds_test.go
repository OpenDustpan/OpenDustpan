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
	return pulumijournal.NewReplayer(now, "3.268.0").Replay(t.Context(), open, bytes.NewReader(entries), &out)
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

// largeStateEntries returns a batch body with n success entries, each carrying
// a state with pad bytes of padding.
func largeStateEntries(n, pad int) []byte {
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
			`{"version":1,"kind":1,"sequenceID":%d,"operationID":%d,"removeOld":null,"removeNew":null,"state":{"urn":"urn:pulumi:dev::web::t:mod:T::r-%d","custom":true,"type":"t:mod:T","pad":"%s"}}`,
			seq+1,
			op,
			i,
			strings.Repeat("x", pad),
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

// TestReplayRefusesNewStateTooLarge checks that a replay with more new state
// bytes than the bound allows fails with ErrNewStateTooLarge, while the bound
// itself passes.
//
//nolint:paralleltest // The test lowers the state bound, so it runs alone.
func TestReplayRefusesNewStateTooLarge(t *testing.T) {
	old := pulumijournal.MaxNewStateBytesPerUpdate
	pulumijournal.MaxNewStateBytesPerUpdate = 2000
	t.Cleanup(func() { pulumijournal.MaxNewStateBytesPerUpdate = old })

	// One entry carries about 150 state bytes, so 10 entries pass under 2,000
	// and 20 do not.
	if err := boundReplayer(t, `{"resources":[]}`, largeStateEntries(10, 64)); err != nil {
		t.Fatalf("replay with small states: err = %v, want nil", err)
	}
	if err := boundReplayer(t, `{"resources":[]}`, largeStateEntries(20, 64)); !errors.Is(
		err,
		pulumijournal.ErrNewStateTooLarge,
	) {
		t.Fatalf("replay with oversized states: err = %v, want ErrNewStateTooLarge", err)
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
	err := pulumijournal.NewReplayer(now, "3.268.0").Replay(ctx, open, bytes.NewReader(benchCreateEntries(10)), &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("replay with a canceled context: err = %v, want context.Canceled", err)
	}
}
