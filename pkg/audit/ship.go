package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The shipper copies committed entries to log sinks (ADR 60 decision 13).
// The SQL table stays the record of the audit log. A sink receives a copy.
//
// For each sink and namespace the shipper keeps a cursor in the database:
// the highest sequence that the sink holds. Each round reads the entries
// above the cursor in order of sequence, writes them to the sink in one
// batch, and moves the cursor past the batch only after the sink accepted
// it. A restart or another instance goes on from the cursor. A failed
// write leaves the cursor, so the next round sends the batch again: a sink
// can receive an entry twice, and the receiver removes duplicates by scope
// and sequence. Each instance of the server runs the shipper, so a sink
// receives each entry about once per instance. The shipper never drops an
// entry, except one that a sink cannot hold (logsink.ErrSkipped): it
// counts and logs that skip, and goes on.

// Shipper defaults and limits.
const (
	// DefaultShipInterval is the time between two rounds of one sink when
	// its ShipSink names none.
	DefaultShipInterval = 5 * time.Second
	// MaxShipBackoff caps the wait after failed rounds.
	MaxShipBackoff = 5 * time.Minute
	// ShipBatchSize caps the entries of one namespace in one sink write.
	ShipBatchSize = 500
	// shipBackoffFactor multiplies the wait after each failed round.
	shipBackoffFactor = 2
	// maxSinkNameLen caps a sink name, which is a cursor key and a metric
	// label value.
	maxSinkNameLen = 64
)

// Record attribute keys and the message of a shipped entry. The keys match
// the JSON names of the audit API, plus RecordScope for the namespace.
const (
	RecordMessage = "audit entry"
	// RecordScope is the attribute key of the scope of a shipped entry.
	RecordScope = "scope"
)

// ErrInvalidShipper reports a shipper configuration that fails validation.
var ErrInvalidShipper = errors.New("invalid audit shipper configuration")

// Feed is the port the shipper reads entries and keeps its cursors
// through. The SQL stores implement it next to Store.
type Feed interface {
	// After answers at most limit entries of ns with a sequence above
	// after, oldest first. A namespace with no such entries answers an
	// empty slice.
	After(ctx context.Context, ns namespace.Name, after int64, limit int) ([]Entry, error)
	// SinkCursor answers the highest sequence of ns that the sink named
	// sink holds, or 0 when the sink holds no entry of ns.
	SinkCursor(ctx context.Context, sink string, ns namespace.Name) (int64, error)
	// AdvanceSinkCursor sets the cursor of sink in the namespace of fence
	// to seq. A cursor that is already at seq or higher stays, so two
	// instances that ship the same batch never move a cursor back. It
	// returns namespace.ErrNotFound if the fence names a missing namespace
	// and namespace.ErrFenced if the fence carries a stale failover version.
	// A fence that names ServerScope has no failover version: the store
	// moves that cursor with no fence check.
	AdvanceSinkCursor(ctx context.Context, fence namespace.Fence, sink string, seq int64) error
}

// NamespaceLister lists the namespaces whose entries the shipper copies.
type NamespaceLister interface {
	// ListNamespaces returns every namespace, ordered by name.
	ListNamespaces(ctx context.Context) ([]namespace.Namespace, error)
}

// ShipSink is one sink of the shipper. Name keys the cursors of the sink
// in the database and labels its metrics: keep it when the sink setting
// changes, or the sink receives every entry again. Interval is the time
// between two rounds. Zero means DefaultShipInterval.
type ShipSink struct {
	Sink     logsink.Sink
	Name     string
	Interval time.Duration
}

// ShipperConfig configures a Shipper. OnShipped, OnFailure and OnSkipped
// are hooks for metrics and may be nil. OnShipped receives the sink name
// and the count of entries of one accepted batch. OnFailure receives the
// sink name after a failed round. OnSkipped receives the sink name and the
// count of entries that the sink skipped (logsink.ErrSkipped).
type ShipperConfig struct {
	Feed       Feed
	Namespaces NamespaceLister
	Logger     *slog.Logger
	OnShipped  func(sink string, entries int)
	OnFailure  func(sink string)
	OnSkipped  func(sink string, entries int)
	Sinks      []ShipSink
}

// Shipper copies committed entries to sinks.
type Shipper struct {
	cfg ShipperConfig
}

// NewShipper checks cfg and returns a shipper. Every sink needs a name of
// at most 64 bytes of [a-z0-9._-], and no two sinks share a name.
func NewShipper(cfg ShipperConfig) (*Shipper, error) {
	if cfg.Feed == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: missing feed or namespace lister", ErrInvalidShipper)
	}

	seen := make(map[string]bool, len(cfg.Sinks))

	for _, s := range cfg.Sinks {
		if s.Sink == nil {
			return nil, fmt.Errorf("%w: sink %q is nil", ErrInvalidShipper, s.Name)
		}

		if !validSinkName(s.Name) {
			return nil, fmt.Errorf("%w: sink name %q: want 1 to %d bytes of a-z, 0-9, '.', '_' or '-'",
				ErrInvalidShipper, s.Name, maxSinkNameLen)
		}

		if seen[s.Name] {
			return nil, fmt.Errorf("%w: two sinks are named %q", ErrInvalidShipper, s.Name)
		}

		seen[s.Name] = true
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	return &Shipper{cfg: cfg}, nil
}

// serverScope answers ServerScope as a namespace value: its fence has no
// failover version, so the store moves its cursor with no fence check.
func serverScope() namespace.Namespace {
	return namespace.Namespace{
		CreatedAt:       time.Time{},
		Name:            ServerScope,
		HomeRegion:      "",
		FailoverVersion: 0,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}
}

// validSinkName reports whether name is a usable sink name.
func validSinkName(name string) bool {
	if name == "" || len(name) > maxSinkNameLen {
		return false
	}

	for _, c := range []byte(name) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return false
		}
	}

	return true
}

// Run ships to every sink, each on its own schedule, until ctx is done. It
// returns after every sink loop has stopped. It does not close the sinks:
// their owner closes them.
func (s *Shipper) Run(ctx context.Context) {
	var wg sync.WaitGroup

	for _, sink := range s.cfg.Sinks {
		wg.Go(func() { s.loop(ctx, sink) })
	}

	wg.Wait()
}

// ShipOnce runs one round for every sink: it copies every entry above the
// cursors. It answers the joined errors of the failed sinks.
func (s *Shipper) ShipOnce(ctx context.Context) error {
	var errs []error

	for _, sink := range s.cfg.Sinks {
		if err := s.round(ctx, sink); err != nil {
			errs = append(errs, fmt.Errorf("sink %s: %w", sink.Name, err))
		}
	}

	return errors.Join(errs...)
}

// loop runs rounds for sink until ctx is done. After a failed round it
// waits twice as long as before, up to MaxShipBackoff.
func (s *Shipper) loop(ctx context.Context, sink ShipSink) {
	interval := sink.Interval
	if interval <= 0 {
		interval = DefaultShipInterval
	}

	wait := interval

	for {
		timer := time.NewTimer(wait)

		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case <-timer.C:
		}

		if err := s.round(ctx, sink); err != nil {
			if ctx.Err() != nil {
				return
			}

			s.fail(ctx, sink.Name, err)

			wait = min(wait*shipBackoffFactor, MaxShipBackoff)

			continue
		}

		wait = interval
	}
}

// fail logs a failed round and calls the failure hook. The log line holds
// the sink name and the error, never an entry.
func (s *Shipper) fail(ctx context.Context, name string, err error) {
	s.cfg.Logger.WarnContext(ctx, "audit sink delivery failed",
		slog.String("sink", name), slog.String("error", err.Error()))

	if s.cfg.OnFailure != nil {
		s.cfg.OnFailure(name)
	}
}

// skip logs the records that sink skipped and calls the skip hook. The log
// line holds the sink name, the count and the reason, never an entry.
func (s *Shipper) skip(ctx context.Context, name string, skip *logsink.SkipError) {
	s.cfg.Logger.ErrorContext(ctx, "audit sink skipped entries that it cannot hold",
		slog.String("sink", name), slog.Int("entries", skip.Skipped), slog.String("reason", skip.Reason))

	if s.cfg.OnSkipped != nil {
		s.cfg.OnSkipped(name, skip.Skipped)
	}
}

// round copies the entries above the cursors of sink in every namespace
// and in ServerScope. Each scope ships on its own: a failure in one scope
// stops that scope, which keeps the order of its entries, and the round
// goes on with the next scope. The answer joins the errors of every scope
// that failed.
func (s *Shipper) round(ctx context.Context, sink ShipSink) error {
	namespaces, err := s.cfg.Namespaces.ListNamespaces(ctx)
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	namespaces = append(namespaces, serverScope())

	var errs []error

	for _, ns := range namespaces {
		if ctx.Err() != nil {
			return errors.Join(append(errs, ctx.Err())...)
		}

		if err := s.shipNamespace(ctx, sink, ns); err != nil {
			errs = append(errs, fmt.Errorf("namespace %s: %w", ns.Name, err))
		}
	}

	return errors.Join(errs...)
}

// shipNamespace copies the entries of ns above the cursor of sink, one
// batch of at most ShipBatchSize entries at a time.
func (s *Shipper) shipNamespace(ctx context.Context, sink ShipSink, ns namespace.Namespace) error {
	cursor, err := s.cfg.Feed.SinkCursor(ctx, sink.Name, ns.Name)
	if err != nil {
		return fmt.Errorf("read cursor: %w", err)
	}

	for {
		entries, err := s.cfg.Feed.After(ctx, ns.Name, cursor, ShipBatchSize)
		if err != nil {
			return fmt.Errorf("read entries: %w", err)
		}

		if len(entries) == 0 {
			return nil
		}

		if err := s.shipBatch(ctx, sink, ns, entries); err != nil {
			return err
		}

		cursor = entries[len(entries)-1].Sequence

		if len(entries) < ShipBatchSize {
			return nil
		}
	}
}

// shipBatch writes entries to sink, then moves the cursor of sink in ns
// past the last of them.
func (s *Shipper) shipBatch(ctx context.Context, sink ShipSink, ns namespace.Namespace, entries []Entry) error {
	records := make([]logsink.Record, 0, len(entries))
	for i := range entries {
		records = append(records, ToRecord(entries[i]))
	}

	delivered := len(entries)

	if err := sink.Sink.Write(ctx, records); err != nil {
		skip, ok := errors.AsType[*logsink.SkipError](err)
		if !ok {
			return fmt.Errorf("write: %w", err)
		}

		s.skip(ctx, sink.Name, skip)

		delivered -= skip.Skipped
	}

	last := entries[len(entries)-1].Sequence

	if err := s.cfg.Feed.AdvanceSinkCursor(ctx, ns.Fence(), sink.Name, last); err != nil {
		return fmt.Errorf("advance cursor: %w", err)
	}

	if s.cfg.OnShipped != nil && delivered > 0 {
		s.cfg.OnShipped(sink.Name, delivered)
	}

	return nil
}

// ToRecord maps an entry to a log record. The record carries the scope (the
// namespace) and the sequence of the entry, so a receiver can remove
// duplicates, and every field of the entry under its audit API name. It
// carries prevHash and hash when the entry has them.
func ToRecord(e Entry) logsink.Record {
	attrs := []logsink.Attr{
		logsink.String(RecordScope, e.Namespace.String()),
		logsink.Int64("sequence", e.Sequence),
		logsink.Int64("atMs", e.AtMs()),
		logsink.String("subject", e.Subject),
		logsink.String("idpSubject", e.IDPSubject),
		logsink.String("tokenID", e.TokenID),
		logsink.String("action", e.Action),
		logsink.String("object", e.Object),
		logsink.String("version", e.Version),
		logsink.String("valuePath", e.ValuePath),
	}

	if e.ValueCount != nil {
		attrs = append(attrs, logsink.Int64("valueCount", *e.ValueCount))
	}

	attrs = append(attrs,
		logsink.String("sourceIP", e.SourceIP),
		logsink.String("userAgent", e.UserAgent),
		logsink.String("channel", string(e.Channel)),
		logsink.String("kind", string(e.Kind)),
		logsink.String("outcome", string(e.Outcome)),
		logsink.String("requestID", e.RequestID),
	)

	if e.Hash != "" {
		attrs = append(attrs,
			logsink.String("prevHash", e.PrevHash),
			logsink.String("hash", e.Hash),
		)
	}

	return logsink.Record{
		Time:    e.At,
		Level:   logsink.LevelInfo,
		Message: RecordMessage,
		Attrs:   attrs,
	}
}
