// Package logsinktest is the conformance suite for logsink.Sink
// implementations. Every built-in adapter runs it, and custom adapters
// should too:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		logsinktest.Run(t, func(t *testing.T) logsinktest.TestSink { return ... })
//	}
package logsinktest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
)

// TestSink is one fresh sink with a way to read back what it delivered.
type TestSink struct {
	// Sink is the sink under test.
	Sink logsink.Sink
	// Delivered answers each record that the target received so far, in
	// order, as the line that logsink.MarshalJSON writes for it. The suite
	// calls it only after a Write returned, so an adapter whose target
	// receives the records later (a network listener) must wait until
	// want records arrived, or until a short timeout.
	Delivered func(t *testing.T, want int) [][]byte
}

// Factory returns a fresh, empty sink for one subtest.
type Factory func(t *testing.T) TestSink

// Run executes the conformance suite against sinks produced by newSink.
func Run(t *testing.T, newSink Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s TestSink)
		name string
	}{
		{name: "write delivers every record in order", test: testOrder},
		{name: "two writes deliver both batches in order", test: testTwoBatches},
		{name: "an empty batch delivers nothing", test: testEmptyBatch},
		{name: "special characters survive delivery", test: testSpecialCharacters},
		{name: "write after close fails with ErrClosed", test: testWriteAfterClose},
		{name: "a second close is not an error", test: testCloseTwice},
		{name: "write with a done context fails", test: testDoneContext},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSink(t)

			t.Cleanup(func() {
				if err := s.Sink.Close(); err != nil {
					t.Errorf("close sink: %v", err)
				}
			})

			tc.test(t, s)
		})
	}
}

// Batch sizes of the cases: large enough to show the order, small enough to
// read in a failure message.
const (
	orderBatch    = 5
	twoBatchTotal = 6
)

// at is a fixed instant with milliseconds, the precision of the JSON form.
func at(offset int) time.Time {
	return time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC).Add(time.Duration(offset) * time.Millisecond)
}

// sample returns n records with distinct messages and attributes.
func sample(n int) []logsink.Record {
	out := make([]logsink.Record, 0, n)

	for i := range n {
		out = append(out, logsink.Record{
			Time:    at(i),
			Level:   logsink.LevelInfo,
			Message: "audit entry",
			Attrs: []logsink.Attr{
				logsink.String("scope", "acme"),
				logsink.Int64("sequence", int64(i+1)),
				logsink.Bool("ok", i%2 == 0),
			},
		})
	}

	return out
}

// expect checks that got holds the JSON lines of want, in order.
func expect(t *testing.T, got [][]byte, want []logsink.Record) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("delivered %d records, want %d", len(got), len(want))
	}

	for i := range want {
		line, err := logsink.MarshalJSON(want[i])
		if err != nil {
			t.Fatalf("marshal record %d: %v", i, err)
		}

		if !bytes.Equal(got[i], line) {
			t.Fatalf("record %d:\n got %s\nwant %s", i, got[i], line)
		}
	}
}

func testOrder(t *testing.T, s TestSink) {
	t.Helper()

	recs := sample(orderBatch)

	if err := s.Sink.Write(t.Context(), recs); err != nil {
		t.Fatalf("write: %v", err)
	}

	expect(t, s.Delivered(t, len(recs)), recs)
}

func testTwoBatches(t *testing.T, s TestSink) {
	t.Helper()

	recs := sample(twoBatchTotal)

	if err := s.Sink.Write(t.Context(), recs[:2]); err != nil {
		t.Fatalf("write first batch: %v", err)
	}

	if err := s.Sink.Write(t.Context(), recs[2:]); err != nil {
		t.Fatalf("write second batch: %v", err)
	}

	expect(t, s.Delivered(t, len(recs)), recs)
}

func testEmptyBatch(t *testing.T, s TestSink) {
	t.Helper()

	if err := s.Sink.Write(t.Context(), nil); err != nil {
		t.Fatalf("write empty batch: %v", err)
	}

	recs := sample(1)

	if err := s.Sink.Write(t.Context(), recs); err != nil {
		t.Fatalf("write: %v", err)
	}

	expect(t, s.Delivered(t, 1), recs)
}

func testSpecialCharacters(t *testing.T, s TestSink) {
	t.Helper()

	recs := []logsink.Record{{
		Time:    at(0),
		Level:   logsink.LevelWarn,
		Message: "line one\nline two \"quoted\" <tag> é ✓",
		Attrs: []logsink.Attr{
			logsink.String("path", "/outputs/db\tpassword"),
			{Key: "missing", Value: nil},
		},
	}}

	if err := s.Sink.Write(t.Context(), recs); err != nil {
		t.Fatalf("write: %v", err)
	}

	expect(t, s.Delivered(t, 1), recs)
}

func testWriteAfterClose(t *testing.T, s TestSink) {
	t.Helper()

	if err := s.Sink.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	err := s.Sink.Write(t.Context(), sample(1))
	if !errors.Is(err, logsink.ErrClosed) {
		t.Fatalf("write after close: got %v, want ErrClosed", err)
	}
}

func testCloseTwice(t *testing.T, s TestSink) {
	t.Helper()

	if err := s.Sink.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	if err := s.Sink.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func testDoneContext(t *testing.T, s TestSink) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := s.Sink.Write(ctx, sample(1))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("write with a done context: got %v, want context.Canceled", err)
	}
}
