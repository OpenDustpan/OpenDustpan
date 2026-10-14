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
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Signed checkpoints are opt-in (ADR 60 decision 18). A checkpoint signs the
// hash at one sequence of a scope with an Ed25519 key. It goes to the blob
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
// checkpoints of the current epoch. An epoch is an object under
// audit-checkpoints/epochs/, so the current epoch needs no database.

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
	// or a hash that differs from the entry at its sequence.
	ErrCheckpointMismatch = errors.New("audit checkpoint does not match")
	// ErrInvalidVerifyKey reports a verify key that is not a 32-byte
	// Ed25519 public key.
	ErrInvalidVerifyKey = errors.New("invalid audit verify key: want a base64 32-byte Ed25519 public key")
)

// Checkpoint is the signed hash at one sequence of a scope.
type Checkpoint struct {
	At        time.Time
	Scope     namespace.Name
	Hash      string
	KeyID     string
	Signature []byte
	Sequence  int64
}

// checkpointJSON is the stored form of a checkpoint.
type checkpointJSON struct {
	Scope     string `json:"scope"`
	Hash      string `json:"hash"`
	At        string `json:"at"`
	KeyID     string `json:"keyID"`
	Signature []byte `json:"signature"`
	Sequence  int64  `json:"sequence"`
}

// Message returns the bytes that the signature covers: a fixed context
// line, then the scope, the sequence, the hash, the time in Unix
// milliseconds and the key ID, one per line.
func (c Checkpoint) Message() []byte {
	return []byte(strings.Join([]string{
		checkpointContext,
		c.Scope.String(),
		strconv.FormatInt(c.Sequence, 10),
		c.Hash,
		strconv.FormatInt(c.At.UnixMilli(), 10),
		c.KeyID,
	}, "\n"))
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

// MarshalCheckpoint encodes c in its stored JSON form.
func MarshalCheckpoint(c Checkpoint) ([]byte, error) {
	out, err := json.Marshal(checkpointJSON{
		Scope:     c.Scope.String(),
		Hash:      c.Hash,
		At:        c.At.UTC().Format(time.RFC3339Nano),
		KeyID:     c.KeyID,
		Signature: c.Signature,
		Sequence:  c.Sequence,
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
	}, nil
}

// CheckpointsConfig configures Checkpoints.
type CheckpointsConfig struct {
	Blobs   blob.Store
	Entries EntryReader
	Signer  *Signer
}

// Checkpoints writes and checks the checkpoints of the audit log. It keeps
// the sequence of the last checkpoint that matched in each scope, so a
// check reads the newer checkpoints and a sample of the older ones. It is
// safe for concurrent use.
type Checkpoints struct {
	good    map[goodKey]int64
	blobs   blob.Store
	entries EntryReader
	signer  *Signer
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
		blobs:   cfg.Blobs,
		entries: cfg.Entries,
		signer:  cfg.Signer,
		mu:      sync.Mutex{},
	}
}

// Signer answers the signer of the checkpoints.
func (c *Checkpoints) Signer() *Signer {
	return c.signer
}

// CheckpointResult reports the checkpoint check of a scope. Valid is true
// when every checkpoint that the check read matches. When Valid is false,
// BadSequence holds the sequence of the first checkpoint that does not
// match, or 0 when its key names no sequence. Checked counts the
// checkpoints the check read.
type CheckpointResult struct {
	Checked     int64
	BadSequence int64
	Valid       bool
}

// Epoch answers the current epoch: the highest epoch object, or 0 when no
// epoch object exists.
func (c *Checkpoints) Epoch(ctx context.Context) (int64, error) {
	objects, err := c.blobs.List(ctx, epochDir)
	if err != nil {
		return 0, fmt.Errorf("list audit checkpoint epochs: %w", err)
	}

	var epoch int64

	for _, obj := range objects {
		name := strings.TrimSuffix(strings.TrimPrefix(obj.Key.String(), epochDir), ".json")

		if n, err := strconv.ParseInt(name, 10, 64); err == nil && n > epoch {
			epoch = n
		}
	}

	return epoch, nil
}

// NewEpoch starts the epoch after the current one and answers it. When
// another instance starts the same epoch at the same time, both answer it.
func (c *Checkpoints) NewEpoch(ctx context.Context, now time.Time) (int64, error) {
	current, err := c.Epoch(ctx)
	if err != nil {
		return 0, err
	}

	next := current + 1

	key, err := blob.ParseKey(fmt.Sprintf("%s%0*d.json", epochDir, checkpointEpochDigits, next))
	if err != nil {
		return 0, fmt.Errorf("audit checkpoint epoch key: %w", err)
	}

	body := `{"at":"` + now.UTC().Format(time.RFC3339Nano) + `"}`

	err = c.blobs.Put(ctx, key, strings.NewReader(body))
	if err != nil && !errors.Is(err, blob.ErrExists) {
		return 0, fmt.Errorf("store audit checkpoint epoch %d: %w", next, err)
	}

	return next, nil
}

// Write signs the hash of the last entry of scope and stores the
// checkpoint in the current epoch. It answers false and no error when scope
// holds no entry, when the last entry holds no hash, or when a checkpoint
// of that sequence already exists.
func (c *Checkpoints) Write(ctx context.Context, scope namespace.Name, now time.Time) (bool, error) {
	last, err := c.entries.LastEntry(ctx, scope)
	if errors.Is(err, ErrEntryNotFound) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("read last audit entry of %s: %w", scope, err)
	}

	if last.Hash == "" {
		return false, nil
	}

	epoch, err := c.Epoch(ctx)
	if err != nil {
		return false, err
	}

	cp := c.signer.Sign(Checkpoint{
		At:        now,
		Scope:     scope,
		Hash:      last.Hash,
		KeyID:     "",
		Signature: nil,
		Sequence:  last.Sequence,
	})

	key, err := CheckpointKey(epoch, scope, cp.Sequence)
	if err != nil {
		return false, err
	}

	raw, err := MarshalCheckpoint(cp)
	if err != nil {
		return false, err
	}

	err = c.blobs.Put(ctx, key, bytes.NewReader(raw))
	if errors.Is(err, blob.ErrExists) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("store audit checkpoint %s: %w", key, err)
	}

	return true, nil
}

// Verify checks the checkpoints of scope in the current epoch: every one
// newer than the last one that matched, up to MaxNewCheckpoints, and up to
// CheckpointSample older ones at random. For each it checks the
// signature, the key, the scope and sequence that its key names, and the
// hash of the entry at its sequence. It answers an error only when it
// cannot read a checkpoint or an entry.
func (c *Checkpoints) Verify(ctx context.Context, scope namespace.Name) (CheckpointResult, error) {
	result := CheckpointResult{Checked: 0, BadSequence: 0, Valid: true}

	epoch, err := c.Epoch(ctx)
	if err != nil {
		return result, err
	}

	dir := checkpointDir(epoch, scope)

	objects, err := c.blobs.List(ctx, dir)
	if err != nil {
		return result, fmt.Errorf("list audit checkpoints of %s: %w", scope, err)
	}

	gk := goodKey{scope: scope, epoch: epoch}
	plan := c.plan(objects, c.lastGood(gk))

	var best int64

	for _, obj := range plan.read {
		seq, err := c.verifyOne(ctx, dir, scope, obj.Key)
		result.Checked++

		if errors.Is(err, ErrCheckpointMismatch) {
			result.Valid = false
			result.BadSequence = seq

			return result, nil
		}

		if err != nil {
			return result, err
		}

		if obj.newer {
			best = max(best, seq)
		}
	}

	c.markGood(gk, best)

	return result, nil
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

// verifyOne checks the checkpoint under key in dir and answers the sequence
// that its key names.
func (c *Checkpoints) verifyOne(ctx context.Context, dir string, scope namespace.Name, key blob.Key) (int64, error) {
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
	body, err := c.blobs.Get(ctx, key)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read audit checkpoint %s: %w", key, err)
	}

	raw, readErr := io.ReadAll(io.LimitReader(body, checkpointMaxBytes+1))
	closeErr := body.Close()

	if err := errors.Join(readErr, closeErr); err != nil {
		return Checkpoint{}, fmt.Errorf("read audit checkpoint %s: %w", key, err)
	}

	if len(raw) > checkpointMaxBytes {
		return Checkpoint{}, fmt.Errorf("%w: %s is larger than %d bytes",
			ErrCheckpointMismatch, key, checkpointMaxBytes)
	}

	return UnmarshalCheckpoint(raw)
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
