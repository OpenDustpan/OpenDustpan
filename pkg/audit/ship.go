package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
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
	// instances that ship the same batch never move a cursor back. The
	// write checks claim in the transaction of the write, with the claim
	// rule of the sink lease: it answers durable.ErrClaimLost, and changes
	// nothing, when claim is not the live claim of the sink. It returns
	// namespace.ErrNotFound if the fence names a missing namespace and
	// namespace.ErrFenced if the fence carries a stale failover version:
	// the fence check runs before the claim check. A fence that names
	// ServerScope has no failover version: the store moves that cursor
	// with no fence check, but still checks the claim.
	AdvanceSinkCursor(ctx context.Context, fence namespace.Fence, claim durable.Claim, sink string, seq int64) error
}

// NamespaceLister lists the namespaces whose entries the shipper copies.
type NamespaceLister interface {
	// ListNamespaces returns every namespace, ordered by name.
	ListNamespaces(ctx context.Context) ([]namespace.Namespace, error)
}

// ShipSink is one sink of the shipper. Name keys the cursors of the sink
// in the database and labels its metrics: keep it when the sink setting
// changes, or the sink receives every entry again. Kind names the adapter
// of the sink, for example file or webhook. Interval is the time
// between two rounds. Zero means DefaultShipInterval.
type ShipSink struct {
	Sink     logsink.Sink
	Name     string
	Kind     string
	Interval time.Duration
}

// ShipperConfig configures a Shipper. OnShipped, OnFailure, OnSkipped,
// OnLag and OnLagEntries are hooks for metrics and may be nil. OnShipped
// receives the sink name and the count of entries of one accepted batch.
// OnFailure receives the sink name and the reason after a failed round, and
// once for each entry that the sink refused as too large
// (logsink.ErrSkipped). OnSkipped receives the sink name and the count of
// entries that the sink skipped (logsink.ErrSkipped). OnLag receives the
// sink name and its delivery lag in seconds after each round. OnLagEntries
// receives the sink name and its delivery lag in entries after each round.
// Now answers the current time for the lag; nil means time.Now. InstanceID
// names the holder of the sink leases of this shipper: one instance ID per
// server process. Leases keeps those leases, and is required when Feed keeps
// sink leases, as both SQL stores do: without it the shipper ships with a
// zero claim that no store accepts. A feed without leases ships with a zero
// claim and moves its cursors with no claim check. LeaseTTL is how long a
// lasts and defaults to SinkLeaseDuration. RenewInterval is how often the
// holder renews and defaults to SinkLeaseRenewInterval. RenewInterval must
// stay shorter than LeaseTTL, so a holder renews well before its lease ends.
type ShipperConfig struct {
	Feed          Feed
	Namespaces    NamespaceLister
	Logger        *slog.Logger
	OnShipped     func(sink string, entries int)
	OnFailure     func(sink, reason string)
	OnSkipped     func(sink string, entries int)
	OnLag         func(sink string, seconds float64)
	OnLagEntries  func(sink string, entries int64)
	Now           func() time.Time
	Sinks         []ShipSink
	InstanceID    string
	Leases        SinkLeaser
	LeaseTTL      time.Duration
	RenewInterval time.Duration
}

// Shipper copies committed entries to sinks.
type Shipper struct {
	cfg ShipperConfig

	mu      sync.Mutex
	lastErr map[string]string
}

// NewShipper checks cfg and returns a shipper. Every sink needs a name of
// at most 64 bytes of [a-z0-9._-], and no two sinks share a name. When the
// shipper keeps leases, InstanceID is required and names their holder. A
// feed that keeps sink leases needs Leases: NewShipper refuses it without
// one.
func NewShipper(cfg ShipperConfig) (*Shipper, error) {
	if cfg.Feed == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: missing feed or namespace lister", ErrInvalidShipper)
	}

	if err := checkShipSinks(cfg.Sinks); err != nil {
		return nil, err
	}

	if err := checkSinkLeaser(cfg); err != nil {
		return nil, err
	}

	if err := applyLeaseDefaults(&cfg); err != nil {
		return nil, err
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &Shipper{cfg: cfg, mu: sync.Mutex{}, lastErr: map[string]string{}}, nil
}

// Sinks answers the configured sinks in order.
func (s *Shipper) Sinks() []ShipSink {
	out := make([]ShipSink, len(s.cfg.Sinks))
	copy(out, s.cfg.Sinks)

	return out
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

// checkSinkLeaser refuses a config with no lease store whose feed keeps
// sink leases, as both SQL stores do: their cursor writes check the claim,
// so a shipper with a zero claim would end every round cleanly with nothing
// shipped. A feed without leases ships with a zero claim and moves its
// cursors with no claim check.
func checkSinkLeaser(cfg ShipperConfig) error {
	if cfg.Leases != nil {
		return nil
	}

	if _, ok := cfg.Feed.(SinkLeaser); ok {
		return fmt.Errorf("%w: feed keeps sink leases but no lease store was passed", ErrInvalidShipper)
	}

	return nil
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
		ID:              "",
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
		claim, held, err := s.acquire(ctx, sink.Name)
		if err != nil {
			s.fail(ctx, sink.Name, err)
			s.setLastError(sink.Name, ErrorClass(err))
			s.observeLag(ctx, sink.Name)
			errs = append(errs, fmt.Errorf("sink %s: %w", sink.Name, err))

			continue
		}

		if !held {
			continue
		}

		if err := s.round(ctx, sink, &claim); err != nil {
			if errors.Is(err, errRoundEnded) {
				s.setLastError(sink.Name, "")
			} else {
				s.fail(ctx, sink.Name, err)
				s.setLastError(sink.Name, ErrorClass(err))
			}

			s.observeLag(ctx, sink.Name)
			errs = append(errs, fmt.Errorf("sink %s: %w", sink.Name, err))

			continue
		}

		s.setLastError(sink.Name, "")
		s.observeLag(ctx, sink.Name)
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
	state := &leasedState{wait: interval, lastShip: time.Now(), claim: zeroClaim()}

	defer s.release(ctx, sink.Name, &state.claim)

	for {
		if !waitNextRound(ctx, leaseSleep(state.wait, renew)) {
			return
		}

		if !s.stepLeased(ctx, sink, interval, state) {
			return
		}
	}
}

// leasedState holds the backoff, the last ship time and the live claim of
// one leased loop. The loop releases the claim when it stops.
type leasedState struct {
	wait     time.Duration
	lastShip time.Time
	claim    durable.Claim
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
	claim, held, err := s.acquire(ctx, sink.Name)
	if err != nil {
		return s.handleLeaseError(ctx, sink.Name, err, state)
	}

	if !held {
		state.wait = interval

		return true
	}

	state.claim = claim

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
	if err := s.round(ctx, sink, &state.claim); err != nil {
		if ctx.Err() != nil {
			return false
		}

		if errors.Is(err, errRoundEnded) {
			s.cfg.Logger.DebugContext(ctx, "audit sink round ended",
				slog.String("sink", sink.Name), slog.String("error", ErrorClass(err)))

			s.setLastError(sink.Name, "")
			s.observeLag(ctx, sink.Name)

			state.wait = interval
			state.lastShip = time.Now()

			return true
		}

		s.fail(ctx, sink.Name, err)
		s.setLastError(sink.Name, ErrorClass(err))
		s.observeLag(ctx, sink.Name)

		state.wait = min(state.wait*shipBackoffFactor, MaxShipBackoff)
		state.lastShip = time.Now()

		return true
	}

	s.setLastError(sink.Name, "")
	s.observeLag(ctx, sink.Name)

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

		claim := zeroClaim()

		if err := s.round(ctx, sink, &claim); err != nil {
			if ctx.Err() != nil {
				return
			}

			s.fail(ctx, sink.Name, err)
			s.setLastError(sink.Name, ErrorClass(err))
			s.observeLag(ctx, sink.Name)

			wait = min(wait*shipBackoffFactor, MaxShipBackoff)

			continue
		}

		s.setLastError(sink.Name, "")
		s.observeLag(ctx, sink.Name)

		wait = interval
	}
}

// acquire takes or renews the lease of sink for this shipper. It answers
// the live claim and true while this shipper holds the lease, and a zero
// claim and false without an error while another instance holds a live
// lease. Without a lease store it always answers held with a zero claim,
// which only a feed that moves its cursors with no claim check accepts:
// NewShipper refuses a feed that keeps sink leases without one.
func (s *Shipper) acquire(ctx context.Context, sink string) (durable.Claim, bool, error) {
	if s.cfg.Leases == nil {
		return zeroClaim(), true, nil
	}

	claim, err := s.cfg.Leases.AcquireSinkLease(ctx, sink, s.cfg.InstanceID, s.cfg.LeaseTTL)
	if errors.Is(err, durable.ErrNoWork) {
		return zeroClaim(), false, nil
	}

	if err != nil {
		return zeroClaim(), false, &shipError{step: stepLease, err: err, seq: 0, hasSeq: false}
	}

	return claim, true, nil
}

// release releases the claim on the sink operation. Without a lease store
// it does nothing. It runs at loop shutdown with a detached context, so a
// canceled loop still releases. A claim that is no longer live releases
// nothing: the store answers durable.ErrClaimLost.
func (s *Shipper) release(ctx context.Context, sink string, claim *durable.Claim) {
	if s.cfg.Leases == nil || claim.Token == 0 {
		return
	}

	ctx = context.WithoutCancel(ctx)

	ctx, cancel := context.WithTimeout(ctx, ShipReleaseTimeout)
	defer cancel()

	if err := s.cfg.Leases.ReleaseSinkLease(ctx, *claim); err != nil {
		s.cfg.Logger.DebugContext(ctx, "audit sink lease release failed",
			slog.String("sink", sink), slog.String("error", ErrorClass(err)))
	}
}

// setLastError records the last error class of sink: empty when the last
// round succeeded.
func (s *Shipper) setLastError(sink, class string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if class == "" {
		delete(s.lastErr, sink)

		return
	}

	s.lastErr[sink] = class
}

// lastErrorOf answers the last error class of sink, or empty when the last
// round succeeded.
func (s *Shipper) lastErrorOf(sink string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.lastErr[sink]
}

// observeLag reads the delivery lags of sink and calls the lag hooks. A lag
// read that fails keeps the last gauge value: the next round tries again.
func (s *Shipper) observeLag(ctx context.Context, sink string) {
	if s.cfg.OnLag != nil {
		lag, err := s.Lag(ctx, sink, s.cfg.Now())
		if err == nil {
			s.cfg.OnLag(sink, lag)
		}
	}

	if s.cfg.OnLagEntries != nil {
		entries, err := s.LagEntries(ctx, sink)
		if err == nil {
			s.cfg.OnLagEntries(sink, entries)
		}
	}
}

// Failure reasons of an audit sink delivery, for the reason label of the
// failure metric. The set is fixed, so the label values never grow with the
// entries.
const (
	// FailureReasonSize reports an entry that the sink refused because it
	// cannot hold it, for example a syslog record above the message cap.
	FailureReasonSize = "size"
	// FailureReasonUnreachable reports a batch that never reached the sink
	// because the dial failed.
	FailureReasonUnreachable = "unreachable"
	// FailureReasonWrite reports a batch whose write or cursor advance
	// failed.
	FailureReasonWrite = "write"
	// FailureReasonRead reports a round that could not read its entries, its
	// cursors or its namespaces.
	FailureReasonRead = "read"
	// FailureReasonLease reports a round that could not take its sink lease.
	FailureReasonLease = "lease"
)

// failureReason reduces a failure of a round to a reason for the failure
// metric: size for a refused entry, unreachable for a failed dial, lease for
// a failed lease take, read for a failed entry, cursor or namespace read,
// and write for every other failure.
func failureReason(err error) string {
	switch {
	case errors.Is(err, logsink.ErrSkipped):
		return FailureReasonSize
	case isDialError(err):
		return FailureReasonUnreachable
	}

	if shipErr, ok := errors.AsType[*shipError](err); ok {
		switch shipErr.step {
		case stepLease:
			return FailureReasonLease
		case stepRead, stepList, stepCursor:
			return FailureReasonRead
		default:
			return FailureReasonWrite
		}
	}

	return FailureReasonWrite
}

// isDialError reports whether err wraps a failed network dial, as a sink
// whose receiver is down answers on its first write.
func isDialError(err error) bool {
	var opErr *net.OpError

	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// fail logs a failed round and calls the failure hook. The log line holds
// the sink name, the count of scopes that failed, the reason, the sequence
// of the first entry of the failed batch when the round knows it, and a
// fixed error class, never an entry and never the text of a store, driver
// or transport error: the line can go to a remote sink. A round that never
// read a batch, for example a failed lease take or a failed entry, cursor
// or namespace read, carries no sequence. A refused entry is a delivery
// failure too: skipSize logs it separately with its sequence.
func (s *Shipper) fail(ctx context.Context, name string, err error) {
	reason := failureReason(err)

	attrs := []any{
		slog.String("sink", name),
		slog.Int("scopes", failedScopes(err)),
		slog.String("reason", reason),
	}

	if seq, ok := failureSequence(err); ok {
		attrs = append(attrs, slog.Int64("sequence", seq))
	}

	attrs = append(attrs, slog.String("error", ErrorClass(err)))

	s.cfg.Logger.ErrorContext(ctx, "audit sink delivery failed", attrs...)

	if s.cfg.OnFailure != nil {
		s.cfg.OnFailure(name, reason)
	}
}

// failureSequence answers the sequence of the first entry of the failed
// batch when the round knows it: the sequence that the failed write or
// cursor advance carried. It answers false for a round that never read a
// batch, for example a failed lease take or a failed entry, cursor or
// namespace read.
func failureSequence(err error) (int64, bool) {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, part := range joined.Unwrap() {
			if seq, ok := failureSequence(part); ok {
				return seq, true
			}
		}
	}

	var shipErr *shipError
	if errors.As(err, &shipErr) && shipErr.hasSeq {
		return shipErr.seq, true
	}

	return 0, false
}

// skipSize logs the records that sink refused because it cannot hold them
// and calls the skip and failure hooks. It logs one line at error level for
// each refused entry with its scope, its sequence and the reason, never the
// entry itself. Each refused entry is a delivery failure, so it also calls
// the failure hook with the size reason. The shipper still moves the cursor
// past the batch: a retry cannot deliver a refused entry.
func (s *Shipper) skipSize(ctx context.Context, name string, skip *logsink.SkipError, entries []Entry) {
	for _, index := range skip.Indexes {
		if index < 0 || index >= len(entries) {
			continue
		}

		entry := entries[index]

		s.cfg.Logger.ErrorContext(ctx, "audit sink refused entry that it cannot hold",
			slog.String("sink", name),
			slog.String("scope", entry.Namespace.String()),
			slog.Int64("sequence", entry.Sequence),
			slog.String("reason", skip.Reason))

		if s.cfg.OnFailure != nil {
			s.cfg.OnFailure(name, FailureReasonSize)
		}
	}

	if len(skip.Indexes) == 0 {
		s.cfg.Logger.ErrorContext(ctx, "audit sink skipped entries that it cannot hold",
			slog.String("sink", name), slog.Int("entries", skip.Skipped), slog.String("reason", skip.Reason))

		if s.cfg.OnFailure != nil {
			for range skip.Skipped {
				s.cfg.OnFailure(name, FailureReasonSize)
			}
		}
	}

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
// once without moving a cursor past what the sink acknowledged. Every
// cursor write carries the live claim, so a holder that lost its claim
// cannot move a cursor: the write fails with durable.ErrClaimLost and the
// round ends cleanly. Each renew writes its claim back through claim, so the
// caller always holds the live claim, for example to release it.
func (s *Shipper) round(ctx context.Context, sink ShipSink, claim *durable.Claim) error {
	outer := ctx

	ctx, cancel := s.withRoundTimeout(ctx)
	defer cancel()

	namespaces, err := s.cfg.Namespaces.ListNamespaces(ctx)
	if err != nil {
		return markRoundEnded(outer, ctx, &shipError{step: stepList, err: err, seq: 0, hasSeq: false})
	}

	namespaces = append(namespaces, serverScope())

	return markRoundEnded(outer, ctx, s.shipScopes(ctx, sink, claim, namespaces))
}

// markRoundEnded reports a round that stopped at its round deadline or lost
// its sink lease as a clean round end. A deadline ends a round cleanly only
// while the round context is done and the outer context is not: a timeout
// from a sink while the round context is still live stays a failure, so the
// loop still counts it. When the outer context is done the round stops for
// shutdown instead, and the error stays as it is. A joined error ends a
// round cleanly only when every part ends it cleanly: when any scope failed,
// the whole round stays a failure, so the loop still counts it. A round
// with any other failure stays a failure, so the loop still counts it.
func markRoundEnded(outer, roundCtx context.Context, err error) error {
	if err == nil {
		return nil
	}

	if outer.Err() != nil {
		return err
	}

	if !roundEndOnly(roundCtx, err) {
		return err
	}

	if errors.Is(err, errRoundEnded) {
		return err
	}

	return fmt.Errorf("%w: %w", errRoundEnded, err)
}

// roundEndOnly reports whether err ends a round cleanly: it matches
// errRoundEnded, errLeaseLost or durable.ErrClaimLost, or the round context
// is done and it matches context.DeadlineExceeded or context.Canceled. A
// cursor write of a holder that lost its claim fails with ErrClaimLost, and
// the round ends cleanly without moving the cursor. A joined error ends a
// round cleanly only when every part does.
func roundEndOnly(roundCtx context.Context, err error) bool {
	if err == nil {
		return true
	}

	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, part := range joined.Unwrap() {
			if !roundEndOnly(roundCtx, part) {
				return false
			}
		}

		return true
	}

	if errors.Is(err, errRoundEnded) {
		return true
	}

	if errors.Is(err, errLeaseLost) {
		return true
	}

	if errors.Is(err, durable.ErrClaimLost) {
		return true
	}

	return roundCtx.Err() != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
}

// shipScopes ships every namespace of one round in order. Before every
// scope but the first it renews the lease: when another instance holds it,
// the round stops at once. Every cursor write carries the live claim, and
// each renew writes its claim back through claim.
func (s *Shipper) shipScopes(
	ctx context.Context,
	sink ShipSink,
	claim *durable.Claim,
	namespaces []namespace.Namespace,
) error {
	var errs []error

	for i, ns := range namespaces {
		if ctx.Err() != nil {
			return errors.Join(append(errs, ctx.Err())...)
		}

		if i > 0 {
			renewed, err := s.renewLease(ctx, sink.Name)
			if err != nil {
				return errors.Join(append(errs, err)...)
			}

			*claim = renewed
		}

		if err := s.shipScope(ctx, sink, claim, ns); err != nil {
			if errors.Is(err, errLeaseLost) {
				return errors.Join(append(errs, err)...)
			}

			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// shipScope ships one scope. It names the scope in a failure.
func (s *Shipper) shipScope(ctx context.Context, sink ShipSink, claim *durable.Claim, ns namespace.Namespace) error {
	if err := s.shipNamespace(ctx, sink, claim, ns); err != nil {
		return fmt.Errorf("namespace %s: %w", ns.Name, err)
	}

	return nil
}

// renewLease renews the lease of sink and answers the live claim. Without
// a lease store it answers a zero claim and does nothing. It answers
// errLeaseLost at once when another instance holds the lease.
func (s *Shipper) renewLease(ctx context.Context, sink string) (durable.Claim, error) {
	if s.cfg.Leases == nil {
		return zeroClaim(), nil
	}

	claim, held, err := s.acquire(ctx, sink)
	if err != nil {
		return zeroClaim(), err
	}

	if !held {
		return durable.Claim{}, &shipError{
			step:   stepLease,
			err:    fmt.Errorf("%w: %w", errRoundEnded, errLeaseLost),
			seq:    0,
			hasSeq: false,
		}
	}

	return claim, nil
}

// shipNamespace copies the entries of ns above the cursor of sink, one
// batch of at most ShipBatchSize entries at a time. Between batches it
// renews the sink lease when the shipper keeps leases: when another
// instance holds the lease, it stops the round at once. Every cursor write
// carries the live claim, and each renew writes its claim back through
// claim.
func (s *Shipper) shipNamespace(
	ctx context.Context,
	sink ShipSink,
	claim *durable.Claim,
	ns namespace.Namespace,
) error {
	cursor, err := s.cfg.Feed.SinkCursor(ctx, sink.Name, ns.Name)
	if err != nil {
		return &shipError{step: stepCursor, err: err, seq: 0, hasSeq: false}
	}

	first := true

	for {
		if !first {
			renewed, err := s.renewLease(ctx, sink.Name)
			if err != nil {
				return err
			}

			*claim = renewed
		}

		first = false

		entries, err := s.nextBatch(ctx, ns, cursor)
		if err != nil {
			return err
		}

		if len(entries) == 0 {
			return nil
		}

		if err := s.shipBatch(ctx, sink, *claim, ns, entries); err != nil {
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
		return nil, &shipError{
			step:   stepRead,
			err:    fmt.Errorf("%w: round deadline: %w", errRoundEnded, ctx.Err()),
			seq:    0,
			hasSeq: false,
		}
	}

	entries, err := s.cfg.Feed.After(ctx, ns.Name, cursor, ShipBatchSize)
	if err != nil {
		return nil, &shipError{step: stepRead, err: err, seq: 0, hasSeq: false}
	}

	return entries, nil
}

// shipBatch writes entries to sink, then moves the cursor of sink in ns
// past the last of them. The cursor write carries claim: a holder that
// lost its claim cannot move the cursor.
func (s *Shipper) shipBatch(
	ctx context.Context,
	sink ShipSink,
	claim durable.Claim,
	ns namespace.Namespace,
	entries []Entry,
) error {
	records := make([]logsink.Record, 0, len(entries))
	for i := range entries {
		records = append(records, ToRecord(entries[i]))
	}

	delivered := len(entries)

	if err := sink.Sink.Write(ctx, records); err != nil {
		skip, ok := errors.AsType[*logsink.SkipError](err)
		if !ok {
			return &shipError{step: stepWrite, err: err, seq: entries[0].Sequence, hasSeq: true}
		}

		s.skipSize(ctx, sink.Name, skip, entries)

		delivered -= skip.Skipped
	}

	last := entries[len(entries)-1].Sequence

	if err := s.cfg.Feed.AdvanceSinkCursor(ctx, ns.Fence(), claim, sink.Name, last); err != nil {
		return &shipError{step: stepAdvance, err: err, seq: entries[0].Sequence, hasSeq: true}
	}

	if s.cfg.OnShipped != nil && delivered > 0 {
		s.cfg.OnShipped(sink.Name, delivered)
	}

	return nil
}

// ToRecord maps an entry to a log record. The record carries the scope (the
// namespace) and the sequence of the entry, so a receiver can remove
// duplicates, and every field of the entry under its audit API name. It
// carries valueCount, objectID, fenceVersion and changes only when the entry has them,
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

	if e.FenceVersion > 0 {
		attrs = append(attrs, logsink.Int64("fenceVersion", e.FenceVersion))
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
// the cause, so the log never prints it: it prints ErrorClass instead. A
// failure of a batch write or a cursor advance carries the sequence of the
// first entry of that batch; a failure before the round read a batch
// carries none.
type shipError struct {
	err    error
	step   string
	seq    int64
	hasSeq bool
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
