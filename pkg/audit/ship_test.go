package audit_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
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
			ID:                  namespace.NewID(),
			CreatedAt:           time.Unix(0, 0),
			Name:                name,
			PrimaryInstallation: "eu-west",
			FailoverVersion:     1,
			Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
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

func (f *memFeed) AdvanceSinkCursor(
	_ context.Context,
	fence namespace.Fence,
	_ durable.Claim,
	sink string,
	seq int64,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := sink + "/" + fence.Namespace.String()
	if seq > f.cursors[key] {
		f.cursors[key] = seq
	}

	return nil
}

// LastEntry answers the entry with the highest sequence of ns, and false
// when ns holds none. It meets the last-entry port of the shipper, so the
// shipper can count the delivery lag in entries.
func (f *memFeed) LastEntry(_ context.Context, ns namespace.Name) (audit.Entry, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := f.entries[ns]
	if len(all) == 0 {
		return audit.Entry{}, false, nil //nolint:exhaustruct_v5 // No entry to return.
	}

	return all[len(all)-1], true, nil
}

// acmeCursor answers the cursor of sink in the namespace acme.
func (f *memFeed) acmeCursor(sink string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.cursors[sink+"/acme"]
}

// scopeCursor answers the cursor of sink in ns.
func (f *memFeed) scopeCursor(sink string, ns namespace.Name) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.cursors[sink+"/"+ns.String()]
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
		At:            time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC).Add(time.Duration(seq) * time.Second),
		Namespace:     ns,
		Subject:       "alice",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "unit:read-secrets",
		Object:        "opentofu:" + ns.String() + "/prod",
		Version:       "sv-1",
		ValuePath:     "",
		ValueCount:    nil,
		ObjectID:      "",
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "OpenTofu/1.11",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindToolOperation,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "req-" + strconv.FormatInt(seq, 10),
		PrevHash:      "prev",
		Hash:          "hash-" + strconv.FormatInt(seq, 10),
		Sequence:      seq,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

func newShipper(t *testing.T, feed *memFeed, sinks ...audit.ShipSink) *audit.Shipper {
	t.Helper()

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    feed,
		Logger:        nil,
		OnShipped:     nil,
		OnFailure:     nil,
		OnSkipped:     nil,
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           nil,
		Sinks:         sinks,
		InstanceID:    "",
		Leases:        nil,
		LeaseTTL:      0,
		RenewInterval: 0,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	return s
}

func shipSink(name string, s logsink.Sink) audit.ShipSink {
	return audit.ShipSink{Sink: s, Name: name, Kind: "test", Interval: 0}
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

	return &logsink.SkipError{Reason: "too large", Skipped: 1, Indexes: []int{0}}
}

func (s skipSink) Close() error { return nil }

func TestASkippedEntryIsCountedAndTheCursorMoves(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 3})
	sink := skipSink{inner: &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}}

	var shipped, skipped int

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    feed,
		Logger:        nil,
		OnShipped:     func(_ string, n int) { shipped += n },
		OnFailure:     nil,
		OnSkipped:     func(_ string, n int) { skipped += n },
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           nil,
		Sinks:         []audit.ShipSink{shipSink("siem", sink)},
		InstanceID:    "",
		Leases:        nil,
		LeaseTTL:      0,
		RenewInterval: 0,
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
		OnFailure: func(string, string) {
			select {
			case failed <- struct{}{}:
			default:
			}
		},
		OnSkipped:    nil,
		OnLag:        nil,
		OnLagEntries: nil,
		Now:          nil,
		Sinks: []audit.ShipSink{
			{
				Sink:     &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false},
				Name:     "siem",
				Kind:     "test",
				Interval: time.Millisecond,
			},
		},
		InstanceID:    "",
		Leases:        nil,
		LeaseTTL:      0,
		RenewInterval: 0,
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
	shipper := newShipper(t, feed, audit.ShipSink{Sink: sink, Name: "siem", Kind: "test", Interval: time.Millisecond})

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
				Feed:          feed,
				Namespaces:    feed,
				Logger:        nil,
				OnShipped:     nil,
				OnFailure:     nil,
				OnSkipped:     nil,
				OnLag:         nil,
				OnLagEntries:  nil,
				Now:           nil,
				Sinks:         tc.sinks,
				InstanceID:    "",
				Leases:        nil,
				LeaseTTL:      0,
				RenewInterval: 0,
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

// TestToRecordCarriesTheObjectID proves that a record carries the stable ID
// of the unit of an entry (ADR 47) only when the entry has one.
func TestToRecordCarriesTheObjectID(t *testing.T) {
	t.Parallel()

	e := entryAt("acme", 10)
	e.ObjectID = "ws-1"

	if rec := audit.ToRecord(e); !hasAttr(rec, "objectID", "ws-1") {
		t.Fatalf("entry with an object ID: record lacks it: %+v", rec.Attrs)
	}

	if rec := audit.ToRecord(entryAt("acme", 11)); hasKey(rec, "objectID") {
		t.Fatalf("entry without an object ID: record holds one: %+v", rec.Attrs)
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

// memLease is the live claim of one sink in a memLeaser.
type memLease struct {
	operation durable.ID
	owner     string
	token     int64
	expires   time.Time
}

// memLeaser is an in-memory audit.SinkLeaser with the claim rule: one live
// claim per sink with its holder, a token that grows on each new claim and
// an expiry. A renew by the holder keeps the token.
type memLeaser struct {
	mu     sync.Mutex
	leases map[string]memLease
}

func newMemLeaser() *memLeaser {
	return &memLeaser{mu: sync.Mutex{}, leases: map[string]memLease{}}
}

func (l *memLeaser) AcquireSinkLease(
	_ context.Context,
	sink, holder string,
	ttl time.Duration,
) (durable.Claim, error) {
	if sink == "" || holder == "" || ttl <= 0 {
		return durable.Claim{}, fmt.Errorf("%w: sink %q holder %q", audit.ErrInvalidSinkLease, sink, holder)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	operation := durable.ID("op-sink-" + sink)

	if old, ok := l.leases[sink]; ok && old.owner != holder && now.Before(old.expires) {
		return durable.Claim{}, durable.ErrNoWork
	}

	token := int64(1)
	if old, ok := l.leases[sink]; ok {
		token = old.token
		if old.owner != holder || !now.Before(old.expires) {
			token = old.token + 1
		}
	}

	l.leases[sink] = memLease{operation: operation, owner: holder, token: token, expires: now.Add(ttl)}

	return durable.Claim{Operation: operation, Owner: holder, Token: token, ExpiresAt: now.Add(ttl)}, nil
}

func (l *memLeaser) ReleaseSinkLease(_ context.Context, claim durable.Claim) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	for sink, old := range l.leases {
		if old.operation != claim.Operation {
			continue
		}

		if old.token != claim.Token || time.Now().After(old.expires) {
			return durable.ErrClaimLost
		}

		delete(l.leases, sink)

		return nil
	}

	return durable.ErrClaimLost
}

func newLeasedShipper(t *testing.T, feed *memFeed, leases *memLeaser, holder string, sink logsink.Sink) *audit.Shipper {
	t.Helper()

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    feed,
		Logger:        nil,
		OnShipped:     nil,
		OnFailure:     nil,
		OnSkipped:     nil,
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           nil,
		Sinks:         []audit.ShipSink{{Sink: sink, Name: "siem", Kind: "test", Interval: 0}},
		InstanceID:    holder,
		Leases:        leases,
		LeaseTTL:      150 * time.Millisecond,
		RenewInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	return s
}

// newLeasedShipperFull builds a shipper with its own lease timing, ship
// interval, logger and failure hook.
func newLeasedShipperFull(t *testing.T, cfg leasedShipperConfig) *audit.Shipper {
	t.Helper()

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          cfg.feed,
		Namespaces:    cfg.feed,
		Logger:        cfg.logger,
		OnShipped:     nil,
		OnFailure:     cfg.onFailure,
		OnSkipped:     nil,
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           nil,
		Sinks:         []audit.ShipSink{{Sink: cfg.sink, Name: "siem", Kind: "test", Interval: cfg.interval}},
		InstanceID:    cfg.holder,
		Leases:        cfg.leases,
		LeaseTTL:      cfg.ttl,
		RenewInterval: cfg.renew,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	return s
}

// leasedShipperConfig carries the inputs of newLeasedShipperFull.
type leasedShipperConfig struct {
	feed      *memFeed
	leases    audit.SinkLeaser
	holder    string
	sink      logsink.Sink
	ttl       time.Duration
	renew     time.Duration
	interval  time.Duration
	logger    *slog.Logger
	onFailure func(string, string)
}

// failureCounter counts OnFailure calls across goroutines.
type failureCounter struct {
	mu sync.Mutex
	n  int
}

func (c *failureCounter) hook(string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.n++
}

func (c *failureCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.n
}

// lockedBuffer is a bytes buffer safe for a logger that writes from the
// shipper loop while the test reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("locked buffer write: %w", err)
	}

	return n, nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// debugLogger answers a logger that writes debug lines to logs.
func debugLogger(logs *lockedBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// waitUntil polls cond until it holds, the test context ends, or the
// deadline passes. It uses channels and timers, never sleeps.
func waitUntil(t *testing.T, deadline time.Time, what string, cond func() bool) {
	t.Helper()

	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	bound := time.NewTimer(time.Until(deadline))
	defer bound.Stop()

	for !cond() {
		select {
		case <-t.Context().Done():
			t.Fatal("test ended while it waited")
		case <-bound.C:
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

// TestTwoShippersShareOneSinkLease proves that two shippers on one feed and
// one lease store share one sink through its lease: only the holder ships,
// and when it stops renewing, the other takes over from the cursor.
// Delivery is at least once: after a lease is lost or a round fails, a sink
// can receive an entry twice.
func TestTwoShippersShareOneSinkLease(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 3})
	leases := newMemLeaser()
	sinkA := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}
	sinkB := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}

	first := newLeasedShipper(t, feed, leases, "ins-holder-a", sinkA)
	second := newLeasedShipper(t, feed, leases, "ins-holder-b", sinkB)

	shipInitialLeaseRounds(t, first, second)
	checkInitialLeaseShipment(t, sinkA, sinkB)
	appendAcmeEntries(feed, 4, 5)
	waitStandbyTakeover(t, second, sinkB)
	checkStandbyShipment(t, sinkB)
	checkNoDuplicateShipments(t, sinkA, sinkB, 5)
}

// shipInitialLeaseRounds runs one round on each shipper while the first holds
// the sink lease.
func shipInitialLeaseRounds(t *testing.T, first, second *audit.Shipper) {
	t.Helper()

	if err := first.ShipOnce(t.Context()); err != nil {
		t.Fatalf("first ship once: %v", err)
	}

	if err := second.ShipOnce(t.Context()); err != nil {
		t.Fatalf("second ship once while the lease is live: %v", err)
	}
}

// checkInitialLeaseShipment checks that only the holder shipped.
func checkInitialLeaseShipment(t *testing.T, sinkA, sinkB *memSink) {
	t.Helper()

	if got := sinkA.sequences(); fmt.Sprint(got) != "[acme/1 acme/2 acme/3]" {
		t.Fatalf("holder shipped %v, want [acme/1 acme/2 acme/3]", got)
	}

	if got := sinkB.sequences(); len(got) != 0 {
		t.Fatalf("standby shipped %v, want nothing while the lease is live", got)
	}
}

// appendAcmeEntries appends entries with the given sequences to acme.
func appendAcmeEntries(feed *memFeed, seqs ...int64) {
	feed.mu.Lock()
	defer feed.mu.Unlock()

	for _, seq := range seqs {
		feed.entries["acme"] = append(feed.entries["acme"], entryAt("acme", seq))
	}
}

// waitStandbyTakeover polls the standby shipper until it ships two entries
// or the deadline passes.
func waitStandbyTakeover(t *testing.T, second *audit.Shipper, sinkB *memSink) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		if err := second.ShipOnce(t.Context()); err != nil {
			t.Fatalf("second ship once: %v", err)
		}

		if len(sinkB.sequences()) == 2 {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("standby did not take over within 10 seconds: %v", sinkB.sequences())
		}

		waitForTakeover(t)
	}
}

// checkStandbyShipment checks that the standby shipped the new entries.
func checkStandbyShipment(t *testing.T, sinkB *memSink) {
	t.Helper()

	if got := sinkB.sequences(); fmt.Sprint(got) != "[acme/4 acme/5]" {
		t.Fatalf("standby shipped %v, want [acme/4 acme/5]", got)
	}
}

// checkNoDuplicateShipments checks that the two sinks shipped want entries
// together with none twice.
func checkNoDuplicateShipments(t *testing.T, sinkA, sinkB *memSink, want int) {
	t.Helper()

	combined := append(append([]string{}, sinkA.sequences()...), sinkB.sequences()...)
	seen := map[string]int{}
	for _, seq := range combined {
		seen[seq]++
	}

	for seq, n := range seen {
		if n != 1 {
			t.Fatalf("entry %s shipped %d times, want once: %v", seq, n, combined)
		}
	}

	if got := len(combined); got != want {
		t.Fatalf("shipped %d entries, want %d: %v", got, want, combined)
	}
}

// waitForShip waits a short time for a shipper round, or stops the test
// when its context ends.
func waitForShip(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()

	select {
	case <-t.Context().Done():
		t.Fatal("run did not ship within 5 seconds: test context done")
	case <-timer.C:
	}
}

// waitForTakeover waits a short time for a standby shipper to take a sink
// lease, or stops the test when its context ends.
func waitForTakeover(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-t.Context().Done():
		t.Fatal("standby did not take over within 10 seconds: test context done")
	case <-timer.C:
	}
}

// slowSink accepts its first batch and blocks later batches until ctx ends,
// without recording them. It proves that a round stops at its deadline.
type slowSink struct {
	inner *memSink
	mu    sync.Mutex
	calls int
}

func (s *slowSink) Write(ctx context.Context, records []logsink.Record) error {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()

	if n == 1 {
		return s.inner.Write(ctx, records)
	}

	<-ctx.Done()

	return fmt.Errorf("slow sink wait: %w", ctx.Err())
}

func (s *slowSink) Close() error { return nil }

// count answers how many batches the sink saw.
func (s *slowSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// loseOnceLeaser grants the first lease take and answers not held after
// that, as when another instance took the lease in the middle of a round.
type loseOnceLeaser struct {
	inner *memLeaser
	mu    sync.Mutex
	calls int
}

func (l *loseOnceLeaser) AcquireSinkLease(
	ctx context.Context,
	sink, holder string,
	ttl time.Duration,
) (durable.Claim, error) {
	l.mu.Lock()
	l.calls++
	n := l.calls
	l.mu.Unlock()

	if n == 1 {
		return l.inner.AcquireSinkLease(ctx, sink, holder, ttl)
	}

	return durable.Claim{}, durable.ErrNoWork
}

func (l *loseOnceLeaser) ReleaseSinkLease(ctx context.Context, claim durable.Claim) error {
	return l.inner.ReleaseSinkLease(ctx, claim)
}

// acquires answers how many lease takes the leaser saw.
func (l *loseOnceLeaser) acquires() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.calls
}

// TestASlowSinkStopsItsRoundAndDoesNotShipAfterTakeover proves that a sink
// slower than the lease stops its round at the round deadline: it keeps
// only its first batch and its cursor, and the next holder ships the rest.
// NewShipper keeps the renew interval shorter than the lease TTL, so the
// round timeout stays below the TTL. The standby runs at the same time as
// the slow round, and the test proves that the two shippers never ship the
// same entry.
func TestASlowSinkStopsItsRoundAndDoesNotShipAfterTakeover(t *testing.T) {
	t.Parallel()

	total := audit.ShipBatchSize + 1
	feed := newMemFeed(map[namespace.Name]int{"acme": total})
	leases := newMemLeaser()
	slow := &slowSink{
		inner: &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false},
		mu:    sync.Mutex{},
		calls: 0,
	}

	first := newLeasedShipper(t, feed, leases, "ins-holder-a", slow)

	ctx := t.Context()

	firstErr := make(chan error, 1)

	go func() {
		firstErr <- first.ShipOnce(ctx)
	}()

	// Wait until the slow round holds the lease and shipped its first batch,
	// so the standby below can only take over after the lease expires.
	waitUntil(t, time.Now().Add(10*time.Second), "the slow round to ship its first batch",
		func() bool { return slow.count() >= 1 })

	standbySink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}
	second := newLeasedShipper(t, feed, leases, "ins-holder-b", standbySink)

	waitTakeoverShipsLast(t, second, standbySink)

	if err := <-firstErr; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow ship once: got %v, want context.DeadlineExceeded", err)
	}

	checkTakeoverShipment(t, slow, feed, standbySink, total)
	checkNoDuplicateShipments(t, slow.inner, standbySink, total)
}

// waitTakeoverShipsLast polls the standby shipper until it ships one entry
// or the deadline passes.
func waitTakeoverShipsLast(t *testing.T, second *audit.Shipper, standbySink *memSink) {
	t.Helper()

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()

	tick := time.NewTimer(50 * time.Millisecond)
	defer tick.Stop()

	for len(standbySink.sequences()) != 1 {
		if err := second.ShipOnce(t.Context()); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("standby ship once: %v", err)
		}

		if len(standbySink.sequences()) == 1 {
			return
		}

		select {
		case <-t.Context().Done():
			t.Fatal("standby did not take over: test context done")
		case <-deadline.C:
			t.Fatalf("standby did not take over within 10 seconds: %v", standbySink.sequences())
		case <-tick.C:
			tick.Reset(50 * time.Millisecond)
		}
	}
}

// checkTakeoverShipment checks that the standby shipped the last entry and
// the slow sink shipped nothing after the takeover.
func checkTakeoverShipment(t *testing.T, slow *slowSink, feed *memFeed, standbySink *memSink, total int) {
	t.Helper()

	wantLast := fmt.Sprintf("[acme/%d]", total)
	if got := standbySink.sequences(); fmt.Sprint(got) != wantLast {
		t.Fatalf("standby shipped %v, want %s", got, wantLast)
	}

	if got := len(slow.inner.sequences()); got != audit.ShipBatchSize {
		t.Fatalf("slow sink shipped %d records after takeover, want %d", got, audit.ShipBatchSize)
	}

	if got := feed.acmeCursor("siem"); got != int64(total) {
		t.Fatalf("cursor after takeover: got %d, want %d", got, total)
	}
}

// TestALostRenewStopsTheRound proves that a renew in the middle of a round
// that answers not held stops the round at once: the second batch never
// ships and the cursor stays past only the first batch.
func TestALostRenewStopsTheRound(t *testing.T) {
	t.Parallel()

	total := audit.ShipBatchSize + 1
	feed := newMemFeed(map[namespace.Name]int{"acme": total})
	leases := &loseOnceLeaser{inner: newMemLeaser(), mu: sync.Mutex{}, calls: 0}
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    feed,
		Logger:        nil,
		OnShipped:     nil,
		OnFailure:     nil,
		OnSkipped:     nil,
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           nil,
		Sinks:         []audit.ShipSink{{Sink: sink, Name: "siem", Kind: "test", Interval: 0}},
		InstanceID:    "ins-holder-a",
		Leases:        leases,
		LeaseTTL:      30 * time.Second,
		RenewInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	if err := s.ShipOnce(t.Context()); err == nil {
		t.Fatal("ship once with a lost renew: got nil, want an error")
	}

	if got := len(sink.sequences()); got != audit.ShipBatchSize {
		t.Fatalf("shipped %d records after a lost renew, want %d", got, audit.ShipBatchSize)
	}

	if got := feed.acmeCursor("siem"); got != int64(audit.ShipBatchSize) {
		t.Fatalf("cursor after a lost renew: got %d, want %d", got, audit.ShipBatchSize)
	}
}

// TestShipperReleasesItsLeaseOnShutdown proves that a running shipper
// releases its sink lease when its context ends, so a standby takes over at
// once without waiting for expiry.
func TestShipperReleasesItsLeaseOnShutdown(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 1})
	leases := newMemLeaser()
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    feed,
		Logger:        nil,
		OnShipped:     nil,
		OnFailure:     nil,
		OnSkipped:     nil,
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           nil,
		Sinks:         []audit.ShipSink{{Sink: sink, Name: "siem", Kind: "test", Interval: time.Millisecond}},
		InstanceID:    "ins-holder-a",
		Leases:        leases,
		LeaseTTL:      30 * time.Second,
		RenewInterval: 10 * time.Millisecond,
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

	deadline := time.Now().Add(5 * time.Second)

	for feed.acmeCursor("siem") != 1 && time.Now().Before(deadline) {
		waitForShip(t)
	}

	if got := feed.acmeCursor("siem"); got != 1 {
		cancel()
		<-done
		t.Fatal("run did not ship within 5 seconds")
	}

	cancel()
	<-done

	claim, err := leases.AcquireSinkLease(t.Context(), "siem", "ins-holder-b", 30*time.Second)
	if err != nil {
		t.Fatalf("acquire after shutdown: %v", err)
	}

	if claim.Token == 0 || claim.Owner != "ins-holder-b" {
		t.Fatalf("lease after shutdown: got %+v, want the claim of ins-holder-b", claim)
	}
}

// TestALostRenewBetweenScopesStopsTheRound proves that the renew between
// scopes stops a round that lost its lease: the first scope ships and moves
// its cursor, the later scopes keep their cursors, and the next holder
// ships them. Without that renew the round would ship every scope after the
// lease was lost.
func TestALostRenewBetweenScopesStopsTheRound(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 1, "globex": 1})
	inner := newMemLeaser()
	leases := &loseOnceLeaser{inner: inner, mu: sync.Mutex{}, calls: 0}
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    feed,
		Logger:        nil,
		OnShipped:     nil,
		OnFailure:     nil,
		OnSkipped:     nil,
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           nil,
		Sinks:         []audit.ShipSink{{Sink: sink, Name: "siem", Kind: "test", Interval: 0}},
		InstanceID:    "ins-holder-a",
		Leases:        leases,
		LeaseTTL:      300 * time.Millisecond,
		RenewInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	if err := s.ShipOnce(t.Context()); err == nil {
		t.Fatal("ship once after losing the lease between scopes: got nil, want an error")
	}

	if got := sink.sequences(); fmt.Sprint(got) != "[acme/1]" {
		t.Fatalf("shipped %v, want [acme/1]", got)
	}

	if got := feed.scopeCursor("siem", "acme"); got != 1 {
		t.Fatalf("acme cursor after a lost renew: got %d, want 1", got)
	}

	if got := feed.scopeCursor("siem", "globex"); got != 0 {
		t.Fatalf("globex cursor after a lost renew: got %d, want 0", got)
	}

	standbySink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}
	second := newLeasedShipperFull(t, leasedShipperConfig{
		feed:      feed,
		leases:    inner,
		holder:    "ins-holder-b",
		sink:      standbySink,
		ttl:       300 * time.Millisecond,
		renew:     50 * time.Millisecond,
		interval:  0,
		logger:    nil,
		onFailure: nil,
	})

	waitTakeoverShipsLast(t, second, standbySink)

	if got := standbySink.sequences(); fmt.Sprint(got) != "[globex/1]" {
		t.Fatalf("standby shipped %v, want [globex/1]", got)
	}

	checkNoDuplicateShipments(t, sink, standbySink, 2)
}

// blockingReleaseLeaser is a memLeaser whose release blocks until its
// context ends. It records that the release ran and how long the release
// context allowed.
type blockingReleaseLeaser struct {
	inner     *memLeaser
	entered   chan struct{}
	mu        sync.Mutex
	remaining time.Duration
	seen      bool
}

func (l *blockingReleaseLeaser) AcquireSinkLease(
	ctx context.Context,
	sink, holder string,
	ttl time.Duration,
) (durable.Claim, error) {
	return l.inner.AcquireSinkLease(ctx, sink, holder, ttl)
}

func (l *blockingReleaseLeaser) ReleaseSinkLease(ctx context.Context, _ durable.Claim) error {
	l.mu.Lock()
	if dl, ok := ctx.Deadline(); ok {
		l.remaining, l.seen = time.Until(dl), true
	}
	l.mu.Unlock()

	select {
	case l.entered <- struct{}{}:
	default:
	}

	<-ctx.Done()

	return fmt.Errorf("sink lease release wait: %w", ctx.Err())
}

// TestShipperReleaseRespectsShipReleaseTimeout proves that the release of a
// sink lease at shutdown is bounded: against a leaser that blocks past
// ShipReleaseTimeout, the shipper still stops, after about the timeout.
func TestShipperReleaseRespectsShipReleaseTimeout(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 1})
	leases := &blockingReleaseLeaser{
		inner:     newMemLeaser(),
		entered:   make(chan struct{}, 1),
		mu:        sync.Mutex{},
		remaining: 0,
		seen:      false,
	}
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}

	s := newLeasedShipperFull(t, leasedShipperConfig{
		feed:      feed,
		leases:    leases,
		holder:    "ins-holder-a",
		sink:      sink,
		ttl:       150 * time.Millisecond,
		renew:     10 * time.Millisecond,
		interval:  time.Millisecond,
		logger:    nil,
		onFailure: nil,
	})

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		defer close(done)

		s.Run(ctx)
	}()

	waitUntil(t, time.Now().Add(10*time.Second), "the first batch to ship",
		func() bool { return feed.acmeCursor("siem") == 1 })

	start := time.Now()
	cancel()

	bound := time.NewTimer(audit.ShipReleaseTimeout + 10*time.Second)
	defer bound.Stop()

	select {
	case <-t.Context().Done():
		t.Fatal("test ended while the shipper shut down")
	case <-bound.C:
		t.Fatal("shipper blocked longer than ShipReleaseTimeout plus slack")
	case <-done:
	}

	if elapsed := time.Since(start); elapsed < audit.ShipReleaseTimeout {
		t.Fatalf("shutdown took %s, want at least ShipReleaseTimeout %s", elapsed, audit.ShipReleaseTimeout)
	}

	select {
	case <-leases.entered:
	default:
		t.Fatal("shipper stopped without releasing its sink lease")
	}

	leases.mu.Lock()
	remaining, seen := leases.remaining, leases.seen
	leases.mu.Unlock()

	if !seen {
		t.Fatal("release ran without a context deadline")
	}

	if remaining <= 0 || remaining > audit.ShipReleaseTimeout {
		t.Fatalf("release context allowed %s, want up to ShipReleaseTimeout %s", remaining, audit.ShipReleaseTimeout)
	}
}

// TestARoundDeadlineEndsCleanlyWithoutFailureOrBackoff proves that a round
// that hits its round deadline ends cleanly: no failure hook call, no
// warning, and the next round starts after the ship interval, not after a
// doubled backoff.
func TestARoundDeadlineEndsCleanlyWithoutFailureOrBackoff(t *testing.T) {
	t.Parallel()

	total := audit.ShipBatchSize + 1
	feed := newMemFeed(map[namespace.Name]int{"acme": total})
	leases := newMemLeaser()
	slow := &slowSink{
		inner: &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false},
		mu:    sync.Mutex{},
		calls: 0,
	}

	var logs lockedBuffer

	failures := &failureCounter{mu: sync.Mutex{}, n: 0}

	s := newLeasedShipperFull(t, leasedShipperConfig{
		feed:      feed,
		leases:    leases,
		holder:    "ins-holder-a",
		sink:      slow,
		ttl:       4 * time.Second,
		renew:     2500 * time.Millisecond,
		interval:  1500 * time.Millisecond,
		logger:    debugLogger(&logs),
		onFailure: failures.hook,
	})

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		defer close(done)

		s.Run(ctx)
	}()

	// The first round blocks in its second write until the round deadline.
	waitUntil(t, time.Now().Add(10*time.Second), "the slow round to block",
		func() bool { return slow.count() >= 2 })

	start := time.Now()

	// A clean round keeps the ship interval of 1.5 seconds, so the next
	// round starts its write about 3 seconds later. A failed round would
	// double its wait first and start about 4 seconds later.
	waitUntil(t, start.Add(3750*time.Millisecond), "the next round to start",
		func() bool { return slow.count() >= 3 })

	cancel()
	<-done

	if got := failures.count(); got != 0 {
		t.Fatalf("failure hook calls: got %d, want 0", got)
	}

	if out := logs.String(); !strings.Contains(out, "audit sink round ended") {
		t.Fatalf("log lacks the clean round end: %s", out)
	}

	if out := logs.String(); strings.Contains(out, "audit sink delivery failed") {
		t.Fatalf("log holds a delivery failure for a clean round end: %s", out)
	}

	if got := feed.acmeCursor("siem"); got != int64(audit.ShipBatchSize) {
		t.Fatalf("cursor after a round deadline: got %d, want %d", got, audit.ShipBatchSize)
	}

	if got := len(slow.inner.sequences()); got != audit.ShipBatchSize {
		t.Fatalf("slow sink shipped %d records, want %d", got, audit.ShipBatchSize)
	}
}

// TestALostLeaseEndsCleanlyWithoutFailureOrBackoff proves that a round that
// loses its sink lease ends cleanly: no failure hook call, no warning, and
// the loop takes the lease again after the ship interval, not after a
// doubled backoff.
func TestALostLeaseEndsCleanlyWithoutFailureOrBackoff(t *testing.T) {
	t.Parallel()

	total := audit.ShipBatchSize + 1
	feed := newMemFeed(map[namespace.Name]int{"acme": total})
	leases := &loseOnceLeaser{inner: newMemLeaser(), mu: sync.Mutex{}, calls: 0}
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}

	var logs lockedBuffer

	failures := &failureCounter{mu: sync.Mutex{}, n: 0}

	s := newLeasedShipperFull(t, leasedShipperConfig{
		feed:      feed,
		leases:    leases,
		holder:    "ins-holder-a",
		sink:      sink,
		ttl:       3 * time.Second,
		renew:     1800 * time.Millisecond,
		interval:  1200 * time.Millisecond,
		logger:    debugLogger(&logs),
		onFailure: failures.hook,
	})

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		defer close(done)

		s.Run(ctx)
	}()

	// The first round ships one batch, then loses its renew and ends.
	waitUntil(t, time.Now().Add(10*time.Second), "the clean round end to be logged",
		func() bool { return strings.Contains(logs.String(), "audit sink round ended") })

	start := time.Now()
	before := leases.acquires()

	// A clean round keeps the ship interval of 1.2 seconds, so the loop
	// takes the lease again about 1.2 seconds later. A failed round would
	// double its wait first and take it about 1.8 seconds later.
	waitUntil(t, start.Add(1600*time.Millisecond), "the next lease take",
		func() bool { return leases.acquires() > before })

	cancel()
	<-done

	if got := failures.count(); got != 0 {
		t.Fatalf("failure hook calls: got %d, want 0", got)
	}

	if out := logs.String(); strings.Contains(out, "audit sink delivery failed") {
		t.Fatalf("log holds a delivery failure for a clean round end: %s", out)
	}

	if got := feed.acmeCursor("siem"); got != int64(audit.ShipBatchSize) {
		t.Fatalf("cursor after a lost lease: got %d, want %d", got, audit.ShipBatchSize)
	}

	if got := len(sink.sequences()); got != audit.ShipBatchSize {
		t.Fatalf("shipped %d records after a lost lease, want %d", got, audit.ShipBatchSize)
	}
}

// timeoutSink fails every write at once with a wrapped deadline exceeded,
// as a sink with its own short timeout does well before the round deadline.
type timeoutSink struct {
	mu    sync.Mutex
	calls int
}

func (s *timeoutSink) Write(context.Context, []logsink.Record) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	return fmt.Errorf("sink write: %w", context.DeadlineExceeded)
}

func (s *timeoutSink) Close() error { return nil }

// count answers how many writes the sink saw.
func (s *timeoutSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// TestASinkTimeoutIsAFailure proves that a sink timeout while the round
// deadline is still ahead ends the round as a failure: the failure hook
// runs once, the log holds a delivery failure and no clean round end, and
// the next round waits out the doubled backoff instead of the ship interval.
func TestASinkTimeoutIsAFailure(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 1})
	leases := newMemLeaser()
	sink := &timeoutSink{mu: sync.Mutex{}, calls: 0}

	var logs lockedBuffer

	failures := &failureCounter{mu: sync.Mutex{}, n: 0}

	s := newLeasedShipperFull(t, leasedShipperConfig{
		feed:      feed,
		leases:    leases,
		holder:    "ins-holder-a",
		sink:      sink,
		ttl:       4 * time.Second,
		renew:     2500 * time.Millisecond,
		interval:  1500 * time.Millisecond,
		logger:    debugLogger(&logs),
		onFailure: failures.hook,
	})

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		defer close(done)

		s.Run(ctx)
	}()

	// The first round fails fast in its first write, well before the round
	// deadline of 1.5 seconds.
	waitUntil(t, time.Now().Add(10*time.Second), "the sink timeout to fail the round",
		func() bool { return failures.count() >= 1 })

	start := time.Now()
	writes := sink.count()

	if got := failures.count(); got != 1 {
		cancel()
		<-done
		t.Fatalf("failure hook calls: got %d, want 1", got)
	}

	if out := logs.String(); !strings.Contains(out, "audit sink delivery failed") {
		cancel()
		<-done
		t.Fatalf("log lacks a delivery failure for a sink timeout: %s", out)
	}

	if out := logs.String(); strings.Contains(out, "audit sink round ended") {
		cancel()
		<-done
		t.Fatalf("log holds a clean round end for a sink timeout: %s", out)
	}

	if got := feed.acmeCursor("siem"); got != 0 {
		cancel()
		<-done
		t.Fatalf("cursor after a sink timeout: got %d, want 0", got)
	}

	// A failed round doubles its wait to 3 seconds, so the loop waits out
	// the renew interval of 2.5 seconds. A clean round would keep the ship
	// interval of 1.5 seconds and start the next round by now.
	pause := time.NewTimer(2 * time.Second)
	defer pause.Stop()

	select {
	case <-t.Context().Done():
		cancel()
		<-done
		t.Fatal("test ended while it waited out the ship interval")
	case <-pause.C:
	}

	if got := sink.count(); got != writes {
		cancel()
		<-done
		t.Fatalf("writes after the ship interval: got %d, want %d", got, writes)
	}

	// The loop still retries with the doubled backoff.
	waitUntil(t, start.Add(5*time.Second), "the next round to start after backoff",
		func() bool { return sink.count() > writes })

	cancel()
	<-done

	if got := failures.count(); got < 1 {
		t.Fatalf("failure hook calls: got %d, want at least 1", got)
	}
}

// mixedSink fails the acme scope at once with a plain error and blocks the
// later scopes until the round deadline, so one round joins a real failure
// with a later deadline.
type mixedSink struct {
	mu      sync.Mutex
	failErr error
}

func (s *mixedSink) Write(ctx context.Context, records []logsink.Record) error {
	if len(records) > 0 && fmt.Sprint(records[0].Attrs[0].Value) == "acme" {
		return s.failErr
	}

	<-ctx.Done()

	return fmt.Errorf("mixed sink wait: %w", ctx.Err())
}

func (s *mixedSink) Close() error { return nil }

// TestAJoinedSinkFailureBeatsALaterRoundDeadline proves that a round that
// joins a real sink failure with a later round deadline still fails: the
// failure hook runs once, the log holds a delivery failure and no clean
// round end.
func TestAJoinedSinkFailureBeatsALaterRoundDeadline(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 1, "globex": 1})
	leases := newMemLeaser()
	sink := &mixedSink{mu: sync.Mutex{}, failErr: errSinkDown}

	var logs lockedBuffer

	failures := &failureCounter{mu: sync.Mutex{}, n: 0}

	s := newLeasedShipperFull(t, leasedShipperConfig{
		feed:      feed,
		leases:    leases,
		holder:    "ins-holder-a",
		sink:      sink,
		ttl:       800 * time.Millisecond,
		renew:     500 * time.Millisecond,
		interval:  400 * time.Millisecond,
		logger:    debugLogger(&logs),
		onFailure: failures.hook,
	})

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		defer close(done)

		s.Run(ctx)
	}()

	waitUntil(t, time.Now().Add(10*time.Second), "the joined failure to fail the round",
		func() bool { return failures.count() >= 1 })

	cancel()
	<-done

	if got := failures.count(); got != 1 {
		t.Fatalf("failure hook calls: got %d, want 1", got)
	}

	if out := logs.String(); !strings.Contains(out, "audit sink delivery failed") {
		t.Fatalf("log lacks a delivery failure for a joined sink failure: %s", out)
	}

	if out := logs.String(); strings.Contains(out, "audit sink round ended") {
		t.Fatalf("log holds a clean round end for a joined sink failure: %s", out)
	}
}

// TestNewShipperRejectsRenewIntervalNotShorterThanLeaseTTL proves that a
// renew interval at or past the lease TTL is rejected, while a shorter one
// is accepted.
func TestNewShipperRejectsRenewIntervalNotShorterThanLeaseTTL(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(nil)
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: false}

	cases := []struct {
		name  string
		ttl   time.Duration
		renew time.Duration
		valid bool
	}{
		{name: "renew equal to lease TTL", ttl: 150 * time.Millisecond, renew: 150 * time.Millisecond, valid: false},
		{name: "renew past lease TTL", ttl: 150 * time.Millisecond, renew: 200 * time.Millisecond, valid: false},
		{name: "renew shorter than lease TTL", ttl: 150 * time.Millisecond, renew: 50 * time.Millisecond, valid: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := audit.NewShipper(audit.ShipperConfig{
				Feed:          feed,
				Namespaces:    feed,
				Logger:        nil,
				OnShipped:     nil,
				OnFailure:     nil,
				OnSkipped:     nil,
				OnLag:         nil,
				OnLagEntries:  nil,
				Now:           nil,
				Sinks:         []audit.ShipSink{shipSink("siem", sink)},
				InstanceID:    "ins-test",
				Leases:        newMemLeaser(),
				LeaseTTL:      tc.ttl,
				RenewInterval: tc.renew,
			})

			if tc.valid {
				if err != nil {
					t.Fatalf("new shipper with a shorter renew interval: got %v, want nil", err)
				}
			} else if !errors.Is(err, audit.ErrInvalidLeaseTiming) {
				t.Fatalf("new shipper: got %v, want ErrInvalidLeaseTiming", err)
			}
		})
	}
}

// sizeSink refuses the records whose JSON line holds more than limit bytes,
// with the positions of the refused records. It delivers the rest, so a
// refused entry never blocks the entries after it.
type sizeSink struct {
	limit   int
	batches [][]logsink.Record
	mu      sync.Mutex
}

func (s *sizeSink) Write(_ context.Context, records []logsink.Record) error {
	var (
		kept    []logsink.Record
		indexes []int
	)

	for i, r := range records {
		line, err := logsink.MarshalJSON(r)
		if err != nil {
			return err
		}

		if len(line) > s.limit {
			indexes = append(indexes, i)

			continue
		}

		kept = append(kept, r)
	}

	s.mu.Lock()
	s.batches = append(s.batches, kept)
	s.mu.Unlock()

	if len(indexes) > 0 {
		return &logsink.SkipError{
			Reason:  fmt.Sprintf("record above %d bytes", s.limit),
			Skipped: len(indexes),
			Indexes: indexes,
		}
	}

	return nil
}

func (s *sizeSink) Close() error { return nil }

// sequences answers the scope and sequence of every accepted record.
func (s *sizeSink) sequences() []string {
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

// failure records one call of the failure hook: the sink name and the reason.
type failure struct {
	sink   string
	reason string
}

// failureHook collects the calls of the failure hook across goroutines.
type failureHook struct {
	mu  sync.Mutex
	got []failure
}

func (h *failureHook) hook(sink, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.got = append(h.got, failure{sink: sink, reason: reason})
}

func (h *failureHook) reasons() []failure {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]failure(nil), h.got...)
}

// TestARefusedEntryCountsAFailureWithItsSequenceAndTheNextEntryIsDelivered
// proves that an entry over the sink limit is not sent: the failure hook
// runs once with the size reason, one error log line holds the scope and
// sequence of the refused entry, the cursor moves past the batch, and the
// next entry is delivered unchanged.
func TestARefusedEntryCountsAFailureWithItsSequenceAndTheNextEntryIsDelivered(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 3})

	feed.mu.Lock()
	first := feed.entries["acme"]
	if len(first) == 0 {
		feed.mu.Unlock()
		t.Fatal("feed holds no entry in acme")
	}

	first[0].UserAgent = strings.Repeat("u", 300)
	feed.mu.Unlock()

	sink := &sizeSink{limit: 600, batches: nil, mu: sync.Mutex{}}

	var logs lockedBuffer

	failures := &failureHook{mu: sync.Mutex{}, got: nil}

	var shipped, skipped int

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    feed,
		Logger:        slog.New(slog.NewJSONHandler(&logs, nil)),
		OnShipped:     func(_ string, n int) { shipped += n },
		OnFailure:     failures.hook,
		OnSkipped:     func(_ string, n int) { skipped += n },
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           nil,
		Sinks:         []audit.ShipSink{shipSink("siem", sink)},
		InstanceID:    "",
		Leases:        nil,
		LeaseTTL:      0,
		RenewInterval: 0,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	if err := s.ShipOnce(t.Context()); err != nil {
		t.Fatalf("ship once: %v", err)
	}

	if got := sink.sequences(); fmt.Sprint(got) != "[acme/2 acme/3]" {
		t.Fatalf("shipped %v, want [acme/2 acme/3]", got)
	}

	if got := feed.acmeCursor("siem"); got != 3 {
		t.Fatalf("cursor after a refusal: got %d, want 3", got)
	}

	if shipped != 2 || skipped != 1 {
		t.Fatalf("counts: shipped %d, skipped %d, want 2 and 1", shipped, skipped)
	}

	if got := failures.reasons(); fmt.Sprint(got) != "[{siem size}]" {
		t.Fatalf("failures: got %v, want [{siem size}]", got)
	}

	out := logs.String()
	for _, want := range []string{
		`"level":"ERROR"`,
		`"msg":"audit sink refused entry that it cannot hold"`,
		`"scope":"acme"`,
		`"sequence":1`,
		`"sink":"siem"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %s:\n%s", want, out)
		}
	}
}

// dialFailSink fails every write with a dial error, as a sink whose receiver
// is down does on its first write.
type dialFailSink struct{}

func (dialFailSink) Write(context.Context, []logsink.Record) error {
	//nolint:exhaustruct_v5 // The test needs only the dial operation.
	return &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errSinkDown,
	}
}

func (dialFailSink) Close() error { return nil }

// TestFailureReasons proves that each failed delivery counts with its
// reason: size for a refused entry, unreachable for a failed dial, and write
// for a failed batch write.
func TestFailureReasons(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		sink   logsink.Sink
		reason string
	}{
		{name: "failed write", sink: &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: true}, reason: "write"},
		{name: "failed dial", sink: dialFailSink{}, reason: "unreachable"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			feed := newMemFeed(map[namespace.Name]int{"acme": 1})
			failures := &failureHook{mu: sync.Mutex{}, got: nil}

			s, err := audit.NewShipper(audit.ShipperConfig{
				Feed:          feed,
				Namespaces:    feed,
				Logger:        nil,
				OnShipped:     nil,
				OnFailure:     failures.hook,
				OnSkipped:     nil,
				OnLag:         nil,
				OnLagEntries:  nil,
				Now:           nil,
				Sinks:         []audit.ShipSink{shipSink("siem", tc.sink)},
				InstanceID:    "",
				Leases:        nil,
				LeaseTTL:      0,
				RenewInterval: 0,
			})
			if err != nil {
				t.Fatalf("new shipper: %v", err)
			}

			if err := s.ShipOnce(t.Context()); err == nil {
				t.Fatal("ship once to a failing sink: got nil, want an error")
			}

			if got := failures.reasons(); fmt.Sprint(got) != fmt.Sprint([]failure{{sink: "siem", reason: tc.reason}}) {
				t.Fatalf("failures: got %v, want [{siem %s}]", got, tc.reason)
			}

			if got := feed.acmeCursor("siem"); got != 0 {
				t.Fatalf("cursor after a failed write: got %d, want 0", got)
			}
		})
	}
}
