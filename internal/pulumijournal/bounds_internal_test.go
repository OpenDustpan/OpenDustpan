package pulumijournal

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestCheckIndexRefusesAddedBytes checks that a build with more new-state plus
// added-link bytes than the bound allows fails with ErrAddedBytesTooLarge,
// while small builds pass.
func TestCheckIndexRefusesAddedBytes(t *testing.T) {
	t.Parallel()
	// One added link with 70 MiB of link bytes exceeds the 64 MiB bound, so no
	// other bound triggers first.
	big := linkFields{ //nolint:exhaustruct_v5 // The test sets only the links that count toward the bound.
		URN:          "urn:pulumi:dev::web::t:mod:T::big",
		Dependencies: []string{strings.Repeat("d", 70<<20)},
	}
	scan := &baseScan{ //nolint:exhaustruct_v5 // The test sets the added counts only.
		added:          1,
		addedLinkBytes: linkSize(big),
	}
	state := newReplayState(nil, nil)
	if err := state.checkIndex(scan); !errors.Is(err, ErrAddedBytesTooLarge) {
		t.Fatalf("checkIndex with oversized links: err = %v, want ErrAddedBytesTooLarge", err)
	}

	small := &baseScan{ //nolint:exhaustruct_v5 // The test sets the added counts only.
		added:          1,
		addedLinkBytes: int64(len("urn:pulumi:dev::web::t:mod:T::a")),
	}
	if err := state.checkIndex(small); err != nil {
		t.Fatalf("checkIndex with small links: err = %v, want nil", err)
	}
}

// TestCountEntryEnforcesEntryLimit checks that the entry bound itself passes
// and one more entry fails with ErrTooManyEntries.
//
//nolint:paralleltest // The test lowers the entry bound, so it runs alone.
func TestCountEntryEnforcesEntryLimit(t *testing.T) {
	old := MaxEntriesPerUpdate
	MaxEntriesPerUpdate = 3
	t.Cleanup(func() { MaxEntriesPerUpdate = old })

	empty := JournalEntry{} //nolint:exhaustruct_v5 // The test counts entries without states.
	state := newReplayState(nil, nil)
	for range 3 {
		if err := state.countEntry(empty); err != nil {
			t.Fatalf("countEntry at the bound: err = %v, want nil", err)
		}
	}
	if err := state.countEntry(empty); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("countEntry past the bound: err = %v, want ErrTooManyEntries", err)
	}
}

// TestCountEntryEnforcesStateLimit checks that new states at the byte bound
// pass and one more byte fails with ErrNewStateTooLarge.
//
//nolint:paralleltest // The test lowers the state bound, so it runs alone.
func TestCountEntryEnforcesStateLimit(t *testing.T) {
	old := MaxNewStateBytesPerUpdate
	MaxNewStateBytesPerUpdate = 200
	t.Cleanup(func() { MaxNewStateBytesPerUpdate = old })

	state := newReplayState(nil, nil)
	raw := jsontext.Value(strings.Repeat("x", 100))
	entry := JournalEntry{State: raw} //nolint:exhaustruct_v5 // The test counts state bytes only.
	for range 2 {
		if err := state.countEntry(entry); err != nil {
			t.Fatalf("countEntry at the bound: err = %v, want nil", err)
		}
	}
	if err := state.countEntry(entry); !errors.Is(err, ErrNewStateTooLarge) {
		t.Fatalf("countEntry past the bound: err = %v, want ErrNewStateTooLarge", err)
	}
}

// TestCheckIndexEnforcesAddedLimit checks that added links at the bound pass
// and one more fails with ErrLinkIndexTooLarge.
//
//nolint:paralleltest // The test lowers the link bound, so it runs alone.
func TestCheckIndexEnforcesAddedLimit(t *testing.T) {
	old := MaxLinkIndexEntriesPerUpdate
	MaxLinkIndexEntriesPerUpdate = 2
	t.Cleanup(func() { MaxLinkIndexEntriesPerUpdate = old })

	state := newReplayState(nil, nil)
	at := &baseScan{added: 2} //nolint:exhaustruct_v5 // The test sets the added count only.
	if err := state.checkIndex(at); err != nil {
		t.Fatalf("checkIndex at the bound: err = %v, want nil", err)
	}
	past := &baseScan{added: 3} //nolint:exhaustruct_v5 // The test sets the added count only.
	if err := state.checkIndex(past); !errors.Is(err, ErrLinkIndexTooLarge) {
		t.Fatalf("checkIndex past the bound: err = %v, want ErrLinkIndexTooLarge", err)
	}
}

// TestCheckIndexEnforcesCountedLimit checks that new-state plus added-link
// bytes at the bound pass and one more byte fails with ErrAddedBytesTooLarge.
//
//nolint:paralleltest // The test lowers the counted-bytes bound, so it runs alone.
func TestCheckIndexEnforcesCountedLimit(t *testing.T) {
	old := MaxNewStatePlusLinkBytesPerUpdate
	MaxNewStatePlusLinkBytesPerUpdate = 500
	t.Cleanup(func() { MaxNewStatePlusLinkBytesPerUpdate = old })

	state := newReplayState(nil, nil)
	state.newStateBytes = 200
	at := &baseScan{added: 1, addedLinkBytes: 300} //nolint:exhaustruct_v5 // The test sets the added counts only.
	if err := state.checkIndex(at); err != nil {
		t.Fatalf("checkIndex at the bound: err = %v, want nil", err)
	}
	past := &baseScan{added: 1, addedLinkBytes: 301} //nolint:exhaustruct_v5 // The test sets the added counts only.
	if err := state.checkIndex(past); !errors.Is(err, ErrAddedBytesTooLarge) {
		t.Fatalf("checkIndex past the bound: err = %v, want ErrAddedBytesTooLarge", err)
	}
}

// TestApplyRefusesCanceledContext checks that applying one entry with a
// canceled context stops with the context error before touching the entry.
func TestApplyRefusesCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	state := newReplayState(nil, nil)
	entry := JournalEntry{} //nolint:exhaustruct_v5 // The test applies an empty entry under a canceled context.
	if err := state.apply(ctx, entry); !errors.Is(err, context.Canceled) {
		t.Fatalf("apply with a canceled context: err = %v, want context.Canceled", err)
	}
}

// TestWriteRefusesCanceledContext checks that writing the deployment with a
// canceled context stops with the context error before writing a byte.
func TestWriteRefusesCanceledContext(t *testing.T) {
	t.Parallel()

	now := func() time.Time { return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC) }
	replayer := NewReplayer(now, "3.268.0")
	open := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"resources":[]}`)), nil
	}

	state := newReplayState(open, replayer)
	scan, err := state.scanBase(t.Context(), open)
	if err != nil {
		t.Fatalf("scan base: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var out bytes.Buffer
	if err := state.write(ctx, open, &out, scan, replayer); !errors.Is(err, context.Canceled) {
		t.Fatalf("write with a canceled context: err = %v, want context.Canceled", err)
	}

	if out.Len() != 0 {
		t.Fatalf("write with a canceled context wrote %d bytes, want none", out.Len())
	}
}

// TestLinkBytesCountsMembers checks that linkBytes adds every link member.
func TestLinkBytesCountsMembers(t *testing.T) {
	t.Parallel()
	links := linkFields{
		URN:                  "urn:a",
		Parent:               "urn:p",
		Dependencies:         []string{"urn:d"},
		PropertyDependencies: map[string][]string{"p": {"urn:q"}},
		ReplaceWith:          []string{"urn:r"},
		DeletedWith:          "urn:w",
		Aliases:              []string{"urn:l"},
		Provider:             "urn:v",
		ViewOf:               "urn:o",
	}
	var want int64
	want += int64(len("urn:a") + len("urn:p") + len("urn:v") + len("urn:o") + len("urn:w"))
	want += int64(len("urn:d") + len("urn:l") + len("urn:r") + len("urn:q"))
	if got := linkBytes([]linkFields{links}); got != want {
		t.Fatalf("linkBytes = %d, want %d", got, want)
	}
	if got := linkBytes(nil); got != 0 {
		t.Fatalf("linkBytes of no index = %d, want 0", got)
	}
}
