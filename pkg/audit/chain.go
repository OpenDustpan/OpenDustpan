package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// The hash chain of the audit log is opt-in (ADR 60 decisions 17 and 19).
// When it is on, each append computes the hash of the entry over its fields
// and the hash of the previous entry of its scope. When it is off, an entry
// holds an empty previous hash and an empty hash. The check reads each run
// of chained entries on its own: an entry without a hash ends a run, and
// the next chained entry starts a new run with an empty previous hash. An
// entry without a hash after a chained entry of its scope is valid only
// when a chained entry of ServerScope that turned the chain off comes
// between the two in time. So a hash that someone removes from a chained
// log fails the check.
//
// A new installation starts with the chain off. An installation that
// already holds chained entries keeps the chain on until an administrator
// turns it off on purpose. A change of the chain writes an entry of the
// kind change in ServerScope. While chained entries exist and the chain is
// off, the server logs a warning at each start.

// ChainSetting is the operator setting of the chain.
type ChainSetting string

// Chain settings.
const (
	// ChainAuto keeps the chain on when the installation holds chained
	// entries, and off otherwise. It is the default.
	ChainAuto ChainSetting = "auto"
	// ChainOn turns the chain on.
	ChainOn ChainSetting = "on"
	// ChainOff turns the chain off on purpose.
	ChainOff ChainSetting = "off"
)

// ChainState is the state of the chain that the installation recorded
// last. ChainUnknown means the installation never recorded one.
type ChainState string

// ChainRecord is what the installation recorded about its chain: the state
// and whether its log ever held a chained entry. A backup does not carry
// it: a restore keeps the record of the target installation.
type ChainRecord struct {
	State       ChainState
	EverChained bool
}

// Chain states.
const (
	ChainUnknown  ChainState = ""
	ChainStateOn  ChainState = "on"
	ChainStateOff ChainState = "off"
)

// Action and object of the entry that records a change of the chain.
const (
	ActionSetChain = "server:set-audit-chain"
	chainSubject   = "opendustpan serve"
)

// ErrInvalidChainSetting reports a chain setting other than auto, on or off.
var ErrInvalidChainSetting = errors.New("invalid audit chain setting: want auto, on or off")

// ParseChainSetting maps an operator value to a setting. An empty value is
// ChainAuto.
func ParseChainSetting(raw string) (ChainSetting, error) {
	switch ChainSetting(raw) {
	case "", ChainAuto:
		return ChainAuto, nil
	case ChainOn, ChainOff:
		return ChainSetting(raw), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidChainSetting, raw)
	}
}

// ChainSwitch is the port that turns the chain on and off. The SQL stores
// implement it next to Store.
type ChainSwitch interface {
	// SetChain makes later appends of this store handle compute the chain
	// (on) or write entries without a hash (off). A new handle computes the
	// chain until the server sets it.
	SetChain(on bool)
	// HasChainedEntries reports whether any scope holds an entry with a
	// hash. It reads the whole log, so the server calls it only without a
	// record and after a restore.
	HasChainedEntries(ctx context.Context) (bool, error)
	// ChainRecord answers what the installation recorded about its chain.
	// Without a record its State is ChainUnknown.
	ChainRecord(ctx context.Context) (ChainRecord, error)
	// SaveChainRecord records rec for the installation. rec.State is
	// ChainStateOn or ChainStateOff.
	SaveChainRecord(ctx context.Context, rec ChainRecord) error
}

// ChainPorts holds what ResolveChain needs: the switch, the server scope
// for the change entry, and the logger for the warning.
type ChainPorts struct {
	Switch ChainSwitch
	Server ServerStore
	Logger *slog.Logger
}

// ResolveChain decides at start whether the chain is on, and sets it on the
// switch of ports.
//
//   - ChainOn and ChainOff decide alone.
//   - ChainAuto keeps the state the installation recorded. With no record,
//     the chain is on when the installation holds chained entries.
//
// When the decision differs from the state before it, ResolveChain appends
// one entry of the kind change in ServerScope. It appends
// that entry while the chain is still on, so a chain that goes off ends
// with a chained entry. It then records the state, and logs a warning when
// the chain is off while chained entries exist.
func ResolveChain(ctx context.Context, ports ChainPorts, setting ChainSetting, now time.Time) (bool, error) {
	sw := ports.Switch

	rec, err := sw.ChainRecord(ctx)
	if err != nil {
		return false, fmt.Errorf("read audit chain state: %w", err)
	}

	// Only an installation without a record reads the whole log: the record
	// keeps the answer after that.
	chained := rec.EverChained
	if rec.State == ChainUnknown {
		chained, err = sw.HasChainedEntries(ctx)
		if err != nil {
			return false, fmt.Errorf("read audit chain: %w", err)
		}
	}

	before := rec.State == ChainStateOn || (rec.State == ChainUnknown && chained)

	on, err := decideChain(setting, before)
	if err != nil {
		return false, err
	}

	change := chainChange{at: now, before: before, after: on, everChained: chained}
	if err := applyChain(ctx, sw, ports.Server, change); err != nil {
		return false, err
	}

	if !on && chained {
		warnChainOff(ctx, ports.Logger)
	}

	return on, nil
}

// RecheckChain reads the chain record again after a restore. The restore
// keeps the record of the target installation and never changes its state.
// When the restored log holds chained entries, the record notes it, and
// while the chain is off the server logs the warning of a start.
func RecheckChain(ctx context.Context, ports ChainPorts) error {
	sw := ports.Switch

	rec, err := sw.ChainRecord(ctx)
	if err != nil {
		return fmt.Errorf("read audit chain state: %w", err)
	}

	chained, err := sw.HasChainedEntries(ctx)
	if err != nil {
		return fmt.Errorf("read audit chain: %w", err)
	}

	if !chained {
		return nil
	}

	if !rec.EverChained && rec.State != ChainUnknown {
		rec.EverChained = true

		if err := sw.SaveChainRecord(ctx, rec); err != nil {
			return fmt.Errorf("save audit chain state: %w", err)
		}
	}

	if rec.State == ChainStateOff {
		warnChainOff(ctx, ports.Logger)
	}

	return nil
}

// warnChainOff logs the warning for a chain that is off while the log
// holds chained entries.
func warnChainOff(ctx context.Context, logger *slog.Logger) {
	if logger != nil {
		logger.WarnContext(ctx, "the audit chain is off and the audit log holds chained entries: "+
			"new entries carry no hash; set --audit-chain=on to turn the chain on again")
	}
}

// decideChain answers the chain after a start: on and off decide alone,
// and auto keeps the state before.
func decideChain(setting ChainSetting, before bool) (bool, error) {
	switch setting {
	case ChainOn:
		return true, nil
	case ChainOff:
		return false, nil
	case ChainAuto:
		return before, nil
	default:
		return false, fmt.Errorf("%w: %q", ErrInvalidChainSetting, setting)
	}
}

// chainChange is the state of the chain before and after a start, and
// whether the log held chained entries before it.
type chainChange struct {
	at          time.Time
	before      bool
	after       bool
	everChained bool
}

// applyChain sets the chain, appends the change entry when the state
// changes, and records the state.
func applyChain(ctx context.Context, sw ChainSwitch, server ServerStore, c chainChange) error {
	state := ChainStateOff
	if c.after {
		state = ChainStateOn
	}

	if c.before != c.after {
		// The entry is chained when the chain was on before or is on after.
		sw.SetChain(true)

		if _, err := server.AppendServer(ctx, chainEntry(state, c.at)); err != nil {
			return fmt.Errorf("record audit chain change: %w", err)
		}
	}

	sw.SetChain(c.after)

	// The change entry is chained, and a chain that is on chains the next
	// entry, so either makes the log hold a chained entry.
	rec := ChainRecord{State: state, EverChained: c.everChained || c.after || c.before != c.after}

	if err := sw.SaveChainRecord(ctx, rec); err != nil {
		return fmt.Errorf("save audit chain state: %w", err)
	}

	return nil
}

// chainEntry returns the entry that records the chain turning on or off.
// The value path holds the new state.
func chainEntry(state ChainState, at time.Time) Entry {
	return Entry{
		At:         at,
		Namespace:  ServerScope,
		Subject:    chainSubject,
		IDPSubject: "",
		TokenID:    "",
		Action:     ActionSetChain,
		Object:     ServerScope.String(),
		Version:    "",
		ValuePath:  "/audit/chain/" + string(state),
		ValueCount: nil,
		SourceIP:   "",
		UserAgent:  "",
		Channel:    ChannelAPI,
		Kind:       KindChange,
		Outcome:    OutcomeSuccess,
		RequestID:  "",
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}
}

// CheckScopes runs the chain check of each scope in scopes and answers the
// count of scopes whose check fails. A scope whose check answers an error
// counts as failed too, and the error joins the answer.
func CheckScopes(ctx context.Context, store Store, scopes []namespace.Name) (int, error) {
	var (
		invalid int
		errs    []error
	)

	for _, scope := range scopes {
		result, err := store.Check(ctx, scope)
		if err != nil {
			invalid++

			errs = append(errs, fmt.Errorf("check %s: %w", scope, err))

			continue
		}

		if !result.Valid {
			invalid++
		}
	}

	return invalid, errors.Join(errs...)
}

// ChainWalk checks the entries of one scope in order of sequence. The
// stores use it in Check, so both dialects read the chain the same way.
// The zero value is not ready: use NewChainWalk.
type ChainWalk struct {
	offTimes    []time.Time
	lastChained time.Time
	prevHash    string
	next        int64
	unchained   int64
	seenChained bool
}

// NewChainWalk returns a walk that expects sequence 1 first. offTimes holds
// the times of the chained entries of ServerScope that turned the chain off
// (ActionSetChain with the value path /audit/chain/off).
func NewChainWalk(offTimes []time.Time) *ChainWalk {
	return &ChainWalk{
		offTimes:    offTimes,
		lastChained: time.Time{},
		prevHash:    "",
		next:        1,
		unchained:   0,
		seenChained: false,
	}
}

// Step checks e and answers false at the first entry that does not match:
// a gap in the sequence, a broken link, or a hash that does not match the
// fields. An entry without a hash must have no previous hash. It ends the
// run, so the next chained entry links to an empty previous hash. After a
// chained entry of the scope, an entry without a hash matches only when the
// chain was turned off between the two.
func (w *ChainWalk) Step(e Entry) bool {
	if e.Sequence != w.next {
		return false
	}

	w.next++

	if e.Hash == "" {
		if e.PrevHash != "" || (w.seenChained && !w.offBetween(w.lastChained, e.At)) {
			return false
		}

		w.unchained++
		w.prevHash = ""

		return true
	}

	if e.PrevHash != w.prevHash || ComputeHash(e, e.PrevHash) != e.Hash {
		return false
	}

	w.prevHash = e.Hash
	w.lastChained = e.At
	w.seenChained = true

	return true
}

// Unchained answers the count of entries without a hash so far.
func (w *ChainWalk) Unchained() int64 {
	return w.unchained
}

// offBetween reports whether the chain was turned off at a time in the
// range from start to end, at millisecond precision, as entries store
// their time.
func (w *ChainWalk) offBetween(start, end time.Time) bool {
	startMs, endMs := start.UnixMilli(), end.UnixMilli()

	for _, t := range w.offTimes {
		if ms := t.UnixMilli(); ms >= startMs && ms <= endMs {
			return true
		}
	}

	return false
}
