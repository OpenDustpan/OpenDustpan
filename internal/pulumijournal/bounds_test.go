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
	for i := 0; i < n; i++ {
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
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		seq := int64(2*i + 1)
		op := int64(i + 1)
		fmt.Fprintf(&b, `{"version":1,"kind":0,"sequenceID":%d,"operationID":%d,"removeOld":null,"removeNew":null},`, seq, op)
		fmt.Fprintf(
			&b,
			`{"version":1,"kind":1,"sequenceID":%d,"operationID":%d,"removeOld":null,"removeNew":null,"state":{"urn":"urn:pulumi:dev::web::t:mod:T::r-%d","custom":true,"type":"t:mod:T","pad":"%s"}}}`,
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
// the bound allows fails with ErrTooManyEntries.
func TestReplayRefusesTooManyEntries(t *testing.T) {
	t.Parallel()
	entries := beginOnlyEntries(pulumijournal.MaxEntriesPerUpdate + 1)
	if err := boundReplayer(t, `{"resources":[]}`, entries); !errors.Is(err, pulumijournal.ErrTooManyEntries) {
		t.Fatalf("replay with %d entries: err = %v, want ErrTooManyEntries", pulumijournal.MaxEntriesPerUpdate+1, err)
	}
}

// TestReplayRefusesNewStateTooLarge checks that a replay with more new state
// bytes than the bound allows fails with ErrNewStateTooLarge.
func TestReplayRefusesNewStateTooLarge(t *testing.T) {
	t.Parallel()
	// Twenty states of 700 KiB exceed the 12 MiB bound with few entries.
	entries := largeStateEntries(20, 700<<10)
	if err := boundReplayer(t, `{"resources":[]}`, entries); !errors.Is(err, pulumijournal.ErrNewStateTooLarge) {
		t.Fatalf("replay with oversized states: err = %v, want ErrNewStateTooLarge", err)
	}
}

// TestReplayRefusesLinkIndexTooLarge checks that a replay with more indexed
// resources than the bound allows fails with ErrLinkIndexTooLarge.
func TestReplayRefusesLinkIndexTooLarge(t *testing.T) {
	t.Parallel()
	n := int(pulumijournal.MaxLinkIndexEntriesPerUpdate) + 1
	entries := benchCreateEntries(n)
	if err := boundReplayer(t, `{"resources":[]}`, entries); !errors.Is(err, pulumijournal.ErrLinkIndexTooLarge) {
		t.Fatalf("replay with %d indexed resources: err = %v, want ErrLinkIndexTooLarge", n, err)
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
