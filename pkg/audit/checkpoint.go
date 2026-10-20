package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Signed checkpoints are opt-in. A checkpoint signs the
// hash at one sequence of a scope with an Ed25519 key. It holds the epoch,
// the scope, the sequence and the hash. It goes to the blob
// store under a key that names the epoch, the scope and the sequence, so two
// instances that write the checkpoint of one sequence write it once. A copy
// of the database that an attacker changes and chains again does not match
// the checkpoints that the server signed before: the attacker does not hold
// the signing key.
//
// An epoch groups the checkpoints of one history of the log. A restore
// starts a new epoch, because the restored log can lose entries that older
// checkpoints sign. An administrator can also start a new epoch, after
// checking why a checkpoint does not match. The check reads only the
// checkpoints of the current epoch. An epoch is a signed object under
// audit-checkpoints/epochs/, so the current epoch needs no database. The
// server signs it with the same key as the checkpoints. A bad epoch object
// fails the check and stops new signatures.
//
// Every check reads the newest checkpoint of each scope and fails when the
// log ends before it, so a cut at the end of the log shows at the next
// check. A mismatch stays until a new epoch starts: later checks of the scope
// fail, and the server signs nothing more for it. With a CheckStore the first
// mismatch of a scope in an epoch is also stored in a row, so every instance
// sees it and stops signing of that scope while it stands. Without a CheckStore it stays in the memory of
// the process.

// CheckpointPrefix is the first segment of every checkpoint key. No other
// writer may use it.
const CheckpointPrefix = "audit-checkpoints"

const (
	// checkpointKeyIDLength is the count of hexadecimal characters of a
	// key ID.
	checkpointKeyIDLength = 16
	// checkpointMaxBytes caps the size of a checkpoint object that the
	// check reads.
	checkpointMaxBytes = 4 << 10
	// checkpointSequenceDigits pads the sequence in a key, so the keys of
	// one scope list in order of sequence.
	checkpointSequenceDigits = 20
	// checkpointEpochDigits pads the epoch in a key.
	checkpointEpochDigits = 8
	// checkpointContext starts the signed message, so a signature of a
	// checkpoint is never valid for other data.
	checkpointContext = "opendustpan audit checkpoint v1"
	// epochContext starts the signed message of an epoch object, so a
	// signature of an epoch is never valid for a checkpoint.
	epochContext = "opendustpan audit checkpoint epoch v1"
	// epochDir is the key directory of the epoch objects.
	epochDir = CheckpointPrefix + "/epochs/"
	// MaxNewCheckpoints caps the checkpoints newer than the last good one
	// that one check reads for a scope. Older ones join the sample pool.
	MaxNewCheckpoints = 256
	// CheckpointSample is the count of older checkpoints that one check
	// reads at random for a scope, so a change to an old checkpoint shows
	// in time without a read of every checkpoint.
	CheckpointSample = 8
)

// Checkpoint errors.
var (
	// ErrInvalidSigningKey reports a signing key that is not a 32-byte
	// Ed25519 seed.
	ErrInvalidSigningKey = errors.New("invalid audit signing key: want 32 bytes")
	// ErrCheckpointMismatch reports a checkpoint that does not match: a
	// bad signature, another key, a key that does not name its contents,
	// an epoch that differs from its directory, a hash that differs from
	// the entry at its sequence, or a bad epoch object.
	ErrCheckpointMismatch = errors.New("audit checkpoint does not match")
	// ErrInvalidVerifyKey reports a verify key that is not a 32-byte
	// Ed25519 public key.
	ErrInvalidVerifyKey = errors.New("invalid audit verify key: want a base64 32-byte Ed25519 public key")
)

// Checkpoint is the signed hash at one sequence of a scope in one epoch.
type Checkpoint struct {
	At        time.Time
	Scope     namespace.Name
	Hash      string
	KeyID     string
	Signature []byte
	Sequence  int64
	Epoch     int64
}

// checkpointJSON is the stored form of a checkpoint.
type checkpointJSON struct {
	Scope     string `json:"scope"`
	Hash      string `json:"hash"`
	At        string `json:"at"`
	KeyID     string `json:"keyID"`
	Signature []byte `json:"signature"`
	Sequence  int64  `json:"sequence"`
	Epoch     int64  `json:"epoch"`
}

// Message returns the bytes that the signature covers: a fixed context
// line, then the scope, the sequence, the epoch, the hash, the time in Unix
// milliseconds and the key ID, one per line.
func (c Checkpoint) Message() []byte {
	return []byte(strings.Join([]string{
		checkpointContext,
		c.Scope.String(),
		strconv.FormatInt(c.Sequence, 10),
		strconv.FormatInt(c.Epoch, 10),
		c.Hash,
		strconv.FormatInt(c.At.UnixMilli(), 10),
		c.KeyID,
	}, "\n"))
}

// EpochRecord is one signed epoch object. It names the epoch that starts a
// new history of the log.
type EpochRecord struct {
	At        time.Time
	KeyID     string
	Signature []byte
	Epoch     int64
}

// epochJSON is the stored form of an epoch object.
type epochJSON struct {
	At        string `json:"at"`
	KeyID     string `json:"keyID"`
	Signature []byte `json:"signature"`
	Epoch     int64  `json:"epoch"`
}

// Message returns the bytes that the signature of an epoch object covers: a
// fixed context line, then the epoch, the time in Unix milliseconds and the
// key ID, one per line.
func (e EpochRecord) Message() []byte {
	return []byte(strings.Join([]string{
		epochContext,
		strconv.FormatInt(e.Epoch, 10),
		strconv.FormatInt(e.At.UnixMilli(), 10),
		e.KeyID,
	}, "\n"))
}

// EpochKey returns the blob key of the epoch object of epoch.
func EpochKey(epoch int64) (blob.Key, error) {
	key, err := blob.ParseKey(fmt.Sprintf("%s%0*d.json", epochDir, checkpointEpochDigits, epoch))
	if err != nil {
		return "", fmt.Errorf("audit checkpoint epoch key %d: %w", epoch, err)
	}

	return key, nil
}

// MarshalEpoch encodes e in its stored JSON form.
func MarshalEpoch(e EpochRecord) ([]byte, error) {
	out, err := json.Marshal(epochJSON{
		At:        e.At.UTC().Format(time.RFC3339Nano),
		KeyID:     e.KeyID,
		Signature: e.Signature,
		Epoch:     e.Epoch,
	})
	if err != nil {
		return nil, fmt.Errorf("encode audit checkpoint epoch: %w", err)
	}

	return out, nil
}

// UnmarshalEpoch decodes the stored JSON form of an epoch object.
func UnmarshalEpoch(raw []byte) (EpochRecord, error) {
	var in epochJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return EpochRecord{}, fmt.Errorf("%w: decode epoch: %w", ErrCheckpointMismatch, err)
	}

	at, err := time.Parse(time.RFC3339Nano, in.At)
	if err != nil {
		return EpochRecord{}, fmt.Errorf("%w: epoch time: %w", ErrCheckpointMismatch, err)
	}

	return EpochRecord{
		At:        at,
		KeyID:     in.KeyID,
		Signature: in.Signature,
		Epoch:     in.Epoch,
	}, nil
}

// CheckpointKey returns the blob key of the checkpoint of scope at seq in
// epoch. The segment of the scope is the scope name in hexadecimal, because
// a scope name can hold capital letters and '@', which a blob key cannot.
func CheckpointKey(epoch int64, scope namespace.Name, seq int64) (blob.Key, error) {
	key, err := blob.ParseKey(fmt.Sprintf("%s%0*d.json", checkpointDir(epoch, scope), checkpointSequenceDigits, seq))
	if err != nil {
		return "", fmt.Errorf("checkpoint key of %s: %w", scope, err)
	}

	return key, nil
}

// checkpointDir returns the key directory of the checkpoints of scope in
// epoch.
func checkpointDir(epoch int64, scope namespace.Name) string {
	return fmt.Sprintf("%s/%0*d/%s/", CheckpointPrefix, checkpointEpochDigits, epoch, hex.EncodeToString([]byte(scope)))
}

// KeyID returns the ID of an Ed25519 public key: the first 16 hexadecimal
// characters of its SHA-256 hash. It does not contain the key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)

	return hex.EncodeToString(sum[:])[:checkpointKeyIDLength]
}

// Signer signs checkpoints with one Ed25519 key. It checks them with its
// own public key and with the verify keys, for example the public key of a
// signing key that an administrator replaced.
type Signer struct {
	verify map[string]ed25519.PublicKey
	key    ed25519.PrivateKey
	pub    ed25519.PublicKey
	id     string
}

// NewSigner returns a signer for the 32-byte Ed25519 seed. It returns
// ErrInvalidSigningKey for a seed of another length.
func NewSigner(seed []byte) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidSigningKey, len(seed))
	}

	key := ed25519.NewKeyFromSeed(seed)

	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: no Ed25519 public key", ErrInvalidSigningKey)
	}

	id := KeyID(pub)

	return &Signer{verify: map[string]ed25519.PublicKey{id: pub}, key: key, pub: pub, id: id}, nil
}

// ParseVerifyKey decodes a base64 32-byte Ed25519 public key. It returns
// ErrInvalidVerifyKey for any other value.
func ParseVerifyKey(raw string) (ed25519.PublicKey, error) {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, ErrInvalidVerifyKey
	}

	return ed25519.PublicKey(pub), nil
}

// AddVerifyKey lets the signer check checkpoints that pub signed. It never
// signs with pub. Call it before the server starts, not while a check runs.
func (s *Signer) AddVerifyKey(pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return ErrInvalidVerifyKey
	}

	s.verify[KeyID(pub)] = pub

	return nil
}

// KeyID returns the ID of the public key of the signer.
func (s *Signer) KeyID() string {
	return s.id
}

// PublicKey returns the public key of the signer.
func (s *Signer) PublicKey() ed25519.PublicKey {
	return s.pub
}

// Sign returns c with the key ID and the signature of the signer. The time
// keeps millisecond precision, as the message holds it.
func (s *Signer) Sign(c Checkpoint) Checkpoint {
	c.At = c.At.UTC().Truncate(time.Millisecond)
	c.KeyID = s.id
	c.Signature = ed25519.Sign(s.key, c.Message())

	return c
}

// Verify checks the key ID and the signature of c with the public key of
// the signer or a verify key. It returns ErrCheckpointMismatch when no key
// has the key ID or the signature does not match.
func (s *Signer) Verify(c Checkpoint) error {
	pub, ok := s.verify[c.KeyID]
	if !ok {
		return fmt.Errorf("%w: key %q is not the signing key or a verify key", ErrCheckpointMismatch, c.KeyID)
	}

	if !ed25519.Verify(pub, c.Message(), c.Signature) {
		return fmt.Errorf("%w: bad signature", ErrCheckpointMismatch)
	}

	return nil
}

// SignEpoch returns e with the key ID and the signature of the signer. The
// time keeps millisecond precision, as the message holds it.
func (s *Signer) SignEpoch(e EpochRecord) EpochRecord {
	e.At = e.At.UTC().Truncate(time.Millisecond)
	e.KeyID = s.id
	e.Signature = ed25519.Sign(s.key, e.Message())

	return e
}

// VerifyEpoch checks the key ID and the signature of e with the public key
// of the signer or a verify key. It returns ErrCheckpointMismatch when no
// key has the key ID or the signature does not match.
func (s *Signer) VerifyEpoch(e EpochRecord) error {
	pub, ok := s.verify[e.KeyID]
	if !ok {
		return fmt.Errorf("%w: key %q is not the signing key or a verify key", ErrCheckpointMismatch, e.KeyID)
	}

	if !ed25519.Verify(pub, e.Message(), e.Signature) {
		return fmt.Errorf("%w: bad epoch signature", ErrCheckpointMismatch)
	}

	return nil
}

// MarshalCheckpoint encodes c in its stored JSON form.
func MarshalCheckpoint(c Checkpoint) ([]byte, error) {
	out, err := json.Marshal(checkpointJSON{
		Scope:     c.Scope.String(),
		Hash:      c.Hash,
		At:        c.At.UTC().Format(time.RFC3339Nano),
		KeyID:     c.KeyID,
		Signature: c.Signature,
		Sequence:  c.Sequence,
		Epoch:     c.Epoch,
	})
	if err != nil {
		return nil, fmt.Errorf("encode checkpoint: %w", err)
	}

	return out, nil
}

// UnmarshalCheckpoint decodes the stored JSON form of a checkpoint.
func UnmarshalCheckpoint(raw []byte) (Checkpoint, error) {
	var in checkpointJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return Checkpoint{}, fmt.Errorf("%w: decode: %w", ErrCheckpointMismatch, err)
	}

	at, err := time.Parse(time.RFC3339Nano, in.At)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("%w: time: %w", ErrCheckpointMismatch, err)
	}

	return Checkpoint{
		At:        at,
		Scope:     namespace.Name(in.Scope),
		Hash:      in.Hash,
		KeyID:     in.KeyID,
		Signature: in.Signature,
		Sequence:  in.Sequence,
		Epoch:     in.Epoch,
	}, nil
}

// CheckpointsConfig configures Checkpoints.
type CheckpointsConfig struct {
	Blobs   blob.Store
	Entries EntryReader
	Signer  *Signer
	// Marks keeps the mark of a checkpoint mismatch, or is nil for a mark
	// in the memory of the process only.
	Marks CheckStore
	Now   func() time.Time
}

// Checkpoints writes and checks the checkpoints of the audit log. It keeps
// the sequence of the last checkpoint that matched in each scope, so a
// check reads the newer checkpoints and a sample of the older ones. It also
// keeps the sequence of a checkpoint that did not match in each scope, so
// later checks of the same epoch fail until a new epoch starts. It is safe
// for concurrent use.
type Checkpoints struct {
	good map[goodKey]int64
	bad  map[goodKey]int64
	// unsaved holds the marks that the store did not take yet. Each Write and
	// Verify stores them again before it does anything else.
	unsaved map[goodKey]CheckMark
	blobs   blob.Store
	entries EntryReader
	signer  *Signer
	marks   CheckStore
	now     func() time.Time
	mu      sync.Mutex
}

// goodKey names the scope of a last good checkpoint in one epoch.
type goodKey struct {
	scope namespace.Name
	epoch int64
}

// NewCheckpoints returns checkpoints for cfg.
func NewCheckpoints(cfg CheckpointsConfig) *Checkpoints {
	return &Checkpoints{
		good:    map[goodKey]int64{},
		bad:     map[goodKey]int64{},
		unsaved: map[goodKey]CheckMark{},
		blobs:   cfg.Blobs,
		entries: cfg.Entries,
		signer:  cfg.Signer,
		marks:   cfg.Marks,
		now:     cfg.Now,
		mu:      sync.Mutex{},
	}
}

// Signer answers the signer of the checkpoints.
func (c *Checkpoints) Signer() *Signer {
	return c.signer
}

// Now answers the time of the checkpoints.
func (c *Checkpoints) Now() time.Time {
	if c.now != nil {
		return c.now()
	}

	return time.Now()
}

// CheckpointResult reports the checkpoint check of a scope. Valid is true
// when every checkpoint that the check read matches. When Valid is false,
// BadSequence holds the sequence of the first checkpoint that does not
// match, or 0 when its key names no sequence, and Reason says why the
// check failed. Checked counts the checkpoints the check read.
type CheckpointResult struct {
	Reason      string
	Checked     int64
	BadSequence int64
	Valid       bool
}

// fail returns r as a failed check at seq for reason.
func (r CheckpointResult) fail(seq int64, reason string) CheckpointResult {
	r.Valid = false
	r.BadSequence = seq
	r.Reason = reason

	return r
}

// Epoch answers the current epoch: the highest signed epoch object, or 0
// when no epoch object exists. It checks every epoch object: the signature,
// a known key ID, and the epoch number in the body against the number in
// its key. A bad object returns an error that wraps ErrCheckpointMismatch
// and names its key. The objects are small, so the read caps each at 4 KiB
// like a checkpoint.
func (c *Checkpoints) Epoch(ctx context.Context) (int64, error) {
	objects, err := c.blobs.List(ctx, epochDir)
	if err != nil {
		return 0, fmt.Errorf("list audit checkpoint epochs: %w", err)
	}

	var epoch int64

	for _, obj := range objects {
		name := strings.TrimSuffix(strings.TrimPrefix(obj.Key.String(), epochDir), ".json")

		n, err := strconv.ParseInt(name, 10, 64)
		if err != nil {
			continue
		}

		rec, err := c.readEpoch(ctx, obj.Key)
		if err != nil {
			return 0, err
		}

		if rec.Epoch != n {
			return 0, fmt.Errorf("audit checkpoint epoch %s holds epoch %d: %w",
				obj.Key, rec.Epoch, ErrCheckpointMismatch)
		}

		if err := c.signer.VerifyEpoch(rec); err != nil {
			return 0, fmt.Errorf("audit checkpoint epoch %s: %w", obj.Key, err)
		}

		if n > epoch {
			epoch = n
		}
	}

	return epoch, nil
}

// NewEpoch starts the epoch after the current one and answers it. It signs
// the new epoch object with the same key as the checkpoints. When another
// instance starts the same epoch at the same time, both answer it. A bad
// epoch object returns an error that wraps ErrCheckpointMismatch and signs
// nothing.
func (c *Checkpoints) NewEpoch(ctx context.Context, now time.Time) (int64, error) {
	current, err := c.Epoch(ctx)
	if err != nil {
		return 0, err
	}

	next := current + 1

	key, err := EpochKey(next)
	if err != nil {
		return 0, err
	}

	rec := c.signer.SignEpoch(EpochRecord{
		At:        now,
		KeyID:     "",
		Signature: nil,
		Epoch:     next,
	})

	raw, err := MarshalEpoch(rec)
	if err != nil {
		return 0, err
	}

	err = c.blobs.Put(ctx, key, bytes.NewReader(raw))
	if err != nil && !errors.Is(err, blob.ErrExists) {
		return 0, fmt.Errorf("store audit checkpoint epoch %d: %w", next, err)
	}

	return next, nil
}

// Write signs the hash of the last entry of scope and stores the
// checkpoint in the current epoch. It sets the epoch of the checkpoint to
// the current epoch. It answers false and no error when scope holds no
// entry, when the last entry holds no hash, or when a checkpoint of that
// sequence already holds the same hash. When the stored checkpoint holds
// another hash, it answers false and an error that wraps
// ErrCheckpointMismatch, and later checks of the scope fail until a new
// epoch starts. When the scope failed to match before in this epoch, or
// another instance stored a mark of scope in this epoch, it signs nothing
// and answers false and an error that wraps ErrCheckpointMismatch. The same holds when
// the stored mark cannot be read: a failed read denies. It lists the stored
// checkpoints first and reads the last entry second. The log only grows, so
// a stored checkpoint above the last entry then means a cut log, never a
// checkpoint that another instance stored between the two reads. When the log
// ends before the newest stored checkpoint, it signs nothing, marks the
// scope, and answers false and an error that wraps ErrCheckpointMismatch. A
// bad epoch object also signs nothing and returns an error that wraps
// ErrCheckpointMismatch. The caller counts such an error as a scope that does
// not match. When the epoch changes before the store, it stores nothing and
// answers false and no error. The stored mark is read before the signature
// and not in the same step: a mark that another instance stores in that small
// window stops the next Write of the scope, not this one.
func (c *Checkpoints) Write(ctx context.Context, scope namespace.Name, now time.Time) (bool, error) {
	if err := c.flushMarks(ctx); err != nil {
		return false, fmt.Errorf("%w: %w", ErrCheckpointMismatch, err)
	}

	epoch, err := c.Epoch(ctx)
	if err != nil {
		return false, err
	}

	gk := goodKey{scope: scope, epoch: epoch}

	if seq, ok := c.sticky(gk); ok {
		return false, fmt.Errorf("audit checkpoint of %s at %d does not match: %w", scope, seq, ErrCheckpointMismatch)
	}

	mark, err := c.standingMark(ctx, epoch, scope)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrCheckpointMismatch, err)
	}

	if mark != nil {
		return false, fmt.Errorf("audit checkpoint of %s at %d does not match, signing stops: %w",
			mark.Scope, mark.Sequence, ErrCheckpointMismatch)
	}

	highest, err := c.highestStored(ctx, epoch, scope)
	if err != nil {
		return false, err
	}

	last, found, err := c.entries.LastEntry(ctx, scope)
	if err != nil {
		return false, fmt.Errorf("read last audit entry of %s: %w", scope, err)
	}

	if !found || last.Hash == "" {
		return false, nil
	}

	if last.Sequence < highest {
		c.markBad(ctx, gk, highest)

		return false, fmt.Errorf("audit checkpoint of %s: log ends at %d before checkpoint %d: %w",
			scope, last.Sequence, highest, ErrCheckpointMismatch)
	}

	return c.storeNew(ctx, gk, last, now)
}

// Verify checks the checkpoints of scope in the current epoch: the newest
// one first in every check, every one newer than the last one that matched,
// up to MaxNewCheckpoints, and up to CheckpointSample older ones at random.
// For each it checks the signature, the key, the scope, the sequence and
// the epoch that its key names, and the hash of the entry at its sequence.
// A bad epoch object fails the scope with Valid false and BadSequence 0.
// It also fails when the scope holds no entry or ends before the newest
// checkpoint, so a cut at the end of the log fails the next check. It fails
// when stored checkpoints are missing below the last one that matched. A
// scope that failed to match before fails again until a new epoch starts.
// It answers an error only when it cannot read a checkpoint or an entry.
func (c *Checkpoints) Verify(ctx context.Context, scope namespace.Name) (CheckpointResult, error) {
	result := CheckpointResult{Reason: "", Checked: 0, BadSequence: 0, Valid: true}

	gate, err := c.verifyGate(ctx, scope, result)
	if gate.stop || err != nil {
		return gate.result, err
	}

	epoch, result := gate.epoch, gate.result
	gk := goodKey{scope: scope, epoch: epoch}

	dir := checkpointDir(epoch, scope)

	objects, err := c.blobs.List(ctx, dir)
	if err != nil {
		return result, fmt.Errorf("list audit checkpoints of %s: %w", scope, err)
	}

	good := c.lastGood(gk)
	highest, highestKey := highestCheckpoint(objects)

	if highest < good {
		c.markBad(ctx, gk, good)

		return result.fail(good, fmt.Sprintf("checkpoints are missing: newest stored checkpoint is %d, want %d",
			highest, good)), nil
	}

	read := withNewest(c.plan(objects, good), highest, highestKey, good)

	var best int64

	for _, obj := range read.read {
		seq, err := c.verifyOne(ctx, dir, scope, epoch, obj.Key)
		result.Checked++

		if errors.Is(err, ErrCheckpointMismatch) {
			c.markBad(ctx, gk, seq)

			return result.fail(seq, err.Error()), nil
		}

		if err != nil {
			return result, err
		}

		if obj.newer {
			best = max(best, seq)
		}
	}

	if seq, bad, err := c.checkTail(ctx, scope, gk, highest); err != nil {
		return result, err
	} else if bad {
		return result.fail(seq, "the log ends before the newest checkpoint"), nil
	}

	c.markGood(gk, best)

	return result, nil
}

// verifyGateResult is the outcome of verifyGate: the epoch, the result so far,
// and whether Verify stops with that result.
type verifyGateResult struct {
	result CheckpointResult
	epoch  int64
	stop   bool
}

// verifyGate runs the steps of Verify that need no list of checkpoints: it
// stores marks that a failed store did not take, reads the epoch, and fails
// the scope when a mark stands.
func (c *Checkpoints) verifyGate(
	ctx context.Context, scope namespace.Name, result CheckpointResult,
) (verifyGateResult, error) {
	if err := c.flushMarks(ctx); err != nil {
		return verifyGateResult{result: result.fail(0, err.Error()), epoch: 0, stop: true}, nil
	}

	epoch, err := c.Epoch(ctx)
	if errors.Is(err, ErrCheckpointMismatch) {
		return verifyGateResult{result: result.fail(0, err.Error()), epoch: 0, stop: true}, nil
	}

	if err != nil {
		return verifyGateResult{result: result, epoch: 0, stop: true}, err
	}

	stop := func(r CheckpointResult) verifyGateResult {
		return verifyGateResult{result: r, epoch: epoch, stop: true}
	}

	if seq, ok := c.sticky(goodKey{scope: scope, epoch: epoch}); ok {
		return stop(result.fail(seq, "an earlier check in this epoch found a mismatch")), nil
	}

	mark, err := c.standingMark(ctx, epoch, scope)
	if err != nil {
		return stop(result.fail(0, err.Error())), nil
	}

	if mark != nil {
		return stop(result.fail(mark.Sequence, "a check on another instance in this epoch found a mismatch")), nil
	}

	return verifyGateResult{result: result, epoch: epoch, stop: false}, nil
}

// keep answers a write that found a checkpoint under key: false and no
// error when the stored checkpoint holds hash, and false and an error that
// wraps ErrCheckpointMismatch when it holds another hash. A stored
// checkpoint that does not match marks the scope, so later checks fail
// until a new epoch starts.
func (c *Checkpoints) keep(ctx context.Context, gk goodKey, key blob.Key, hash string) (bool, error) {
	stored, err := c.read(ctx, key)
	if errors.Is(err, ErrCheckpointMismatch) {
		c.markBad(ctx, gk, checkpointSeq(key))

		return false, err
	}

	if err != nil {
		return false, err
	}

	if stored.Hash != hash {
		c.markBad(ctx, gk, stored.Sequence)

		return false, fmt.Errorf("audit checkpoint %s does not match: %w", key, ErrCheckpointMismatch)
	}

	return false, nil
}

// planned is one checkpoint to read, and whether it is newer than the last
// good one.
type planned struct {
	Key   blob.Key
	newer bool
}

// readPlan is the checkpoints that one check reads.
type readPlan struct {
	read []planned
}

// plan chooses the checkpoints to read: every one newer than good, up to
// MaxNewCheckpoints of the newest, and a random sample of the others.
func (c *Checkpoints) plan(objects []blob.Object, good int64) readPlan {
	var newer, older []blob.Key

	for _, obj := range objects {
		if checkpointSeq(obj.Key) > good {
			newer = append(newer, obj.Key)
		} else {
			older = append(older, obj.Key)
		}
	}

	if len(newer) > MaxNewCheckpoints {
		older = append(older, newer[:len(newer)-MaxNewCheckpoints]...)
		newer = newer[len(newer)-MaxNewCheckpoints:]
	}

	out := readPlan{read: make([]planned, 0, len(newer)+CheckpointSample)}

	for _, key := range newer {
		out.read = append(out.read, planned{Key: key, newer: true})
	}

	for _, key := range sample(older, CheckpointSample) {
		out.read = append(out.read, planned{Key: key, newer: false})
	}

	return out
}

// highestCheckpoint answers the highest sequence of objects and its key,
// or 0 and an empty key when no object names a sequence.
func highestCheckpoint(objects []blob.Object) (int64, blob.Key) {
	var (
		best int64
		key  blob.Key
	)

	for _, obj := range objects {
		if seq := checkpointSeq(obj.Key); seq > best {
			best, key = seq, obj.Key
		}
	}

	return best, key
}

// withNewest puts the newest checkpoint first in the plan and keeps it
// once, so a cut at the end fails at the newest sequence.
func withNewest(plan readPlan, highest int64, key blob.Key, good int64) readPlan {
	if highest <= 0 {
		return plan
	}

	out := readPlan{read: make([]planned, 0, len(plan.read)+1)}
	out.read = append(out.read, planned{Key: key, newer: highest > good})

	for _, obj := range plan.read {
		if obj.Key == key {
			continue
		}

		out.read = append(out.read, obj)
	}

	return out
}

// highestStored answers the highest sequence of the checkpoints that are
// stored for scope in epoch, or 0 when none is.
func (c *Checkpoints) highestStored(ctx context.Context, epoch int64, scope namespace.Name) (int64, error) {
	stored, err := c.blobs.List(ctx, checkpointDir(epoch, scope))
	if err != nil {
		return 0, fmt.Errorf("list audit checkpoints of %s: %w", scope, err)
	}

	highest, _ := highestCheckpoint(stored)

	return highest, nil
}

// storeNew signs the hash of last and stores it. When the epoch changes
// before the store, it stores nothing and answers false and no error.
func (c *Checkpoints) storeNew(ctx context.Context, gk goodKey, last Entry, now time.Time) (bool, error) {
	cp := c.signer.Sign(Checkpoint{
		At:        now,
		Scope:     gk.scope,
		Hash:      last.Hash,
		KeyID:     "",
		Signature: nil,
		Sequence:  last.Sequence,
		Epoch:     gk.epoch,
	})

	key, err := CheckpointKey(gk.epoch, gk.scope, cp.Sequence)
	if err != nil {
		return false, err
	}

	raw, err := MarshalCheckpoint(cp)
	if err != nil {
		return false, err
	}

	again, err := c.Epoch(ctx)
	if err != nil {
		return false, err
	}

	if again != gk.epoch {
		return false, nil
	}

	err = c.blobs.Put(ctx, key, bytes.NewReader(raw))
	if errors.Is(err, blob.ErrExists) {
		return c.keep(ctx, gk, key, last.Hash)
	}

	if err != nil {
		return false, fmt.Errorf("store audit checkpoint %s: %w", key, err)
	}

	return true, nil
}

// checkTail fails when the scope holds no entry or ends before the newest
// checkpoint, so a cut at the end of the log fails the check. It marks the
// scope, so later checks fail until a new epoch starts.
func (c *Checkpoints) checkTail(
	ctx context.Context,
	scope namespace.Name,
	gk goodKey,
	highest int64,
) (int64, bool, error) {
	if highest <= 0 {
		return 0, false, nil
	}

	last, ok, err := c.entries.LastEntry(ctx, scope)
	if err != nil {
		return 0, false, fmt.Errorf("read last audit entry of %s: %w", scope, err)
	}

	if !ok || last.Sequence < highest {
		c.markBad(ctx, gk, highest)

		return highest, true, nil
	}

	return 0, false, nil
}

// lastGood answers the sequence of the last checkpoint that matched in gk.
func (c *Checkpoints) lastGood(gk goodKey) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.good[gk]
}

// markGood records seq as the last checkpoint that matched in gk, when it
// is higher.
func (c *Checkpoints) markGood(gk goodKey, seq int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if seq > c.good[gk] {
		c.good[gk] = seq
	}
}

// sticky answers the sequence of the checkpoint that did not match in gk,
// when one did before in this epoch.
func (c *Checkpoints) sticky(gk goodKey) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	seq, ok := c.bad[gk]

	return seq, ok
}

// markBad records seq as a checkpoint of gk that did not match, so later
// checks of the same epoch fail until a new epoch starts. It keeps the first
// mismatch of the scope in memory, and stores the mark when the checkpoints
// have a CheckStore, so every instance stops signing that scope while it
// stands. A store that fails keeps the mark in memory and flushMarks stores
// it again: this process signs nothing for the scope meanwhile, and the next
// Write or Verify stores the mark before it does anything else.
func (c *Checkpoints) markBad(ctx context.Context, gk goodKey, seq int64) {
	c.mu.Lock()

	if _, ok := c.bad[gk]; !ok {
		c.bad[gk] = seq
	}

	c.mu.Unlock()

	if c.marks == nil {
		return
	}

	mark := CheckMark{At: time.Time{}, Scope: gk.scope, Sequence: seq, Epoch: gk.epoch}

	if err := c.marks.SaveCheckMark(ctx, mark); err != nil {
		c.mu.Lock()
		c.unsaved[gk] = mark
		c.mu.Unlock()
	}
}

// flushMarks stores the marks that a failed store did not take. It answers
// an error while one stays unsaved, so a caller signs nothing and treats the
// check as failed.
func (c *Checkpoints) flushMarks(ctx context.Context) error {
	if c.marks == nil {
		return nil
	}

	c.mu.Lock()
	pending := make(map[goodKey]CheckMark, len(c.unsaved))
	maps.Copy(pending, c.unsaved)
	c.mu.Unlock()

	for gk, mark := range pending {
		if err := c.marks.SaveCheckMark(ctx, mark); err != nil {
			return fmt.Errorf("store audit checkpoint mark of %s: %w", mark.Scope, err)
		}

		c.mu.Lock()
		delete(c.unsaved, gk)
		c.mu.Unlock()
	}

	return nil
}

// standingMark answers the stored mark of scope that stops signing in epoch:
// a mark of this epoch or of a newer one. A mark of another scope stops
// nothing here, and a mark of an older epoch stops nothing, so a new epoch
// ends it. It answers nil without a CheckStore. A store that cannot be read
// returns an error, and the caller denies: signing stops whenever the mark is
// unknown.
func (c *Checkpoints) standingMark(ctx context.Context, epoch int64, scope namespace.Name) (*CheckMark, error) {
	if c.marks == nil {
		return nil, nil //nolint:nilnil // Nil without an error means no mark stands.
	}

	state, err := c.marks.CheckState(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the audit checkpoint mark: %w", err)
	}

	for _, m := range state.Marks {
		if m.Scope == scope && m.Epoch >= epoch {
			return &m, nil
		}
	}

	return nil, nil //nolint:nilnil // Nil without an error means no mark stands.
}

// verifyOne checks the checkpoint under key in dir of epoch and answers
// the sequence that its key names. It fails when the epoch in the
// checkpoint differs from the epoch of its key directory.
func (c *Checkpoints) verifyOne(
	ctx context.Context,
	dir string,
	scope namespace.Name,
	epoch int64,
	key blob.Key,
) (int64, error) {
	seq := checkpointSeq(key)
	if seq <= 0 || !strings.HasPrefix(key.String(), dir) {
		return 0, fmt.Errorf("%w: key %s names no sequence", ErrCheckpointMismatch, key)
	}

	cp, err := c.read(ctx, key)
	if err != nil {
		return seq, err
	}

	if cp.Scope != scope || cp.Sequence != seq {
		return seq, fmt.Errorf("%w: key %s holds %s at %d", ErrCheckpointMismatch, key, cp.Scope, cp.Sequence)
	}

	if cp.Epoch != epoch {
		return seq, fmt.Errorf("%w: key %s holds epoch %d, want %d", ErrCheckpointMismatch, key, cp.Epoch, epoch)
	}

	err = c.signer.Verify(cp)
	if err != nil {
		return seq, err
	}

	entry, err := c.entries.EntryAt(ctx, scope, seq)
	if errors.Is(err, ErrEntryNotFound) {
		return seq, fmt.Errorf("%w: %s holds no entry %d", ErrCheckpointMismatch, scope, seq)
	}

	if err != nil {
		return seq, fmt.Errorf("read audit entry %s %d: %w", scope, seq, err)
	}

	if entry.Hash != cp.Hash {
		return seq, fmt.Errorf("%w: hash at %s %d differs", ErrCheckpointMismatch, scope, seq)
	}

	return seq, nil
}

// read reads and decodes the checkpoint under key.
func (c *Checkpoints) read(ctx context.Context, key blob.Key) (Checkpoint, error) {
	raw, err := c.readRaw(ctx, key, "audit checkpoint")
	if err != nil {
		return Checkpoint{}, err
	}

	cp, err := UnmarshalCheckpoint(raw)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("audit checkpoint %s: %w", key, err)
	}

	return cp, nil
}

// readEpoch reads and decodes the epoch object under key. A bad object
// returns an error that wraps ErrCheckpointMismatch and names the key.
func (c *Checkpoints) readEpoch(ctx context.Context, key blob.Key) (EpochRecord, error) {
	raw, err := c.readRaw(ctx, key, "audit checkpoint epoch")
	if err != nil {
		return EpochRecord{}, err
	}

	rec, err := UnmarshalEpoch(raw)
	if err != nil {
		return EpochRecord{}, fmt.Errorf("audit checkpoint epoch %s: %w", key, err)
	}

	return rec, nil
}

// readRaw reads the object under key, capped at checkpointMaxBytes. It
// names the object what in read errors.
func (c *Checkpoints) readRaw(ctx context.Context, key blob.Key, what string) ([]byte, error) {
	body, err := c.blobs.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", what, key, err)
	}

	raw, readErr := io.ReadAll(io.LimitReader(body, checkpointMaxBytes+1))
	closeErr := body.Close()

	if readErr != nil {
		return nil, fmt.Errorf("read %s %s: %w", what, key, errors.Join(readErr, closeErr))
	}

	if closeErr != nil {
		return nil, fmt.Errorf("read %s %s: %w", what, key, closeErr)
	}

	if len(raw) > checkpointMaxBytes {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes",
			ErrCheckpointMismatch, key, checkpointMaxBytes)
	}

	return raw, nil
}

// checkpointSeq answers the sequence that the last segment of key names,
// or 0 when it names none.
func checkpointSeq(key blob.Key) int64 {
	name := key.String()
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}

	seq, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
	if err != nil {
		return 0
	}

	return seq
}

// sample answers up to n keys of keys, chosen at random.
func sample(keys []blob.Key, n int) []blob.Key {
	if len(keys) <= n {
		return keys
	}

	pool := append([]blob.Key(nil), keys...)

	for i := range n {
		j := i + randomIndex(len(pool)-i)
		pool[i], pool[j] = pool[j], pool[i]
	}

	return pool[:n]
}

// randomIndex answers a random number from 0 to n-1, from crypto/rand, so
// a person who changes old checkpoints cannot predict the sample.
func randomIndex(n int) int {
	if n <= 1 {
		return 0
	}

	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}

	return int(v.Int64())
}
