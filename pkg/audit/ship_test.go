package audit_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var errSinkDown = errors.New("sink down")

// memFeed is an in-memory audit.Feed and audit.NamespaceLister.
type memFeed struct {
	entries map[namespace.Name][]audit.Entry
	cursors map[string]int64
	nss     []namespace.Namespace
	mu      sync.Mutex
}

func newMemFeed(counts map[namespace.Name]int) *memFeed {
	f := &memFeed{entries: map[namespace.Name][]audit.Entry{}, cursors: map[string]int64{}, nss: nil, mu: sync.Mutex{}}

	for _, name := range []namespace.Name{"acme", "globex"} {
		n, ok := counts[name]
		if !ok {
			continue
		}

		f.nss = append(f.nss, namespace.Namespace{
			CreatedAt:       time.Unix(0, 0),
			Name:            name,
			HomeRegion:      "eu-west",
			FailoverVersion: 1,
			Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
		})

		for i := range n {
			f.entries[name] = append(f.entries[name], entryAt(name, int64(i+1)))
		}
	}

	return f
}

func (f *memFeed) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]namespace.Namespace(nil), f.nss...), nil
}

func (f *memFeed) After(_ context.Context, ns namespace.Name, after int64, limit int) ([]audit.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []audit.Entry

	all := f.entries[ns]
	for i := range all {
		if all[i].Sequence > after && len(out) < limit {
			out = append(out, all[i])
		}
	}

	return out, nil
}

func (f *memFeed) SinkCursor(_ context.Context, sink string, ns namespace.Name) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.cursors[sink+"/"+ns.String()], nil
}

func (f *memFeed) AdvanceSinkCursor(_ context.Context, fence namespace.Fence, sink string, seq int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := sink + "/" + fence.Namespace.String()
	if seq > f.cursors[key] {
		f.cursors[key] = seq
	}

	return nil
}

// acmeCursor answers the cursor of sink in the namespace acme.
func (f *memFeed) acmeCursor(sink string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.cursors[sink+"/acme"]
}

// memSink keeps each batch it accepts. While down, it refuses every batch.
// It refuses every batch of the scope refuse.
type memSink struct {
	refuse  string
	batches [][]logsink.Record
	mu      sync.Mutex
	down    bool
}

func (s *memSink) Write(_ context.Context, records []logsink.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.down || (len(records) > 0 && fmt.Sprint(records[0].Attrs[0].Value) == s.refuse) {
		return errSinkDown
	}

	s.batches = append(s.batches, append([]logsink.Record(nil), records...))

	return nil
}

func (s *memSink) Close() error { return nil }

func (s *memSink) setDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.down = down
}

// sequences answers the scope and sequence of every accepted record.
func (s *memSink) sequences() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []string

	for _, b := range s.batches {
		for _, r := range b {
			out = append(out, fmt.Sprint(r.Attrs[0].Value)+"/"+fmt.Sprint(r.Attrs[1].Value))
		}
	}

	return out
}

func (s *memSink) batchSizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]int, 0, len(s.batches))
	for _, b := range s.batches {
		out = append(out, len(b))
	}

	return out
}

// entryAt returns a stored entry of ns with sequence seq and a hash.
func entryAt(ns namespace.Name, seq int64) audit.Entry {
	return audit.Entry{
		At:         time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC).Add(time.Duration(seq) * time.Second),
		Namespace:  ns,
		Subject:    "alice",
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:read-secrets",
		Object:     "opentofu:" + ns.String() + "/prod",
		Version:    "sv-1",
		ValuePath:  "",
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  "OpenTofu/1.11",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindToolOperation,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "req-" + strconv.FormatInt(seq, 10),
		PrevHash:   "prev",
		Hash:       "hash-" + strconv.FormatInt(seq, 10),
		Sequence:   seq,
	}
}

func newShipper(t *testing.T, feed *memFeed, sinks ...audit.ShipSink) *audit.Shipper {
	t.Helper()

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:       feed,
		Namespaces: feed,
		Logger:     nil,
		OnShipped:  nil,
		OnFailure:  nil,
		OnSkipped:  nil,
		Sinks:      sinks,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	return s
}

func shipSink(name string, s logsink.Sink) audit.ShipSink {
	return audit.ShipSink{Sink: s, Name: name, Interval: 0}
}

func TestShipOnceCopiesEveryEntryInOrderAndMovesTheCursors(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 3, "globex": 2})
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}
	shipper := newShipper(t, feed, shipSink("siem", sink))

	if err := shipper.ShipOnce(t.Context()); err != nil {
		t.Fatalf("ship once: %v", err)
	}

	want := []string{"acme/1", "acme/2", "acme/3", "globex/1", "globex/2"}
	if got := sink.sequences(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("shipped %v, want %v", got, want)
	}

	if got := feed.acmeCursor("siem"); got != 3 {
		t.Fatalf("acme cursor: got %d, want 3", got)
	}

	if err := shipper.ShipOnce(t.Context()); err != nil {
		t.Fatalf("second ship once: %v", err)
	}

	if got := len(sink.sequences()); got != len(want) {
		t.Fatalf("second round shipped again: %d records, want %d", got, len(want))
	}
}

func TestAFailedWriteKeepsTheCursorAndTheNextRoundSendsTheBatchAgain(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 2})
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: true}
	shipper := newShipper(t, feed, shipSink("siem", sink))

	if err := shipper.ShipOnce(t.Context()); !errors.Is(err, errSinkDown) {
		t.Fatalf("ship to a down sink: got %v, want errSinkDown", err)
	}

	if got := feed.acmeCursor("siem"); got != 0 {
		t.Fatalf("cursor after a failed write: got %d, want 0", got)
	}

	sink.setDown(false)

	if err := shipper.ShipOnce(t.Context()); err != nil {
		t.Fatalf("ship once: %v", err)
	}

	if got := sink.sequences(); fmt.Sprint(got) != "[acme/1 acme/2]" {
		t.Fatalf("shipped %v, want [acme/1 acme/2]", got)
	}
}

func TestAFailedScopeDoesNotStopTheLaterScopes(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 2, "globex": 2})
	sink := &memSink{refuse: "acme", batches: nil, mu: sync.Mutex{}, down: false}
	shipper := newShipper(t, feed, shipSink("siem", sink))

	if err := shipper.ShipOnce(t.Context()); !errors.Is(err, errSinkDown) {
		t.Fatalf("ship with a refused scope: got %v, want errSinkDown", err)
	}

	if got := sink.sequences(); fmt.Sprint(got) != "[globex/1 globex/2]" {
		t.Fatalf("shipped %v, want [globex/1 globex/2]", got)
	}

	if got := feed.acmeCursor("siem"); got != 0 {
		t.Fatalf("acme cursor after a refused batch: got %d, want 0", got)
	}
}

// skipSink accepts every batch and skips its first record.
type skipSink struct{ inner *memSink }

func (s skipSink) Write(ctx context.Context, records []logsink.Record) error {
	if err := s.inner.Write(ctx, records[1:]); err != nil {
		return err
	}

	return &logsink.SkipError{Reason: "too large", Skipped: 1}
}

func (s skipSink) Close() error { return nil }

func TestASkippedEntryIsCountedAndTheCursorMoves(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 3})
	sink := skipSink{inner: &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}}

	var shipped, skipped int

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:       feed,
		Namespaces: feed,
		Logger:     nil,
		OnShipped:  func(_ string, n int) { shipped += n },
		OnFailure:  nil,
		OnSkipped:  func(_ string, n int) { skipped += n },
		Sinks:      []audit.ShipSink{shipSink("siem", sink)},
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	if err := s.ShipOnce(t.Context()); err != nil {
		t.Fatalf("ship once: %v", err)
	}

	if got := feed.acmeCursor("siem"); got != 3 {
		t.Fatalf("cursor after a skip: got %d, want 3", got)
	}

	if shipped != 2 || skipped != 1 {
		t.Fatalf("counts: shipped %d, skipped %d, want 2 and 1", shipped, skipped)
	}
}

// failingFeed is a memFeed whose After fails with err.
type failingFeed struct {
	*memFeed

	err error
}

func (f failingFeed) After(context.Context, namespace.Name, int64, int) ([]audit.Entry, error) {
	return nil, f.err
}

func TestAFailedRoundLogsAClassNotTheDriverText(t *testing.T) {
	t.Parallel()

	const canary = "canary-driver-9f8e7d6c5b4a"

	base := newMemFeed(map[namespace.Name]int{"acme": 1, "globex": 1})
	feed := failingFeed{
		memFeed: base,
		err:     fmt.Errorf("%w: pq: connection to 10.0.0.7 reset (%s)", errSinkDown, canary),
	}

	var logs bytes.Buffer

	failed := make(chan struct{}, 1)

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:       feed,
		Namespaces: base,
		Logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		OnShipped:  nil,
		OnFailure: func(string) {
			select {
			case failed <- struct{}{}:
			default:
			}
		},
		OnSkipped: nil,
		Sinks: []audit.ShipSink{
			{
				Sink:     &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false},
				Name:     "siem",
				Interval: time.Millisecond,
			},
		},
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		defer close(done)

		s.Run(ctx)
	}()

	<-failed
	cancel()
	<-done

	out := logs.String()
	if strings.Contains(out, canary) || strings.Contains(out, "10.0.0.7") {
		t.Fatalf("log holds the driver text: %s", out)
	}

	if !strings.Contains(out, `"error":"read entries: failed"`) || !strings.Contains(out, `"scopes":3`) {
		t.Fatalf("log lacks the error class and the scope count: %s", out)
	}
}

func TestErrorClass(t *testing.T) {
	t.Parallel()

	cases := map[string]error{
		"round: canceled":              context.Canceled,
		"round: timeout":               fmt.Errorf("dial: %w", context.DeadlineExceeded),
		"round: refused by the target": fmt.Errorf("webhook: %w", logsink.ErrRefused),
		"round: sink closed":           logsink.ErrClosed,
		"round: fenced":                namespace.ErrFenced,
		"round: failed":                errSinkDown,
	}

	for want, err := range cases {
		if got := audit.ErrorClass(err); got != want {
			t.Fatalf("class of %v: got %q, want %q", err, got, want)
		}
	}
}

func TestEachSinkKeepsItsOwnCursor(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 2})
	up := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}
	down := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: true}
	shipper := newShipper(t, feed, shipSink("archive", down), shipSink("siem", up))

	if err := shipper.ShipOnce(t.Context()); !errors.Is(err, errSinkDown) {
		t.Fatalf("ship once: got %v, want errSinkDown for the down sink", err)
	}

	if got := feed.acmeCursor("siem"); got != 2 {
		t.Fatalf("siem cursor: got %d, want 2", got)
	}

	if got := feed.acmeCursor("archive"); got != 0 {
		t.Fatalf("archive cursor: got %d, want 0", got)
	}
}

func TestALargeBacklogShipsInBatches(t *testing.T) {
	t.Parallel()

	total := 2*audit.ShipBatchSize + 7
	feed := newMemFeed(map[namespace.Name]int{"acme": total})
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}
	shipper := newShipper(t, feed, shipSink("siem", sink))

	if err := shipper.ShipOnce(t.Context()); err != nil {
		t.Fatalf("ship once: %v", err)
	}

	want := fmt.Sprint([]int{audit.ShipBatchSize, audit.ShipBatchSize, 7})
	if got := fmt.Sprint(sink.batchSizes()); got != want {
		t.Fatalf("batch sizes: got %s, want %s", got, want)
	}

	if got := feed.acmeCursor("siem"); got != int64(total) {
		t.Fatalf("cursor: got %d, want %d", got, total)
	}
}

func TestRunShipsOnItsIntervalAndStopsWithTheContext(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 1})
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}
	shipper := newShipper(t, feed, audit.ShipSink{Sink: sink, Name: "siem", Interval: time.Millisecond})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		shipper.Run(ctx)
		close(done)
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for feed.acmeCursor("siem") != 1 {
		select {
		case <-deadline.C:
			t.Fatal("run did not ship within 5 seconds")
		case <-tick.C:
		}
	}

	cancel()
	<-done
}

func TestNewShipperRejectsBadSinks(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(nil)
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}

	cases := []struct {
		name  string
		sinks []audit.ShipSink
	}{
		{name: "empty name", sinks: []audit.ShipSink{shipSink("", sink)}},
		{name: "upper case name", sinks: []audit.ShipSink{shipSink("SIEM", sink)}},
		{name: "duplicate name", sinks: []audit.ShipSink{shipSink("siem", sink), shipSink("siem", sink)}},
		{name: "nil sink", sinks: []audit.ShipSink{shipSink("siem", nil)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := audit.NewShipper(audit.ShipperConfig{
				Feed:       feed,
				Namespaces: feed,
				Logger:     nil,
				OnShipped:  nil,
				OnFailure:  nil,
				OnSkipped:  nil,
				Sinks:      tc.sinks,
			})
			if !errors.Is(err, audit.ErrInvalidShipper) {
				t.Fatalf("new shipper: got %v, want ErrInvalidShipper", err)
			}
		})
	}
}

func TestToRecordCarriesTheChainOnlyWhenTheEntryHasIt(t *testing.T) {
	t.Parallel()

	chained := audit.ToRecord(entryAt("acme", 4))
	if !hasAttr(chained, "hash", "hash-4") || !hasAttr(chained, "prevHash", "prev") {
		t.Fatalf("chained entry: record lacks the chain: %+v", chained.Attrs)
	}

	plain := entryAt("acme", 5)
	plain.PrevHash, plain.Hash = "", ""

	if rec := audit.ToRecord(plain); hasKey(rec, "hash") || hasKey(rec, "prevHash") {
		t.Fatalf("entry without a chain: record holds chain fields: %+v", rec.Attrs)
	}

	count := int64(3)
	withCount := entryAt("acme", 6)
	withCount.ValueCount = &count

	if rec := audit.ToRecord(withCount); !hasAttr(rec, "valueCount", int64(3)) {
		t.Fatalf("entry with a value count: record lacks it: %+v", rec.Attrs)
	}

	if rec := audit.ToRecord(plain); hasKey(rec, "valueCount") {
		t.Fatalf("entry without a value count: record holds one: %+v", rec.Attrs)
	}
}

// TestToRecordCarriesAReadAccessEntry proves that an entry of the kind
// read-access (ADR 52 decision 13) reaches a sink with its kind and its
// value path, in a namespace and in @server.
func TestToRecordCarriesAReadAccessEntry(t *testing.T) {
	t.Parallel()

	for _, scope := range []namespace.Name{"acme", audit.ServerScope} {
		e := entryAt(scope, 7)
		e.Kind, e.ValuePath = audit.KindReadAccess, "/history"

		rec := audit.ToRecord(e)
		if !hasAttr(rec, "kind", "read-access") || !hasAttr(rec, "valuePath", "/history") {
			t.Fatalf("read-access entry in %s: record lacks its kind or value path: %+v", scope, rec.Attrs)
		}
	}
}

// TestToRecordCarriesChanges proves that a record carries the changes of an
// entry (ADR 63) as their canonical JSON, after valueCount, and that an
// entry without changes has no changes attribute.
func TestToRecordCarriesChanges(t *testing.T) {
	t.Parallel()

	old := "eu"
	e := entryAt("acme", 8)
	e.Kind = audit.KindChange
	e.Changes = []audit.Change{
		{Key: "region", Old: &old, New: "us <b>", Layer: "file", TakesEffect: "next start"},
		{Key: "database", Old: nil, New: "set", Layer: "env", TakesEffect: ""},
	}

	rec := audit.ToRecord(e)

	line, err := logsink.MarshalJSON(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}

	want := `"changes":` + audit.EncodeChanges(e.Changes)
	if !strings.Contains(string(line), want) {
		t.Fatalf("record line lacks %s: %s", want, line)
	}

	if rec := audit.ToRecord(entryAt("acme", 9)); hasKey(rec, "changes") {
		t.Fatalf("entry without changes: record holds them: %+v", rec.Attrs)
	}
}

func hasKey(r logsink.Record, key string) bool {
	for _, a := range r.Attrs {
		if a.Key == key {
			return true
		}
	}

	return false
}

func hasAttr(r logsink.Record, key string, value any) bool {
	for _, a := range r.Attrs {
		if a.Key == key && a.Value == value {
			return true
		}
	}

	return false
}
