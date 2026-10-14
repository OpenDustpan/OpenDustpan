package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// The hash chain of the audit log is opt-in (ADR 60 decisions 17 and 19,
// ADR 64). When it is on, each append computes the hash of the entry over
// its fields and the hash of the previous entry of its scope. When it is
// off, an entry holds an empty previous hash and an empty hash. The check
// reads each run of chained entries on its own: an entry without a hash
// ends a run, and the next chained entry starts a new run with an empty
// previous hash.
//
// Each change of the chain writes a switch entry in ServerScope: the kind
// change, the action ActionSetChain, and the value path /audit/chain/on or
// /audit/chain/off. The first start of an installation writes a switch
// entry for the state before it, so the switch entries are the whole
// timeline of the chain. The check accepts an entry without a hash only
// when the timeline says the chain was off at its time, or within
// ChainSwitchLag before it. So a hash that someone removes fails the check.
//
// Every instance reads the state from the store and keeps it for at most
// ChainSwitchLag, so a change reaches every instance within that time.
//
// A new installation starts with the chain off. An installation that
// already holds chained entries keeps the chain on until an administrator
// turns it off on purpose. While chained entries exist and the chain is
// off, the server logs a warning at each start.

// ChainSetting is the operator setting of the chain. The zero value is
// ChainAuto.
type ChainSetting uint8

// Chain settings.
const (
	// ChainAuto keeps the chain on when the installation holds chained
	// entries, and off otherwise. It is the default.
	ChainAuto ChainSetting = iota
	// ChainOn turns the chain on.
	ChainOn
	// ChainOff turns the chain off on purpose.
	ChainOff
)

// String answers the operator value of s: auto, on or off.
func (s ChainSetting) String() string {
	names := [...]string{ChainAuto: "auto", ChainOn: "on", ChainOff: "off"}
	if int(s) < len(names) {
		return names[s]
	}

	return strconv.Itoa(int(s))
}

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

// Action, subject and value path prefix of a switch entry.
const (
	ActionSetChain  = "server:set-audit-chain"
	chainSubject    = "opendustpan serve"
	chainPathPrefix = "/audit/chain/"
)

// ErrInvalidChainSetting reports a chain setting other than auto, on or off.
var ErrInvalidChainSetting = errors.New("invalid audit chain setting: want auto, on or off")

// ParseChainSetting maps an operator value to a setting. An empty value is
// ChainAuto.
func ParseChainSetting(raw string) (ChainSetting, error) {
	if raw == "" {
		return ChainAuto, nil
	}

	for _, setting := range []ChainSetting{ChainAuto, ChainOn, ChainOff} {
		if raw == setting.String() {
			return setting, nil
		}
	}

	return ChainAuto, fmt.Errorf("%w: %q", ErrInvalidChainSetting, raw)
}

// ChainSwitchLag is the longest time an instance keeps the chain state it
// read, so a change of the chain reaches every instance within it. The
// check accepts an entry without a hash that an instance appended within
// this time after the chain went on.
const ChainSwitchLag = 2 * time.Second

// ChainEntry is one switch entry that RecordChain appends in ServerScope,
// and whether it holds a hash.
type ChainEntry struct {
	Entry   Entry
	Chained bool
}

// ChainSwitch is the port that turns the chain on and off. The SQL stores
// implement it next to Store.
type ChainSwitch interface {
	// SetChain makes later appends of this store handle compute the chain
	// (on) or write entries without a hash (off) until the handle reads the
	// recorded state again, at most ChainSwitchLag later. A handle with no
	// recorded state computes the chain.
	SetChain(on bool)
	// HasChainedEntries reports whether any scope holds an entry with a
	// hash. It reads the whole log, so the server calls it only without a
	// record and after a restore.
	HasChainedEntries(ctx context.Context) (bool, error)
	// ChainRecord answers what the installation recorded about its chain.
	// Without a record its State is ChainUnknown.
	ChainRecord(ctx context.Context) (ChainRecord, error)
	// RecordChain appends entries in ServerScope, each with a hash only
	// when it is Chained, and records rec for the installation, in one
	// transaction. rec.State is ChainStateOn or ChainStateOff. It returns
	// ErrInvalidEntry when an entry fails validation or names another
	// scope, and then changes nothing.
	RecordChain(ctx context.Context, rec ChainRecord, entries []ChainEntry) error
}

// ChainPorts holds what ResolveChain needs: the switch and the logger for
// the warning.
type ChainPorts struct {
	Switch ChainSwitch
	Logger *slog.Logger
}

// ResolveChain decides at start whether the chain is on, and sets it on the
// switch of ports.
//
//   - ChainOn and ChainOff decide alone.
//   - ChainAuto keeps the state the installation recorded. With no record,
//     the chain is on when the installation holds chained entries.
//
// Without a record, it first writes a switch entry for the state before the
// start. When the decision differs from the state before it, it writes a
// switch entry with a hash, so a chain that goes off ends with a chained
// entry. It writes the entries and the state in one transaction, and logs a
// warning when the chain is off while chained entries exist.
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

	if err := recordStart(ctx, sw, rec, chainStart{at: now, chained: chained, before: before, after: on}); err != nil {
		return false, err
	}

	sw.SetChain(on)

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

		if err := sw.RecordChain(ctx, rec, nil); err != nil {
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

// chainStart is what a start knows: its time, whether the log holds
// chained entries, and the state of the chain before and after it.
type chainStart struct {
	at      time.Time
	chained bool
	before  bool
	after   bool
}

// recordStart writes the switch entries of a start and the new record in
// one call, when either changes: the start state of an installation with
// no record, and a switch when the state changes.
func recordStart(ctx context.Context, sw ChainSwitch, rec ChainRecord, c chainStart) error {
	states := map[bool]ChainState{true: ChainStateOn, false: ChainStateOff}

	var entries []ChainEntry

	if rec.State == ChainUnknown {
		entries = append(entries, ChainEntry{Entry: chainEntry(states[c.before], c.at), Chained: c.before})
	}

	if c.before != c.after {
		entries = append(entries, ChainEntry{Entry: chainEntry(states[c.after], c.at), Chained: true})
	}

	next := ChainRecord{State: states[c.after], EverChained: c.chained || c.after || c.before != c.after}
	if len(entries) == 0 && next == rec {
		return nil
	}

	if err := sw.RecordChain(ctx, next, entries); err != nil {
		return fmt.Errorf("record audit chain: %w", err)
	}

	return nil
}

// chainEntry returns the switch entry that records the chain as state. The
// value path holds the state.
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
		ValuePath:  chainPathPrefix + string(state),
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

// ChainPoint is one switch entry of the timeline: when the chain went on
// or off.
type ChainPoint struct {
	At time.Time
	On bool
}

// ChainPointFromPath answers the point of a switch entry with the time at
// and the value path path, and false when the path names no state.
func ChainPointFromPath(at time.Time, path string) (ChainPoint, bool) {
	switch path {
	case chainPathPrefix + string(ChainStateOn):
		return ChainPoint{At: at, On: true}, true
	case chainPathPrefix + string(ChainStateOff):
		return ChainPoint{At: at, On: false}, true
	default:
		return ChainPoint{At: at, On: false}, false
	}
}

// ChainTimeline is what the check knows about the history of the chain:
// the trusted switch entries of ServerScope as points, oldest first,
// whether the installation holds a recorded chain state, and whether the
// timeline is broken. An installation with a record always writes its start
// switch entry in the same transaction (ADR 64), so a record with no point
// means that someone removed the switch entries. A broken timeline fails
// every scope.
type ChainTimeline struct {
	Points   []ChainPoint
	Recorded bool
	Broken   bool
}

// TimelineBuilder builds the timeline of the chain from the entries of
// ServerScope. The caller adds them in order of sequence from 1, up to the
// last switch entry. A switch entry counts only when the walk of ServerScope
// accepts it: its hash matches its fields and links it to the entry before
// it. The one exception is a switch entry to off without a hash at sequence
// 1, which a new installation writes at its first start. The first entry
// that the walk refuses, a switch entry without a hash after sequence 1,
// and a switch entry earlier than the switch entry before it break the
// timeline. The zero value is not ready: use NewTimelineBuilder.
type TimelineBuilder struct {
	walk   *ChainWalk
	points []ChainPoint
	broken bool
}

// NewTimelineBuilder returns a builder that expects sequence 1 first.
func NewTimelineBuilder() *TimelineBuilder {
	return &TimelineBuilder{
		walk:   NewChainWalk(ChainTimeline{Points: nil, Recorded: false, Broken: false}),
		points: nil,
		broken: false,
	}
}

// Add adds the next entry of ServerScope and answers false once the
// timeline is broken, so the caller can stop.
func (b *TimelineBuilder) Add(e Entry) bool {
	if b.broken {
		return false
	}

	if p, ok := switchPoint(e); ok {
		if !b.acceptSwitch(e, p) {
			b.broken = true

			return false
		}

		b.points = append(b.points, p)
		b.walk.points = b.points
	}

	if !b.walk.Step(e) {
		b.broken = true

		return false
	}

	return true
}

// Timeline answers the timeline that the builder holds, with recorded as
// whether the installation holds a recorded chain state.
func (b *TimelineBuilder) Timeline(recorded bool) ChainTimeline {
	return ChainTimeline{Points: b.points, Recorded: recorded, Broken: b.broken}
}

// acceptSwitch reports whether the switch entry e with the point p can join
// the timeline before the walk checks its link: it has a hash, or it is the
// switch to off at sequence 1, and its time does not go back.
func (b *TimelineBuilder) acceptSwitch(e Entry, p ChainPoint) bool {
	if e.Hash == "" && (e.Sequence != 1 || p.On || e.PrevHash != "") {
		return false
	}

	n := len(b.points)

	return n == 0 || p.At.UnixMilli() >= b.points[n-1].At.UnixMilli()
}

// switchPoint answers the point of e when e is a switch entry of the chain.
func switchPoint(e Entry) (ChainPoint, bool) {
	if e.Namespace != ServerScope || e.Action != ActionSetChain {
		return ChainPoint{At: e.At, On: false}, false
	}

	return ChainPointFromPath(e.At, e.ValuePath)
}

// ChainWalk checks the entries of one scope in order of sequence. The
// stores use it in Check, so both dialects read the chain the same way.
// The zero value is not ready: use NewChainWalk.
type ChainWalk struct {
	lastAt        time.Time
	lastChainedAt time.Time
	prevHash      string
	points        []ChainPoint
	next          int64
	unchained     int64
	broken        bool
	seenChained   bool
	lastUnchained bool
}

// NewChainWalk returns a walk that expects sequence 1 first, against the
// timeline of the chain.
func NewChainWalk(timeline ChainTimeline) *ChainWalk {
	return &ChainWalk{
		lastAt:        time.Time{},
		lastChainedAt: time.Time{},
		prevHash:      "",
		points:        timeline.Points,
		next:          1,
		unchained:     0,
		broken:        timeline.Broken || (timeline.Recorded && len(timeline.Points) == 0),
		seenChained:   false,
		lastUnchained: false,
	}
}

// Step checks e and answers false at the first entry that does not match:
// a gap in the sequence, a broken link, a hash that does not match the
// fields, or a timeline that someone cut. An entry without a hash must have
// no previous hash and must fit the timeline (see unchainedFits). Next to
// an entry without a hash, the time must not go back, because only the
// hash protects the time of an entry. An entry without a hash ends the
// run, so the next chained entry links to an empty previous hash.
func (w *ChainWalk) Step(e Entry) bool {
	if w.broken || e.Sequence != w.next || w.timeGoesBack(e) {
		return false
	}

	w.next++
	w.lastAt = e.At

	if e.Hash == "" {
		if e.PrevHash != "" || !w.unchainedFits(e.At) {
			return false
		}

		w.unchained++
		w.prevHash = ""
		w.lastUnchained = true

		return true
	}

	if e.PrevHash != w.prevHash || ComputeHash(e, e.PrevHash) != e.Hash {
		return false
	}

	w.prevHash = e.Hash
	w.seenChained = true
	w.lastChainedAt = e.At
	w.lastUnchained = false

	return true
}

// Unchained answers the count of entries without a hash so far.
func (w *ChainWalk) Unchained() int64 {
	return w.unchained
}

// timeGoesBack reports whether e is earlier than the entry before it, when
// e or the entry before it has no hash.
func (w *ChainWalk) timeGoesBack(e Entry) bool {
	if w.next == 1 || (e.Hash != "" && !w.lastUnchained) {
		return false
	}

	return e.At.UnixMilli() < w.lastAt.UnixMilli()
}

// unchainedFits reports whether an entry without a hash at t fits the
// timeline. The chain was off at t (see offAt). After a chained entry of
// the scope, the chain also went off after that entry, or t is within
// ChainSwitchLag after a switch to on. Without a timeline, an entry without
// a hash fits only before the first chained entry of its scope.
func (w *ChainWalk) unchainedFits(t time.Time) bool {
	if len(w.points) == 0 {
		return !w.seenChained
	}

	if !w.offAt(t) {
		return false
	}

	if !w.seenChained {
		return true
	}

	return w.onAt(t) || w.offSince(w.lastChainedAt.Add(-ChainSwitchLag))
}

// offSince reports whether the timeline holds a switch to off at t or
// later.
func (w *ChainWalk) offSince(t time.Time) bool {
	ms := t.UnixMilli()

	for _, p := range w.points {
		if !p.On && p.At.UnixMilli() >= ms {
			return true
		}
	}

	return false
}

// offAt reports whether an entry without a hash at t matches the timeline:
// the chain was off at t, or at t minus ChainSwitchLag, when an instance
// could still hold the state before a switch to on. It needs at least one
// point.
func (w *ChainWalk) offAt(t time.Time) bool {
	return !w.onAt(t) || !w.onAt(t.Add(-ChainSwitchLag))
}

// onAt answers the state of the chain at t: the state of the latest point
// at or before t, at millisecond precision. Before the first point, the
// first point holds the state, because the first start writes a point for
// the state before it.
func (w *ChainWalk) onAt(t time.Time) bool {
	ms := t.UnixMilli()
	state := w.points[0].On

	for _, p := range w.points {
		if p.At.UnixMilli() > ms {
			break
		}

		state = p.On
	}

	return state
}
