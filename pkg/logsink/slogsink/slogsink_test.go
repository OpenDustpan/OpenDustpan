package slogsink_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/slogsink"
)

var errDown = errors.New("sink down")

// memSink keeps every record it accepts. While block is open, a write
// waits on it. While down, a write fails.
type memSink struct {
	block chan struct{}
	recs  []logsink.Record
	mu    sync.Mutex
	down  bool
}

func newMemSink() *memSink {
	return &memSink{block: nil, recs: nil, mu: sync.Mutex{}, down: false}
}

func (s *memSink) Write(ctx context.Context, records []logsink.Record) error {
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return fmt.Errorf("memory sink: %w", ctx.Err())
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.down {
		return errDown
	}

	s.recs = append(s.recs, records...)

	return nil
}

func (s *memSink) Close() error { return nil }

func (s *memSink) records() []logsink.Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]logsink.Record(nil), s.recs...)
}

// options returns options with a short flush interval and a drop counter.
func options(drops *atomic.Int64) slogsink.Options {
	return slogsink.Options{
		Level:         slog.LevelInfo,
		OnDrop:        func(n int) { drops.Add(int64(n)) },
		QueueSize:     0,
		BatchSize:     0,
		FlushInterval: 5 * time.Millisecond,
		WriteTimeout:  time.Second,
	}
}

// closeHandler closes h and fails the test on error. It does not use the
// test context, so it also works in a cleanup.
func closeHandler(t *testing.T, h *slogsink.Handler) {
	t.Helper()

	if err := h.Close(context.Background()); err != nil {
		t.Fatalf("close handler: %v", err)
	}
}

// attr returns the value of the attribute key of r, and whether r has it.
func attr(r logsink.Record, key string) (any, bool) {
	for _, a := range r.Attrs {
		if a.Key == key {
			return a.Value, true
		}
	}

	return nil, false
}

func TestRecordsReachTheSinkWithFlatAttributes(t *testing.T) {
	t.Parallel()

	sink := newMemSink()

	var drops atomic.Int64

	h := slogsink.New(sink, options(&drops))
	logger := slog.New(h).With(slog.String("adapter", "pulumi")).WithGroup("req")

	logger.Info("request", slog.Int("status", 200), slog.Group("user", slog.String("id", "admin")))
	logger.Debug("hidden")

	closeHandler(t, h)

	recs := sink.records()
	if len(recs) != 1 {
		t.Fatalf("records: got %d, want 1 (debug is below the level)", len(recs))
	}

	want := map[string]any{"adapter": "pulumi", "req.status": int64(200), "req.user.id": "admin"}
	for key, value := range want {
		got, ok := attr(recs[0], key)
		if !ok || got != value {
			t.Fatalf("attribute %s: got %v (%v), want %v", key, got, ok, value)
		}
	}

	if recs[0].Message != "request" || recs[0].Level != logsink.LevelInfo {
		t.Fatalf("record: got %q at %v", recs[0].Message, recs[0].Level)
	}

	if drops.Load() != 0 {
		t.Fatalf("drops: got %d, want 0", drops.Load())
	}
}

func TestValuesTakePlainForms(t *testing.T) {
	t.Parallel()

	sink := newMemSink()

	var drops atomic.Int64

	h := slogsink.New(sink, options(&drops))
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.FixedZone("x", 3600))

	slog.New(h).Info("values",
		slog.Duration("took", 1500*time.Millisecond),
		slog.Time("at", at),
		slog.Any("err", errDown),
		slog.Uint64("big", math.MaxUint64),
		slog.Float64("ratio", 0.5),
		slog.Bool("ok", true),
	)

	closeHandler(t, h)

	recs := sink.records()
	if len(recs) != 1 {
		t.Fatalf("records: got %d, want 1", len(recs))
	}

	want := map[string]any{
		"took":  int64(1500 * time.Millisecond),
		"at":    "2026-10-13T11:00:00Z",
		"err":   "sink down",
		"big":   "18446744073709551615",
		"ratio": 0.5,
		"ok":    true,
	}

	for key, value := range want {
		got, ok := attr(recs[0], key)
		if !ok || got != value {
			t.Fatalf("attribute %s: got %#v (%v), want %#v", key, got, ok, value)
		}
	}
}

func TestAFullQueueDropsAndCounts(t *testing.T) {
	t.Parallel()

	sink := newMemSink()
	sink.block = make(chan struct{})

	var drops atomic.Int64

	opts := options(&drops)
	opts.QueueSize = 2
	opts.BatchSize = 1
	h := slogsink.New(sink, opts)
	logger := slog.New(h)

	// The writer takes one record and waits on the sink. Two more fill the
	// queue. The rest drop, and Handle never waits.
	const sent = 20
	for i := range sent {
		logger.Info("burst", slog.Int("i", i))
	}

	if drops.Load() == 0 {
		t.Fatal("drops: got 0, want the records beyond the queue")
	}

	close(sink.block)
	closeHandler(t, h)

	if got := int64(len(sink.records())) + drops.Load(); got != sent {
		t.Fatalf("written plus dropped: got %d, want %d", got, sent)
	}
}

func TestAFailedWriteDropsAndCounts(t *testing.T) {
	t.Parallel()

	sink := newMemSink()
	sink.down = true

	var drops atomic.Int64

	h := slogsink.New(sink, options(&drops))
	logger := slog.New(h)

	logger.Info("one")
	logger.Info("two")

	closeHandler(t, h)

	if drops.Load() != 2 {
		t.Fatalf("drops: got %d, want 2", drops.Load())
	}
}

// skipSink accepts every batch and skips the records with the message
// "large", as a sink with a size cap does.
type skipSink struct{ inner *memSink }

func (s skipSink) Write(ctx context.Context, records []logsink.Record) error {
	var kept []logsink.Record

	for _, r := range records {
		if r.Message != "large" {
			kept = append(kept, r)
		}
	}

	if err := s.inner.Write(ctx, kept); err != nil {
		return err
	}

	if skipped := len(records) - len(kept); skipped > 0 {
		return &logsink.SkipError{Reason: "too large", Skipped: skipped}
	}

	return nil
}

func (s skipSink) Close() error { return nil }

func TestASkippedRecordCountsAsOneDrop(t *testing.T) {
	t.Parallel()

	sink := skipSink{inner: newMemSink()}

	var drops atomic.Int64

	opts := options(&drops)
	opts.FlushInterval = time.Hour
	h := slogsink.New(sink, opts)
	logger := slog.New(h)

	logger.Info("small")
	logger.Info("large")
	logger.Info("small")

	closeHandler(t, h)

	if drops.Load() != 1 || len(sink.inner.records()) != 2 {
		t.Fatalf("drops %d, written %d: want 1 and 2", drops.Load(), len(sink.inner.records()))
	}
}

func TestCloseFlushesAndRefusesLaterRecords(t *testing.T) {
	t.Parallel()

	sink := newMemSink()

	var drops atomic.Int64

	opts := options(&drops)
	opts.FlushInterval = time.Hour
	h := slogsink.New(sink, opts)
	logger := slog.New(h)

	logger.Info("before close")
	closeHandler(t, h)

	if len(sink.records()) != 1 {
		t.Fatalf("records after close: got %d, want 1", len(sink.records()))
	}

	logger.Info("after close")

	if drops.Load() != 1 {
		t.Fatalf("drops after close: got %d, want 1", drops.Load())
	}

	if err := h.Close(t.Context()); !errors.Is(err, slogsink.ErrClosed) {
		t.Fatalf("second close: got %v, want ErrClosed", err)
	}
}

func TestWithGroupEmptyNameIsTheSameHandler(t *testing.T) {
	t.Parallel()

	var drops atomic.Int64

	h := slogsink.New(newMemSink(), options(&drops))
	t.Cleanup(func() { closeHandler(t, h) })

	if h.WithGroup("") != slog.Handler(h) {
		t.Fatal("WithGroup with an empty name returned a new handler")
	}
}
