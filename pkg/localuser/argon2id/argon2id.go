// Package argon2id is the adapter of localuser.PasswordHasher on Argon2id
// (RFC 9106, version 19) from golang.org/x/crypto/argon2.
//
// A hash is stored as a PHC string:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>
//
// with the salt and the key in standard base64 without padding. The parser
// reads a stored hash as untrusted input: it refuses each form that is not
// exactly this one, and each parameter outside the bounds of package
// localuser, before it does any hash work. So a changed database row cannot
// make the server spend unbounded memory or time.
//
// Each hash takes its memory cost from a Budget that the hashes of one
// instance share. A hash that finds no room within localuser.HashWait
// returns localuser.ErrHashBusy.
package argon2id

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"opendustpan.org/opendustpan/pkg/localuser"
)

var _ localuser.PasswordHasher = (*Hasher)(nil)

// MaxEncodedLength is the length limit of a stored hash. The longest valid
// hash, with a salt of 64 bytes and a key of 64 bytes, is below it.
const MaxEncodedLength = 256

// prefix is the start of each hash of this adapter: the algorithm and the
// version.
const prefix = "$argon2id$v=19$"

// maxParamDigits is the longest decimal form of a parameter that the parser
// reads: MaxMemoryKiB has 5 digits.
const maxParamDigits = 5

// decimalBase is the base of the numbers of the parameters.
const decimalBase = 10

// The counts of the fields of a stored hash: the parameters, the salt and the
// key after the version, and the three parameters m, t and p.
const (
	fieldsAfterVersion = 3
	paramCount         = 3
)

// Decoded is a stored hash after Decode.
type Decoded struct {
	Salt   []byte
	Key    []byte
	Params localuser.HashParams
}

// Hasher hashes and checks passwords with Argon2id.
type Hasher struct {
	budget *Budget
}

// New returns a Hasher that takes the memory of each hash from budget. It
// returns localuser.ErrInvalid for a nil budget.
func New(budget *Budget) (*Hasher, error) {
	if budget == nil {
		return nil, fmt.Errorf("%w: the hasher needs a memory budget", localuser.ErrInvalid)
	}

	return &Hasher{budget: budget}, nil
}

// Hash implements localuser.PasswordHasher.
func (h *Hasher) Hash(ctx context.Context, password string, p localuser.HashParams) (localuser.PasswordHash, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}

	if len(password) > localuser.MaxBytes {
		return "", fmt.Errorf("%w: %d bytes", localuser.ErrPasswordTooLong, len(password))
	}

	salt := make([]byte, p.SaltBytes)
	// crypto/rand.Read never returns an error: it panics when the system
	// source fails.
	_, _ = rand.Read(salt)

	key, err := h.derive(ctx, password, salt, p)
	if err != nil {
		return "", err
	}

	return Encode(p, salt, key), nil
}

// Verify implements localuser.PasswordHasher. It is Reserve, then the
// Verify of the reservation, then its Release.
func (h *Hasher) Verify(
	ctx context.Context, password string, stored localuser.PasswordHash, current localuser.HashParams,
) (localuser.Verdict, error) {
	// The reservation checks the length too. This check comes first so that
	// a password that is too long takes no budget.
	if len(password) > localuser.MaxBytes {
		return localuser.Verdict{NeedsRehash: false}, fmt.Errorf(
			"%w: %d bytes", localuser.ErrPasswordTooLong, len(password),
		)
	}

	r, err := h.Reserve(ctx, stored)
	if err != nil {
		return localuser.Verdict{NeedsRehash: false}, err
	}
	defer r.Release()

	return r.Verify(password, current)
}

// Reserve implements localuser.PasswordHasher. It parses stored before it
// takes any budget, so a hash that does not parse never waits and never
// holds memory.
func (h *Hasher) Reserve(ctx context.Context, stored localuser.PasswordHash) (*localuser.Reservation, error) {
	d, err := Decode(stored)
	if err != nil {
		return nil, err
	}

	release, err := h.budget.Acquire(ctx, d.Params.MemoryKiB)
	if err != nil {
		return nil, err
	}

	r := localuser.NewReservation(func(password string, current localuser.HashParams) (localuser.Verdict, error) {
		return compare(d, password, current)
	}, release)
	if r == nil {
		release()

		return nil, fmt.Errorf("%w: no reservation", localuser.ErrUnavailable)
	}

	return r, nil
}

// compare checks password against the decoded hash d, on memory that the
// caller holds in the budget.
func compare(d Decoded, password string, current localuser.HashParams) (localuser.Verdict, error) {
	none := localuser.Verdict{NeedsRehash: false}

	// Defense in depth: the service checks the current parameters when it
	// loads its settings; this check stays so that a new caller cannot
	// compare a hash with parameters that no hash may have.
	if err := current.Validate(); err != nil {
		return none, err
	}

	if len(password) > localuser.MaxBytes {
		return none, fmt.Errorf("%w: %d bytes", localuser.ErrPasswordTooLong, len(password))
	}

	p := d.Params

	// Defense in depth: Decode checks the parameters; this check stays so
	// that no path reaches argon2 with a cost outside the bounds.
	if err := p.Validate(); err != nil {
		return none, err
	}

	got := argon2.IDKey([]byte(password), d.Salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyBytes)

	if subtle.ConstantTimeCompare(got, d.Key) != 1 {
		return none, localuser.ErrWrongPassword
	}

	return localuser.Verdict{NeedsRehash: p != current}, nil
}

// derive takes the memory of p from the budget and returns the key of
// password with salt and p.
func (h *Hasher) derive(ctx context.Context, password string, salt []byte, p localuser.HashParams) ([]byte, error) {
	// Defense in depth: Hash and Decode check the parameters; this check
	// stays so that no path reaches argon2 with a cost outside the bounds.
	if err := p.Validate(); err != nil {
		return nil, err
	}

	release, err := h.budget.Acquire(ctx, p.MemoryKiB)
	if err != nil {
		return nil, err
	}
	defer release()

	return argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyBytes), nil
}

// Encode returns the PHC string of a hash with the parameters p, the salt
// and the key.
func Encode(p localuser.HashParams, salt, key []byte) localuser.PasswordHash {
	enc := base64.RawStdEncoding

	return localuser.PasswordHash(prefix +
		"m=" + strconv.FormatUint(uint64(p.MemoryKiB), 10) +
		",t=" + strconv.FormatUint(uint64(p.Iterations), 10) +
		",p=" + strconv.FormatUint(uint64(p.Parallelism), 10) +
		"$" + enc.EncodeToString(salt) +
		"$" + enc.EncodeToString(key))
}

// Decode parses a stored hash. It returns the parameters, the salt and the
// key, or localuser.ErrInvalid for each form that Encode does not make and
// for each parameter outside the bounds. It does no hash work and never
// panics.
func Decode(stored localuser.PasswordHash) (Decoded, error) {
	var none Decoded

	s := string(stored)
	if len(s) > MaxEncodedLength {
		return none, fmt.Errorf("%w: the hash has %d bytes", localuser.ErrInvalid, len(s))
	}

	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return none, fmt.Errorf("%w: the hash is not argon2id version 19", localuser.ErrInvalid)
	}

	parts := strings.Split(rest, "$")
	if len(parts) != fieldsAfterVersion {
		return none, fmt.Errorf("%w: the hash has %d fields after the version", localuser.ErrInvalid, len(parts))
	}

	p, err := decodeParams(parts[0])
	if err != nil {
		return none, err
	}

	salt, err := decodeBytes(parts[1], localuser.MinSaltBytes, localuser.MaxSaltBytes, "salt")
	if err != nil {
		return none, err
	}

	key, err := decodeBytes(parts[2], localuser.MinKeyBytes, localuser.MaxKeyBytes, "key")
	if err != nil {
		return none, err
	}

	p.SaltBytes, p.KeyBytes = byteCount(salt), byteCount(key)

	if err := p.Validate(); err != nil {
		return none, err
	}

	return Decoded{Salt: salt, Key: key, Params: p}, nil
}

// byteCount returns the length of b, which decodeBytes bounds to at most
// MaxKeyBytes. It returns 0 for a longer b, which Validate refuses.
func byteCount(b []byte) uint32 {
	n := len(b)
	if n > localuser.MaxKeyBytes || n > localuser.MaxSaltBytes {
		return 0
	}

	return uint32(n)
}

// decodeParams parses "m=<n>,t=<n>,p=<n>", in this order, with decimal
// numbers that have no sign and no leading zero.
func decodeParams(s string) (localuser.HashParams, error) {
	none := localuser.HashParams{
		Algorithm: "", MemoryKiB: 0, Iterations: 0, Parallelism: 0, SaltBytes: 0, KeyBytes: 0,
	}

	fields := strings.Split(s, ",")
	if len(fields) != paramCount {
		return none, fmt.Errorf("%w: the hash has %d parameters, want 3", localuser.ErrInvalid, len(fields))
	}

	names := [paramCount]string{"m", "t", "p"}

	var values [paramCount]uint32

	for i, f := range fields {
		raw, ok := strings.CutPrefix(f, names[i]+"=")
		if !ok {
			return none, fmt.Errorf("%w: hash parameter %d is not %q", localuser.ErrInvalid, i+1, names[i])
		}

		v, err := decodeNumber(raw)
		if err != nil {
			return none, fmt.Errorf("%w: hash parameter %q", err, names[i])
		}

		values[i] = v
	}

	lanes, err := laneCount(values[2])
	if err != nil {
		return none, err
	}

	return localuser.HashParams{
		Algorithm:   localuser.AlgorithmArgon2id,
		MemoryKiB:   values[0],
		Iterations:  values[1],
		Parallelism: lanes,
		SaltBytes:   0,
		KeyBytes:    0,
	}, nil
}

// laneCount returns v as a count of lanes, or localuser.ErrInvalid above
// localuser.MaxParallelism.
func laneCount(v uint32) (uint8, error) {
	if v > math.MaxUint8 || v > localuser.MaxParallelism {
		return 0, fmt.Errorf(
			"%w: hash parallelism %d, want %d to %d",
			localuser.ErrInvalid, v, localuser.MinParallelism, localuser.MaxParallelism,
		)
	}

	return uint8(v), nil
}

// decodeNumber parses a decimal number of 1 to maxParamDigits ASCII digits
// with no leading zero.
func decodeNumber(s string) (uint32, error) {
	if s == "" || len(s) > maxParamDigits {
		return 0, fmt.Errorf("%w: a number of %d characters", localuser.ErrInvalid, len(s))
	}

	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("%w: a number with a leading zero", localuser.ErrInvalid)
	}

	var v uint32

	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%w: a number with a character that is not a digit", localuser.ErrInvalid)
		}

		v = v*decimalBase + uint32(c-'0')
	}

	return v, nil
}

// decodeBytes decodes standard base64 without padding, strictly, and checks
// that the result has lo to hi bytes. It checks the encoded length before
// it decodes.
func decodeBytes(s string, lo, hi int, name string) ([]byte, error) {
	enc := base64.RawStdEncoding.Strict()

	if len(s) < enc.EncodedLen(lo) || len(s) > enc.EncodedLen(hi) {
		return nil, fmt.Errorf("%w: hash %s of %d characters", localuser.ErrInvalid, name, len(s))
	}

	// The decoder skips CR and LF even in strict mode, so each character
	// is checked against the alphabet first.
	for i := range len(s) {
		if !isBase64(s[i]) {
			return nil, fmt.Errorf("%w: hash %s holds a character that is not base64", localuser.ErrInvalid, name)
		}
	}

	b, err := enc.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: hash %s is not base64", localuser.ErrInvalid, name)
	}

	if len(b) < lo || len(b) > hi {
		return nil, fmt.Errorf("%w: hash %s of %d bytes, want %d to %d", localuser.ErrInvalid, name, len(b), lo, hi)
	}

	return b, nil
}

// isBase64 reports whether c is in the alphabet of standard base64.
func isBase64(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/'
}
