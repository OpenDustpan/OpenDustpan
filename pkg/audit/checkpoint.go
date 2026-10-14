package audit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Signed checkpoints are opt-in (ADR 60 decision 18). A checkpoint signs the
// hash at one sequence of a scope with an Ed25519 key. It goes to the blob
// store under a key that names the scope and the sequence, so two instances
// that write the checkpoint of one sequence write it once. A copy of the
// database that an attacker changes and chains again does not match the
// checkpoints that the server signed before: the attacker does not hold the
// signing key.

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
	// checkpointContext starts the signed message, so a signature of a
	// checkpoint is never valid for other data.
	checkpointContext = "opendustpan audit checkpoint v1"
	// hashAtReads caps the reads of the entry at one sequence while appends
	// move it to later pages.
	hashAtReads = 3
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

// CheckpointKey returns the blob key of the checkpoint of scope at seq. The
// segment of the scope is the scope name in hexadecimal, because a scope
// name can hold capital letters and '@', which a blob key cannot.
func CheckpointKey(scope namespace.Name, seq int64) (blob.Key, error) {
	key, err := blob.ParseKey(fmt.Sprintf("%s/%s/%0*d.json",
		CheckpointPrefix, hex.EncodeToString([]byte(scope)), checkpointSequenceDigits, seq))
	if err != nil {
		return "", fmt.Errorf("checkpoint key of %s: %w", scope, err)
	}

	return key, nil
}

// checkpointDir returns the key directory of the checkpoints of scope.
func checkpointDir(scope namespace.Name) string {
	return CheckpointPrefix + "/" + hex.EncodeToString([]byte(scope)) + "/"
}

// KeyID returns the ID of an Ed25519 public key: the first 16 hexadecimal
// characters of its SHA-256 hash. It does not contain the key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)

	return hex.EncodeToString(sum[:])[:checkpointKeyIDLength]
}

// Signer signs checkpoints with one Ed25519 key and checks them with its
// public key.
type Signer struct {
	key ed25519.PrivateKey
	pub ed25519.PublicKey
	id  string
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

	return &Signer{key: key, pub: pub, id: KeyID(pub)}, nil
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

// Verify checks the key ID and the signature of c. It returns
// ErrCheckpointMismatch when either does not match.
func (s *Signer) Verify(c Checkpoint) error {
	if c.KeyID != s.id {
		return fmt.Errorf("%w: key %q, want %q", ErrCheckpointMismatch, c.KeyID, s.id)
	}

	if !ed25519.Verify(s.pub, c.Message(), c.Signature) {
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

// Checkpoints writes and checks the checkpoints of the audit log.
type Checkpoints struct {
	Blobs   blob.Store
	Entries Store
	Signer  *Signer
}

// CheckpointResult reports the checkpoint check of a scope. Valid is true
// when every checkpoint matches. When Valid is false, BadSequence holds
// the sequence of the first checkpoint that does not match, or 0 when its
// key names no sequence. Checked counts the checkpoints the check read.
type CheckpointResult struct {
	Checked     int64
	BadSequence int64
	Valid       bool
}

// Write signs the hash of the last entry of scope and stores the
// checkpoint. It answers false and no error when scope holds no entry, when
// the last entry holds no hash, or when a checkpoint of that sequence
// already exists.
func (c Checkpoints) Write(ctx context.Context, scope namespace.Name, now time.Time) (bool, error) {
	head, err := c.Entries.List(ctx, scope, Page{Number: 1, Size: 1})
	if err != nil {
		return false, fmt.Errorf("read last audit entry of %s: %w", scope, err)
	}

	if len(head.Entries) == 0 || head.Entries[0].Hash == "" {
		return false, nil
	}

	last := head.Entries[0]

	cp := c.Signer.Sign(Checkpoint{
		At:        now,
		Scope:     scope,
		Hash:      last.Hash,
		KeyID:     "",
		Signature: nil,
		Sequence:  last.Sequence,
	})

	key, err := CheckpointKey(scope, cp.Sequence)
	if err != nil {
		return false, err
	}

	raw, err := MarshalCheckpoint(cp)
	if err != nil {
		return false, err
	}

	err = c.Blobs.Put(ctx, key, bytes.NewReader(raw))
	if errors.Is(err, blob.ErrExists) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("store audit checkpoint %s: %w", key, err)
	}

	return true, nil
}

// Verify checks every checkpoint of scope: the signature, the key, the
// scope and sequence that its key names, and the hash of the entry at its
// sequence. It answers an error only when it cannot read a checkpoint or
// an entry.
func (c Checkpoints) Verify(ctx context.Context, scope namespace.Name) (CheckpointResult, error) {
	result := CheckpointResult{Checked: 0, BadSequence: 0, Valid: true}

	objects, err := c.Blobs.List(ctx, checkpointDir(scope))
	if err != nil {
		return result, fmt.Errorf("list audit checkpoints of %s: %w", scope, err)
	}

	for _, obj := range objects {
		seq, err := c.verifyOne(ctx, scope, obj.Key)
		result.Checked++

		if errors.Is(err, ErrCheckpointMismatch) {
			result.Valid = false
			result.BadSequence = seq

			return result, nil
		}

		if err != nil {
			return result, err
		}
	}

	return result, nil
}

// verifyOne checks the checkpoint under key and answers the sequence that
// its key names.
func (c Checkpoints) verifyOne(ctx context.Context, scope namespace.Name, key blob.Key) (int64, error) {
	name := strings.TrimSuffix(strings.TrimPrefix(key.String(), checkpointDir(scope)), ".json")

	seq, err := strconv.ParseInt(name, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: key %s names no sequence", ErrCheckpointMismatch, key)
	}

	cp, err := c.read(ctx, key)
	if err != nil {
		return seq, err
	}

	if cp.Scope != scope || cp.Sequence != seq {
		return seq, fmt.Errorf("%w: key %s holds %s at %d", ErrCheckpointMismatch, key, cp.Scope, cp.Sequence)
	}

	err = c.Signer.Verify(cp)
	if err != nil {
		return seq, err
	}

	hash, err := c.hashAt(ctx, scope, seq)
	if err != nil {
		return seq, err
	}

	if hash != cp.Hash {
		return seq, fmt.Errorf("%w: hash at %s %d differs", ErrCheckpointMismatch, scope, seq)
	}

	return seq, nil
}

// read reads and decodes the checkpoint under key.
func (c Checkpoints) read(ctx context.Context, key blob.Key) (Checkpoint, error) {
	body, err := c.Blobs.Get(ctx, key)
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

// hashAt answers the hash of the entry of scope at seq. List answers
// entries newest first, so it reads the page of one entry that holds seq
// from the count of entries. An append between two reads moves the entry to
// a later page, so it reads again from the sequence it found, at most
// hashAtReads times. An entry that is missing or holds another sequence
// does not match.
func (c Checkpoints) hashAt(ctx context.Context, scope namespace.Name, seq int64) (string, error) {
	head, err := c.Entries.List(ctx, scope, Page{Number: 1, Size: 1})
	if err != nil {
		return "", fmt.Errorf("read audit entries of %s: %w", scope, err)
	}

	if head.Total < seq || seq < 1 {
		return "", fmt.Errorf("%w: %s holds no entry %d", ErrCheckpointMismatch, scope, seq)
	}

	number := head.Total - seq + 1

	for range hashAtReads {
		page, err := c.Entries.List(ctx, scope, Page{Number: int(number), Size: 1})
		if err != nil {
			return "", fmt.Errorf("read audit entry %s %d: %w", scope, seq, err)
		}

		if len(page.Entries) != 1 || page.Entries[0].Sequence < seq {
			break
		}

		if got := page.Entries[0].Sequence; got > seq {
			number += got - seq

			continue
		}

		return page.Entries[0].Hash, nil
	}

	return "", fmt.Errorf("%w: %s holds no entry %d", ErrCheckpointMismatch, scope, seq)
}
