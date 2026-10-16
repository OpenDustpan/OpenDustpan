package audit

import (
	"context"
	"encoding/json"
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
// write leaves the cursor, so the next round sends the batch again. While
// its lease holds, one instance ships a sink; after a lease is lost or a
// round fails, a sink can receive an entry twice, and the receiver removes
// duplicates by scope and sequence. The holder renews the lease well
// before it ends, and the cursor stays the source of truth, so a new holder
// goes on from the cursor. The shipper never drops an entry, except one
// that a sink cannot hold (logsink.ErrSkipped): it counts and logs that
// skip, and goes on.

// Shipper defaults and limits.
const (
	// DefaultShipInterval is the time between two rounds of one sink when
	// its ShipSink names none.
	DefaultShipInterval = 5 * time.Second
	// MaxShipBackoff caps the wait after failed rounds.
	MaxShipBackoff = 5 * time.Minute
	// ShipBatchSize caps the entries of one namespace in one sink write.
	ShipBatchSize = 500
	// ShipReleaseTimeout caps the release of a sink lease at shutdown.
	ShipReleaseTimeout = 5 * time.Second
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

// ErrInvalidLeaseTiming reports a shipper lease timing where the renew
// interval is not shorter than the lease TTL. A holder must renew well
// before its lease ends, so NewShipper rejects such a timing.
var ErrInvalidLeaseTiming = errors.New("invalid audit shipper lease timing")

// errRoundEnded reports that a round stopped at its round deadline or lost
// its sink lease. The round ends cleanly: the shipper logs the handover,
// keeps its wait, and starts the next round with no failure.
var errRoundEnded = errors.New("audit sink round ended")

// errLeaseLost reports that another instance holds the sink lease.
var errLeaseLost = errors.New("sink lease lost")

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
// count of entries that the sink skipped (logsink.ErrSkipped). InstanceID
// names the holder of the sink leases of this shipper: one instance ID per
// server process. Leases keeps those leases; when it is nil and Feed serves
// SinkLeaser, the shipper uses Feed. LeaseTTL is how long a taken lease
// lasts and defaults to SinkLeaseDuration. RenewInterval is how often the
// holder renews and defaults to SinkLeaseRenewInterval. RenewInterval must
// stay shorter than LeaseTTL, so a holder renews well before its lease ends.
type ShipperConfig struct {
	Feed          Feed
	Namespaces    NamespaceLister
	Logger        *slog.Logger
	OnShipped     func(sink string, entries int)
	OnFailure     func(sink string)
	OnSkipped     func(sink string, entries int)
	Sinks         []ShipSink
	InstanceID    string
	Leases        SinkLeaser
	LeaseTTL      time.Duration
	RenewInterval time.Duration
}

// Shipper copies committed entries to sinks.
type Shipper struct {
	cfg ShipperConfig
}

// NewShipper checks cfg and returns a shipper. Every sink needs a name of
// at most 64 bytes of [a-z0-9._-], and no two sinks share a name. When the
// shipper keeps leases, InstanceID is required and names their holder.
func NewShipper(cfg ShipperConfig) (*Shipper, error) {
	if cfg.Feed == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: missing feed or namespace lister", ErrInvalidShipper)
	}

	if err := checkShipSinks(cfg.Sinks); err != nil {
		return nil, err
	}

	cfg.setSinkLeaser()

	if err := applyLeaseDefaults(&cfg); err != nil {
		return nil, err
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	return &Shipper{cfg: cfg}, nil
}

// checkShipSinks checks that every sink has a usable name and no name
// repeats.
func checkShipSinks(sinks []ShipSink) error {
	seen := make(map[string]bool, len(sinks))

	for _, s := range sinks {
		if err := checkShipSink(s, seen); err != nil {
			return err
		}

		seen[s.Name] = true
	}

	return nil
}

// checkShipSink checks one sink against the names seen so far.
func checkShipSink(s ShipSink, seen map[string]bool) error {
	if s.Sink == nil {
		return fmt.Errorf("%w: sink %q is nil", ErrInvalidShipper, s.Name)
	}

	if !validSinkName(s.Name) {
		return fmt.Errorf("%w: sink name %q: want 1 to %d bytes of a-z, 0-9, '.', '_' or '-'",
			ErrInvalidShipper, s.Name, maxSinkNameLen)
	}

	if seen[s.Name] {
		return fmt.Errorf("%w: two sinks are named %q", ErrInvalidShipper, s.Name)
	}

	return nil
}

// setSinkLeaser sets the lease store of a shipper config: cfg when set, or
// the feed when it keeps leases.
func (cfg *ShipperConfig) setSinkLeaser() {
	if cfg.Leases == nil {
		if leaser, ok := cfg.Feed.(SinkLeaser); ok {
			cfg.Leases = leaser
		}
	}
}

// applyLeaseDefaults checks the lease holder and timing and sets the timing
// defaults.
func applyLeaseDefaults(cfg *ShipperConfig) error {
	if cfg.Leases != nil && cfg.InstanceID == "" {
		return fmt.Errorf("%w: missing instance ID for sink leases", ErrInvalidShipper)
	}

	if cfg.LeaseTTL < 0 || cfg.RenewInterval < 0 {
		return fmt.Errorf("%w: negative lease timing", ErrInvalidShipper)
	}

	if cfg.LeaseTTL == 0 {
		cfg.LeaseTTL = SinkLeaseDuration
	}

	if cfg.RenewInterval == 0 {
		cfg.RenewInterval = SinkLeaseRenewInterval
	}

	if cfg.RenewInterval >= cfg.LeaseTTL {
		return fmt.Errorf("%w: renew interval %s must be shorter than lease TTL %s",
			ErrInvalidLeaseTiming, cfg.RenewInterval, cfg.LeaseTTL)
	}

	return nil
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

// ShipOnce runs one round for every sink: it takes or renews the lease of
// the sink and copies the entries above the cursors while it holds it.
// Delivery is at least once: after a lease is lost or a round fails, a sink
// can receive an entry twice. A sink held by another instance is skipped
// without an error. It answers the joined errors of the failed sinks.
func (s *Shipper) ShipOnce(ctx context.Context) error {
	var errs []error

	for _, sink := range s.cfg.Sinks {
		held, err := s.acquire(ctx, sink.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("sink %s: %w", sink.Name, err))

			continue
		}

		if !held {
			continue
		}

		if err := s.round(ctx, sink); err != nil {
			errs = append(errs, fmt.Errorf("sink %s: %w", sink.Name, err))
		}
	}

	return errors.Join(errs...)
}

// loop runs rounds for sink until ctx is done. After a failed round it
// waits twice as long as before, up to MaxShipBackoff. A round that ends at
// its round deadline or loses its lease ends cleanly with no failure. It
// takes or renews
// the lease of the sink before every round and ships only while it holds
// it. It wakes at least every renew interval to renew, so a ship interval
// longer than the lease still holds it, and it releases the lease when the
// context ends.
func (s *Shipper) loop(ctx context.Context, sink ShipSink) {
	interval := shipInterval(sink)

	if s.cfg.Leases == nil {
		s.loopWithoutLease(ctx, sink, interval)

		return
	}

	renew := s.cfg.RenewInterval
	state := &leasedState{wait: interval, lastShip: time.Now()}

	defer s.release(ctx, sink.Name)

	for {
		if !waitNextRound(ctx, leaseSleep(state.wait, renew)) {
			return
		}

		if !s.stepLeased(ctx, sink, interval, state) {
			return
		}
	}
}

// leasedState holds the backoff and the last ship time of one leased loop.
type leasedState struct {
	wait     time.Duration
	lastShip time.Time
}

// shipInterval answers the time between two rounds of sink.
func shipInterval(sink ShipSink) time.Duration {
	if sink.Interval <= 0 {
		return DefaultShipInterval
	}

	return sink.Interval
}

// leaseSleep answers how long a leased loop waits before its next lease
// take: the backoff or the renew interval, whichever is shorter.
func leaseSleep(wait, renew time.Duration) time.Duration {
	sleep := min(wait, renew)
	if sleep <= 0 {
		return renew
	}

	return sleep
}

// waitNextRound waits sleep before the next lease take and answers whether
// to go on: false when ctx ends first.
func waitNextRound(ctx context.Context, sleep time.Duration) bool {
	timer := time.NewTimer(sleep)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// stepLeasedShip takes or renews the lease of sink and ships when due. It
// updates state and answers whether the loop goes on: false when ctx ends.
func (s *Shipper) stepLeased(ctx context.Context, sink ShipSink, interval time.Duration, state *leasedState) bool {
	held, err := s.acquire(ctx, sink.Name)
	if err != nil {
		return s.handleLeaseError(ctx, sink.Name, err, state)
	}

	if !held {
		state.wait = interval

		return true
	}

	if time.Since(state.lastShip) < interval {
		return true
	}

	return s.shipAndAccount(ctx, sink, interval, state)
}

// handleLeaseError records a failed lease take in state and answers whether
// the loop goes on: false when ctx ends.
func (s *Shipper) handleLeaseError(ctx context.Context, name string, err error, state *leasedState) bool {
	if ctx.Err() != nil {
		return false
	}

	s.fail(ctx, name, err)

	state.wait = min(state.wait*shipBackoffFactor, MaxShipBackoff)

	return true
}

// shipAndAccount ships one round and records its outcome in state. A round
// that ends at its round deadline or loses its lease ends cleanly: it keeps
// the wait of the loop and starts the next round with no failure. It
// answers whether the loop goes on: false when ctx ends.
func (s *Shipper) shipAndAccount(ctx context.Context, sink ShipSink, interval time.Duration, state *leasedState) bool {
	if err := s.round(ctx, sink); err != nil {
		if ctx.Err() != nil {
			return false
		}

		if errors.Is(err, errRoundEnded) {
			s.cfg.Logger.DebugContext(ctx, "audit sink round ended",
				slog.String("sink", sink.Name), slog.String("error", ErrorClass(err)))

			state.wait = interval
			state.lastShip = time.Now()

			return true
		}

		s.fail(ctx, sink.Name, err)

		state.wait = min(state.wait*shipBackoffFactor, MaxShipBackoff)
		state.lastShip = time.Now()

		return true
	}

	state.wait = interval
	state.lastShip = time.Now()

	return true
}

// loopWithoutLease runs rounds for sink until ctx is done without keeping a
// lease. After a failed round it waits twice as long as before, up to
// MaxShipBackoff.
func (s *Shipper) loopWithoutLease(ctx context.Context, sink ShipSink, interval time.Duration) {
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

// acquire takes or renews the lease of sink for this shipper. It answers
// false without an error while another instance holds a live lease. Without
// a lease store it always answers true.
func (s *Shipper) acquire(ctx context.Context, sink string) (bool, error) {
	if s.cfg.Leases == nil {
		return true, nil
	}

	held, err := s.cfg.Leases.AcquireSinkLease(ctx, sink, s.cfg.InstanceID, s.cfg.LeaseTTL)
	if err != nil {
		return false, &shipError{step: stepLease, err: err}
	}

	return held, nil
}

// release releases the lease of sink held by this shipper. Without a lease
// store it does nothing. It runs at loop shutdown with a detached context,
// so a canceled loop still releases.
func (s *Shipper) release(ctx context.Context, sink string) {
	if s.cfg.Leases == nil {
		return
	}

	ctx = context.WithoutCancel(ctx)

	ctx, cancel := context.WithTimeout(ctx, ShipReleaseTimeout)
	defer cancel()

	if err := s.cfg.Leases.ReleaseSinkLease(ctx, sink, s.cfg.InstanceID); err != nil {
		s.cfg.Logger.DebugContext(ctx, "audit sink lease release failed",
			slog.String("sink", sink), slog.String("error", ErrorClass(err)))
	}
}

// fail logs a failed round and calls the failure hook. The log line holds
// the sink name, the count of scopes that failed and a fixed error class,
// never an entry and never the text of a store, driver or transport error:
// the line can go to a remote sink (ADR 32 decision 7).
func (s *Shipper) fail(ctx context.Context, name string, err error) {
	s.cfg.Logger.WarnContext(ctx, "audit sink delivery failed",
		slog.String("sink", name), slog.Int("scopes", failedScopes(err)), slog.String("error", ErrorClass(err)))

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

// roundTimeout answers how long a leased round may run: the lease TTL
// less the renew interval, so the round stops before the lease ends. It
// answers the lease TTL when the difference is not positive.
func (s *Shipper) roundTimeout() time.Duration {
	if s.cfg.LeaseTTL > s.cfg.RenewInterval {
		return s.cfg.LeaseTTL - s.cfg.RenewInterval
	}

	return s.cfg.LeaseTTL
}

// withRoundTimeout bounds a leased round by a deadline shorter than the
// lease, so the round stops before the lease ends. Without a lease store it
// returns ctx with a no-op cancel.
func (s *Shipper) withRoundTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.cfg.Leases == nil {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, s.roundTimeout())
}

// round copies the entries above the cursors of sink in every namespace
// and in ServerScope. Each scope ships on its own: a failure in one scope
// stops that scope, which keeps the order of its entries, and the round
// goes on with the next scope. The answer joins the errors of every scope
// that failed. A leased round runs under a deadline shorter than the lease
// and renews the lease between scopes and between batches: when the
// deadline passes or another instance holds the lease, the round stops at
// once without moving a cursor past what the sink acknowledged.
func (s *Shipper) round(ctx context.Context, sink ShipSink) error {
	outer := ctx

	ctx, cancel := s.withRoundTimeout(ctx)
	defer cancel()

	namespaces, err := s.cfg.Namespaces.ListNamespaces(ctx)
	if err != nil {
		return markRoundEnded(outer, &shipError{step: stepList, err: err})
	}

	namespaces = append(namespaces, serverScope())

	return markRoundEnded(outer, s.shipScopes(ctx, sink, namespaces))
}

// markRoundEnded reports a round that stopped at its round deadline or lost
// its sink lease as a clean round end. When the outer context is done the
// round stops for shutdown instead, and the error stays as it is. A round
// with any other failure stays a failure, so the loop still counts it.
func markRoundEnded(outer context.Context, err error) error {
	if err == nil {
		return nil
	}

	if outer.Err() != nil {
		return err
	}

	if errors.Is(err, errRoundEnded) {
		return err
	}

	if errors.Is(err, errLeaseLost) {
		return fmt.Errorf("%w: %w", errRoundEnded, err)
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", errRoundEnded, err)
	}

	return err
}

// shipScopes ships every namespace of one round in order. Before every
// scope but the first it renews the lease: when another instance holds it,
// the round stops at once.
func (s *Shipper) shipScopes(ctx context.Context, sink ShipSink, namespaces []namespace.Namespace) error {
	var errs []error

	for i, ns := range namespaces {
		if ctx.Err() != nil {
			return errors.Join(append(errs, ctx.Err())...)
		}

		if i > 0 {
			if err := s.renewLease(ctx, sink.Name); err != nil {
				return errors.Join(append(errs, err)...)
			}
		}

		if err := s.shipScope(ctx, sink, ns); err != nil {
			if errors.Is(err, errLeaseLost) {
				return errors.Join(append(errs, err)...)
			}

			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// shipScope ships one scope. It names the scope in a failure.
func (s *Shipper) shipScope(ctx context.Context, sink ShipSink, ns namespace.Namespace) error {
	if err := s.shipNamespace(ctx, sink, ns); err != nil {
		return fmt.Errorf("namespace %s: %w", ns.Name, err)
	}

	return nil
}

// renewLease renews the lease of sink. Without a lease store it does
// nothing. It answers errLeaseLost at once when another instance holds the
// lease.
func (s *Shipper) renewLease(ctx context.Context, sink string) error {
	if s.cfg.Leases == nil {
		return nil
	}

	held, err := s.acquire(ctx, sink)
	if err != nil {
		return err
	}

	if !held {
		return &shipError{step: stepLease, err: fmt.Errorf("%w: %w", errRoundEnded, errLeaseLost)}
	}

	return nil
}

// shipNamespace copies the entries of ns above the cursor of sink, one
// batch of at most ShipBatchSize entries at a time. Between batches it
// renews the sink lease when the shipper keeps leases: when another
// instance holds the lease, it stops the round at once.
func (s *Shipper) shipNamespace(ctx context.Context, sink ShipSink, ns namespace.Namespace) error {
	cursor, err := s.cfg.Feed.SinkCursor(ctx, sink.Name, ns.Name)
	if err != nil {
		return &shipError{step: stepCursor, err: err}
	}

	first := true

	for {
		if !first {
			if err := s.renewLease(ctx, sink.Name); err != nil {
				return err
			}
		}

		first = false

		entries, err := s.nextBatch(ctx, ns, cursor)
		if err != nil {
			return err
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

// nextBatch answers the entries above cursor, oldest first. It answers no
// entries when the scope holds no more, and a clean round end when the
// round deadline passed, so the cursor stays where the sink acknowledged.
func (s *Shipper) nextBatch(ctx context.Context, ns namespace.Namespace, cursor int64) ([]Entry, error) {
	if ctx.Err() != nil {
		return nil, &shipError{step: stepRead, err: fmt.Errorf("%w: round deadline: %w", errRoundEnded, ctx.Err())}
	}

	entries, err := s.cfg.Feed.After(ctx, ns.Name, cursor, ShipBatchSize)
	if err != nil {
		return nil, &shipError{step: stepRead, err: err}
	}

	return entries, nil
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
			return &shipError{step: stepWrite, err: err}
		}

		s.skip(ctx, sink.Name, skip)

		delivered -= skip.Skipped
	}

	last := entries[len(entries)-1].Sequence

	if err := s.cfg.Feed.AdvanceSinkCursor(ctx, ns.Fence(), sink.Name, last); err != nil {
		return &shipError{step: stepAdvance, err: err}
	}

	if s.cfg.OnShipped != nil && delivered > 0 {
		s.cfg.OnShipped(sink.Name, delivered)
	}

	return nil
}

// ToRecord maps an entry to a log record. The record carries the scope (the
// namespace) and the sequence of the entry, so a receiver can remove
// duplicates, and every field of the entry under its audit API name. It
// carries valueCount, objectID and changes only when the entry has them,
// and prevHash and hash when the entry has them.
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

	if e.ObjectID != "" {
		attrs = append(attrs, logsink.String("objectID", e.ObjectID))
	}

	// The changes go as their canonical JSON, the text that the hash covers.
	if text := EncodeChanges(e.Changes); text != "" {
		attrs = append(attrs, logsink.RawJSON("changes", json.RawMessage(text)))
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

// Steps of a round, for the error class of a failure.
const (
	stepList    = "list namespaces"
	stepCursor  = "read cursor"
	stepRead    = "read entries"
	stepWrite   = "write"
	stepAdvance = "advance cursor"
	stepLease   = "take lease"
)

// shipError is a failure of one step of a round. Its text holds the text of
// the cause, so the log never prints it: it prints ErrorClass instead.
type shipError struct {
	err  error
	step string
}

// Error implements error.
func (e *shipError) Error() string {
	return e.step + ": " + e.err.Error()
}

// Unwrap returns the cause.
func (e *shipError) Unwrap() error {
	return e.err
}

// ErrorClass reduces a failure of a round to a fixed text: the step that
// failed and a class of the cause, for example "write: refused by the
// target". It never holds the text of the cause, which can carry driver
// detail, a host name or a token (ADR 32 decision 7). When several scopes
// fail, it names the first.
func ErrorClass(err error) string {
	step := "round"
	if se, ok := errors.AsType[*shipError](err); ok {
		step = se.step
	}

	return step + ": " + causeClass(err)
}

// causeClass names the class of the cause of err.
func causeClass(err error) string {
	var timeout interface{ Timeout() bool }

	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, logsink.ErrClosed):
		return "sink closed"
	case errors.Is(err, logsink.ErrRefused):
		return "refused by the target"
	case errors.Is(err, namespace.ErrFenced):
		return "fenced"
	case errors.Is(err, namespace.ErrNotFound):
		return "namespace not found"
	case errors.As(err, &timeout) && timeout.Timeout():
		return "timeout"
	default:
		return "failed"
	}
}

// failedScopes counts the scopes whose shipping failed in the round that
// answered err.
func failedScopes(err error) int {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		return len(joined.Unwrap())
	}

	return 1
}
