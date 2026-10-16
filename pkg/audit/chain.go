package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// The hash chain of the audit log is a start setting. Each append
// of a chained scope computes the hash of the entry over its fields and the
// hash of the previous entry of its scope. The database keeps the state of
// the chain. A start whose setting differs from that state starts the
// server in maintenance mode, and a person approves the change, which runs
// a migration:
//
//   - On: each scope gets a start entry. It holds a hash with an empty
//     previous hash and the digest of every earlier entry of its scope, so
//     the earlier rows keep their content and a verifier can prove that they
//     did not change after the chain started.
//   - Off: each scope gets an end entry. It holds a hash linked to the entry
//     before it, so the chain ends with a chained entry. Later entries hold
//     no hash.
//
// The check of a scope starts at its last start entry, or at sequence 1
// when the scope holds none, and follows the chain with no gap up to now or
// up to an end entry.

// ChainSetting is the operator setting of the chain. The zero value is
// ChainOn, the default.
type ChainSetting uint8

// Chain settings.
const (
	// ChainOn computes the chain. It is the default.
	ChainOn ChainSetting = iota
	// ChainOff writes entries without a hash.
	ChainOff
)

// String answers the operator value of s: on or off.
func (s ChainSetting) String() string {
	names := [...]string{ChainOn: "on", ChainOff: "off"}
	if int(s) < len(names) {
		return names[s]
	}

	return strconv.Itoa(int(s))
}

// State answers the chain state that s asks for.
func (s ChainSetting) State() ChainState {
	if s == ChainOff {
		return ChainStateOff
	}

	return ChainStateOn
}

// ChainState is the state of the chain that the installation keeps.
type ChainState string

// Chain states.
const (
	ChainStateOn  ChainState = "on"
	ChainStateOff ChainState = "off"
)

// ChainRecord is what the installation keeps about its chain: the state,
// when it last changed and who changed it. Pending reports that a change of
// the chain waits for a person to approve it, which keeps the installation
// in maintenance mode. EnteredMaintenance reports whether the chain entered
// maintenance mode itself. MigratingAt holds the start of the running
// migration, zero when none runs. LogReplaced reports that the swap of a
// restore replaced the log and the server has not compared it with the
// state yet. A backup does not carry the record: a restore keeps the record
// of the target installation. An installation without a record reads as
// ChainStateOn, because the chain was on in every installation before it
// had a setting.
type ChainRecord struct {
	ChangedAt          time.Time
	ChangedBy          string
	State              ChainState
	Pending            bool
	EnteredMaintenance bool
	MigratingAt        time.Time
	LogReplaced        bool
}

// Actions of the entries that the chain writes, and the keys of the changes
// of a start entry.
const (
	// ActionSetChain is the entry in ServerScope that records an approved
	// change of the chain.
	ActionSetChain = "server:set-audit-chain"
	// ActionStartChain is the start entry of the chain of a scope.
	ActionStartChain = "server:start-audit-chain"
	// ActionEndChain is the end entry of the chain of a scope.
	ActionEndChain = "server:end-audit-chain"
	// ChangeDigest holds the digest of the earlier entries of a start entry.
	ChangeDigest = "digest"
	// ChangeEntries holds the count of the earlier entries of a start entry.
	ChangeEntries = "entries"

	chainPathPrefix = "/audit/chain/"
)

// ErrInvalidChainSetting reports a chain setting other than on or off.
var ErrInvalidChainSetting = errors.New("invalid audit chain setting: want on or off")

// ChainMigrationLease bounds how long a migration that stops without a
// release blocks a new approval.
const ChainMigrationLease = 15 * time.Minute

// Errors of the approval of a change of the chain.
var (
	// ErrNoChainChange reports an approval while no change of the chain
	// waits.
	ErrNoChainChange = errors.New("no audit chain change waits for approval")
	// ErrChainMigrating reports an approval or a restore while the
	// migration of an approved change runs.
	ErrChainMigrating = errors.New("an audit chain migration runs")
	// ErrChainChangeWaits reports a restore while a change of the chain
	// waits for approval.
	ErrChainChangeWaits = errors.New("an audit chain change waits for approval")
	// ErrChainStepRequired reports an approval of a change to off without
	// a fresh MFA step.
	ErrChainStepRequired = errors.New("turning the audit chain off needs a fresh MFA step")
	// ErrChainNeedsPerson reports an approval of a change to off by a
	// service principal. Only a person may approve turning the chain off.
	ErrChainNeedsPerson = errors.New("a person must approve turning the audit chain off")
	// ErrChainClaimLost reports a renewal while no claim holds. The
	// migration stops when it loses its claim.
	ErrChainClaimLost = errors.New("the audit chain migration lost its claim")
	// ErrChainMigrationsStopped reports an approval while the server runs
	// no migration loop.
	ErrChainMigrationsStopped = errors.New(
		"the server does not run audit chain migrations now: restart the server and approve again",
	)
)

// ParseChainSetting maps an operator value to a setting. An empty value is
// ChainOn.
func ParseChainSetting(raw string) (ChainSetting, error) {
	switch raw {
	case "", ChainOn.String():
		return ChainOn, nil
	case ChainOff.String():
		return ChainOff, nil
	default:
		return ChainOn, fmt.Errorf("%w: %q", ErrInvalidChainSetting, raw)
	}
}

// ChainStore is the port of the chain state and of its migration. The SQL
// stores implement it next to Store.
type ChainStore interface {
	// ChainRecord answers the record of the installation. Without a row it
	// answers ChainStateOn with a zero time.
	ChainRecord(ctx context.Context) (ChainRecord, error)
	// SaveChainRecord stores rec as the record of the installation.
	SaveChainRecord(ctx context.Context, rec ChainRecord) error
	// ClaimChainMigration starts the migration of a waiting change in one
	// conditional write: it stamps now as the start only when a change waits
	// and no claim within lease holds. It returns ErrNoChainChange when no
	// change waits, and ErrChainMigrating when a claim within the lease
	// holds.
	ClaimChainMigration(ctx context.Context, now time.Time, lease time.Duration) error
	// ReleaseChainMigration releases the claim of a migration that stops
	// without storing the new state.
	ReleaseChainMigration(ctx context.Context) error
	// RenewChainMigration moves the start of the held claim to now, so a
	// long migration keeps its claim within the lease. It writes only when
	// a claim holds, and returns ErrChainClaimLost when none holds.
	RenewChainMigration(ctx context.Context, now time.Time) error
	// StartChain appends e as the start entry of its scope, in one
	// transaction with the read of the earlier entries and the check of
	// fence: its changes get the digest and the count of those entries (see
	// StartChanges), and its hash an empty previous hash. A fence of
	// ServerScope checks nothing. It returns ErrInvalidEntry when e fails
	// validation or names another scope than fence, and
	// namespace.ErrFenced when the fence is stale. When the scope holds no
	// entry, or its last entry already is a start entry (see MarkerDone),
	// it appends nothing and returns false, read in the same transaction.
	// So two migrations write one start entry.
	StartChain(ctx context.Context, fence namespace.Fence, e Entry) (bool, error)
	// EndChain appends e as the end entry of its scope, with a hash linked
	// to the hash of the entry before it, in one transaction with the check
	// of fence. It returns the errors of StartChain, and skips a scope that
	// holds no entry or already agrees with the chain off, as StartChain
	// does.
	EndChain(ctx context.Context, fence namespace.Fence, e Entry) (bool, error)
	// LastEntry answers the entry with the highest sequence of scope, and
	// false when the scope holds none.
	LastEntry(ctx context.Context, scope namespace.Name) (Entry, bool, error)
}

// ServerFence answers the fence of ServerScope for MigrateChain. The scope
// belongs to no namespace, so the stores check no fence for it.
func ServerFence() namespace.Fence {
	return namespace.Fence{Namespace: ServerScope, FailoverVersion: 0}
}

// ChainChange is one approved change of the chain: the state it moves to,
// who approved it and when.
type ChainChange struct {
	At time.Time
	By string
	To ChainState
}

// MigrateChain runs the migration of an approved change in the scope of
// each fence of fences. To off, it writes an end entry in each scope, then
// stores the new record with the state off and no wait. To on, it first
// stores the new record with the state on and a wait. Then it writes a
// start entry in each scope. Then it stores the same record with no wait.
// The early store to on keeps the claim of the approval, so a second
// approval still sees the migration. The final store clears the claim.
// After each scope it renews the claim with now, so a long migration keeps
// a second approval out. A nil now reads time.Now. A renewal error stops
// the migration. Appends in a scope hash only after its start entry, or at
// once when the scope holds no entries. The digest of the start entry
// covers an append before it. An append after an end entry but before the
// stored state changes writes no hash, because the last entry is an end
// entry. A scope without entries gets no entry: its first entry starts the
// chain, or comes after the chain went off. A second start entry does no
// harm, because its digest covers the first.
func MigrateChain(
	ctx context.Context, store ChainStore, fences []namespace.Fence, change ChainChange, now func() time.Time,
) error {
	clock := now
	if clock == nil {
		clock = time.Now
	}

	current, err := store.ChainRecord(ctx)
	if err != nil {
		return fmt.Errorf("read audit chain state: %w", err)
	}

	if change.To == ChainStateOn {
		early := ChainRecord{
			ChangedAt: change.At, ChangedBy: change.By, State: change.To, Pending: true,
			EnteredMaintenance: current.EnteredMaintenance,
			MigratingAt:        current.MigratingAt, LogReplaced: false,
		}
		if err := store.SaveChainRecord(ctx, early); err != nil {
			return fmt.Errorf("save audit chain state: %w", err)
		}
	}

	for _, fence := range fences {
		if err := migrateScope(ctx, store, fence, change); err != nil {
			return err
		}

		if err := store.RenewChainMigration(ctx, clock()); err != nil {
			return fmt.Errorf("renew audit chain migration: %w", err)
		}
	}

	rec := ChainRecord{
		ChangedAt: change.At, ChangedBy: change.By, State: change.To, Pending: false,
		EnteredMaintenance: current.EnteredMaintenance,
		MigratingAt:        time.Time{}, LogReplaced: false,
	}
	if err := store.SaveChainRecord(ctx, rec); err != nil {
		return fmt.Errorf("save audit chain state: %w", err)
	}

	return nil
}

// migrateScope writes the start or the end entry of change in the scope of
// fence. The store skips a scope that holds no entry or already agrees.
func migrateScope(ctx context.Context, store ChainStore, fence namespace.Fence, change ChainChange) error {
	scope := fence.Namespace

	if change.To == ChainStateOn {
		if _, err := store.StartChain(ctx, fence, chainMarker(scope, ActionStartChain, change)); err != nil {
			return fmt.Errorf("start the audit chain of %s: %w", scope, err)
		}

		return nil
	}

	if _, err := store.EndChain(ctx, fence, chainMarker(scope, ActionEndChain, change)); err != nil {
		return fmt.Errorf("end the audit chain of %s: %w", scope, err)
	}

	return nil
}

// LastAgrees reports whether a scope whose last entry is last agrees with
// state: with the chain on, the last entry is a chained entry that is not an
// end entry; with the chain off, it holds no hash or it is an end entry. A
// scope without entries (last nil) agrees.
func LastAgrees(last *Entry, state ChainState) bool {
	if last == nil {
		return true
	}

	chained := last.Hash != "" && last.Action != ActionEndChain

	return chained == (state == ChainStateOn)
}

// MarkerDone reports whether the migration to state needs no marker in a
// scope whose last entry is last. A scope without entries (last nil) needs
// none. To on, only a start entry as the last entry makes the marker done:
// a hashed last entry is not enough, because an append can hash after the
// state is stored as on and before the start entry of its scope, and the
// start entry must still cover the earlier entries with its digest. To
// off, the scope needs no marker when it agrees with off (see LastAgrees).
func MarkerDone(last *Entry, state ChainState) bool {
	if last == nil {
		return true
	}

	if state == ChainStateOn {
		return last.Action == ActionStartChain && last.Hash != ""
	}

	return LastAgrees(last, state)
}

// LogAgrees reports whether the log of every scope of scopes agrees with
// state, as LastAgrees decides for each scope.
func LogAgrees(ctx context.Context, store ChainStore, scopes []namespace.Name, state ChainState) (bool, error) {
	for _, scope := range scopes {
		last, found, err := store.LastEntry(ctx, scope)
		if err != nil {
			return false, fmt.Errorf("read the last audit entry of %s: %w", scope, err)
		}

		if !found {
			continue
		}

		if !LastAgrees(&last, state) {
			return false, nil
		}
	}

	return true, nil
}

// ChainSetEntry returns the entry in ServerScope that records an approved
// change. It keeps who acted and from where from caller: the subject, the
// token, the address, the user agent, the channel and the request.
func ChainSetEntry(caller Entry, change ChainChange) Entry {
	e := chainMarker(ServerScope, ActionSetChain, change)
	e.Subject = caller.Subject
	e.IDPSubject = caller.IDPSubject
	e.TokenID = caller.TokenID
	e.SourceIP = caller.SourceIP
	e.UserAgent = caller.UserAgent
	e.Channel = caller.Channel
	e.RequestID = caller.RequestID
	e.ValuePath = chainPathPrefix + string(change.To)

	return e
}

// chainMarker returns an entry of change with action in scope, by the
// person who approved it.
func chainMarker(scope namespace.Name, action string, change ChainChange) Entry {
	return Entry{
		At:           change.At,
		Namespace:    scope,
		Subject:      change.By,
		IDPSubject:   "",
		TokenID:      "",
		Action:       action,
		Object:       scope.String(),
		ObjectID:     "",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      ChannelAPI,
		Kind:         KindChange,
		Outcome:      OutcomeSuccess,
		RequestID:    "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

// digestSeparator joins the parts of one step of the digest.
const digestSeparator = "\x1f"

// Digest is the running digest of the entries of a scope before a start
// entry. It starts as the empty string. For each entry,
// in order of sequence, the next digest is the hex SHA-256 of the previous
// digest, the entry hash, the stored previous hash and the stored hash,
// joined with the unit separator. The entry hash is ComputeStoredHash of
// the entry with an empty previous hash. The zero value is ready.
type Digest struct {
	sum   string
	count int64
}

// Add adds the stored entry e, with changesText as the store holds it.
func (d *Digest) Add(e Entry, changesText string) {
	leaf := ComputeStoredHash(e, "", changesText)
	step := sha256.Sum256(
		[]byte(d.sum + digestSeparator + leaf + digestSeparator + e.PrevHash + digestSeparator + e.Hash),
	)

	d.sum = hex.EncodeToString(step[:])
	d.count++
}

// Sum answers the digest so far.
func (d *Digest) Sum() string {
	return d.sum
}

// Count answers the count of entries so far.
func (d *Digest) Count() int64 {
	return d.count
}

// StartChanges answers the changes of a start entry after the entries that
// d covers.
func StartChanges(d *Digest) []Change {
	return []Change{
		{Key: ChangeDigest, Old: nil, New: d.Sum(), Layer: "", TakesEffect: ""},
		{Key: ChangeEntries, Old: nil, New: strconv.FormatInt(d.Count(), 10), Layer: "", TakesEffect: ""},
	}
}

// ChainScope is what the check of one scope knows besides its entries: the
// record of the installation and the sequence of the last start entry of
// the scope, 0 when the scope holds none.
type ChainScope struct {
	Record ChainRecord
	Start  int64
}

// Phases of a ChainWalk.
const (
	phaseDigest = iota + 1
	phaseChain
	phaseEnded
	phaseNoChain
)

// ChainWalk checks the entries of one scope in order of sequence. The stores use it in Check, so both dialects read the
// chain
// the same way. Before the start entry, it also checks each entry that
// holds a hash against its own fields and stored previous hash. The zero
// value is not ready: use NewChainWalk.
type ChainWalk struct {
	scope     ChainScope
	digest    Digest
	prevHash  string
	next      int64
	unchained int64
	phase     int
	failed    bool
}

// NewChainWalk returns a walk that expects sequence 1 first.
func NewChainWalk(scope ChainScope) *ChainWalk {
	phase := phaseChain
	if scope.Start > 0 {
		phase = phaseDigest
	}

	return &ChainWalk{
		scope:     scope,
		digest:    Digest{sum: "", count: 0},
		prevHash:  "",
		next:      1,
		unchained: 0,
		phase:     phase,
		failed:    false,
	}
}

// StepStored checks the stored entry e, with changesText as the store holds
// it, and answers false at the first entry that does not match: a gap, a
// broken link, a hash that does not match the fields, a digest that does
// not match the earlier entries, or an entry without a hash inside the
// chain. Before the start entry, an entry that holds a hash must match its
// own fields and its stored previous hash.
func (w *ChainWalk) StepStored(e Entry, changesText string) bool {
	if w.failed || e.Sequence != w.next {
		w.failed = true

		return false
	}

	w.next++

	ok := w.advance(e, changesText)
	if !ok {
		w.failed = true
	}

	if e.Hash == "" {
		w.unchained++
	}

	return ok
}

// Step is StepStored with the changes text of e.Changes.
func (w *ChainWalk) Step(e Entry) bool {
	return w.StepStored(e, EncodeChanges(e.Changes))
}

// Finish answers whether the scope ends in a state that agrees with the
// record, after the last entry: with the chain on, a scope must not end
// after an end entry; with the chain off, a scope with entries must end
// after an end entry or have no chain. A walk whose start entry never came
// fails.
func (w *ChainWalk) Finish() bool {
	if w.failed {
		return false
	}

	if w.next == 1 {
		return true
	}

	switch w.phase {
	case phaseDigest:
		return false
	case phaseEnded:
		return w.scope.Record.State == ChainStateOff
	case phaseChain:
		return w.scope.Record.State == ChainStateOn
	default:
		return true
	}
}

// Unchained answers the count of entries without a hash so far.
func (w *ChainWalk) Unchained() int64 {
	return w.unchained
}

// advance checks e in the current phase.
func (w *ChainWalk) advance(e Entry, changesText string) bool {
	switch w.phase {
	case phaseDigest:
		return w.stepDigest(e, changesText)
	case phaseChain:
		return w.stepChain(e, changesText)
	case phaseEnded, phaseNoChain:
		return e.Hash == "" && e.PrevHash == ""
	default:
		return false
	}
}

// stepDigest adds an entry before the start entry to the digest, checks each
// earlier entry that holds a hash against its own fields and stored previous
// hash, and checks the start entry against the digest. It does not check the
// links between earlier entries.
func (w *ChainWalk) stepDigest(e Entry, changesText string) bool {
	if e.Sequence < w.scope.Start {
		if e.Hash != "" && ComputeStoredHash(e, e.PrevHash, changesText) != e.Hash {
			return false
		}

		w.digest.Add(e, changesText)

		return true
	}

	if e.Action != ActionStartChain || e.PrevHash != "" || e.Hash == "" ||
		changesText != EncodeChanges(StartChanges(&w.digest)) {
		return false
	}

	w.phase = phaseChain

	return w.stepChain(e, changesText)
}

// stepChain checks a chained entry and its link.
func (w *ChainWalk) stepChain(e Entry, changesText string) bool {
	if e.Hash == "" {
		// A scope whose first entry holds no hash has no chain. That fits
		// only a scope that began after the chain went off.
		if e.Sequence == 1 && w.scope.Start == 0 && e.PrevHash == "" && w.beganWhileOff(e) {
			w.phase = phaseNoChain

			return true
		}

		return false
	}

	if e.PrevHash != w.prevHash || ComputeStoredHash(e, e.PrevHash, changesText) != e.Hash {
		return false
	}

	w.prevHash = e.Hash

	if e.Action == ActionEndChain {
		w.phase = phaseEnded
	}

	return true
}

// beganWhileOff reports whether e, the first entry of a scope, comes after
// the chain went off.
func (w *ChainWalk) beganWhileOff(e Entry) bool {
	rec := w.scope.Record

	return rec.State == ChainStateOff && !rec.ChangedAt.IsZero() && e.At.UnixMilli() >= rec.ChangedAt.UnixMilli()
}

// ScopesChecker is the port that checks many scopes in one read of the
// record. The SQL stores implement it next to Store: they read the record
// once and walk every scope in one read transaction.
type ScopesChecker interface {
	// CheckEach answers the result of Check for each scope of scopes, in
	// order. It answers an error when a read fails, and then no result.
	CheckEach(ctx context.Context, scopes []namespace.Name) ([]CheckResult, error)
}

// CheckScopes runs the chain check of each scope in scopes and answers the
// count of scopes whose check fails. A store that implements ScopesChecker
// checks them all in one call. A scope whose check answers an error counts
// as failed too, and the error joins the answer.
func CheckScopes(ctx context.Context, store Store, scopes []namespace.Name) (int, error) {
	if checker, ok := store.(ScopesChecker); ok {
		return checkEach(ctx, checker, scopes)
	}

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

// checkEach runs CheckScopes with one call of checker. A failed call
// counts every scope as failed.
func checkEach(ctx context.Context, checker ScopesChecker, scopes []namespace.Name) (int, error) {
	results, err := checker.CheckEach(ctx, scopes)
	if err != nil {
		return len(scopes), fmt.Errorf("check scopes: %w", err)
	}

	invalid := len(scopes) - len(results)

	for i := range results {
		if !results[i].Valid {
			invalid++
		}
	}

	return invalid, nil
}
