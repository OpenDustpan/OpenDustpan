// Package jobtoken defines the short-lived credential that the server gives
// to the runner of one job.
//
// A job token authenticates as the principal that started the job, the
// starter. It names the job, the unit of the job and the claim of the durable
// operation that the runner holds. It adds no action and no object type: the
// grants of the starter decide what a request can do.
//
// The store keeps only the SHA-256 digest of the secret, so a read of the
// database gives no usable token. Every request checks the live claim and the
// job against the record. So a token stops at the end of its claim, at a
// takeover, at the end of the job and at its hard expiry. No revoke is
// needed.
package jobtoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// MaxLifetime is the deadline that runner.LimitsFor gives for the longest
// phase that job.ValidateSteps accepts: job.MaxSteps steps, each with
// job.MaxStepTimeout and the grace period of a step that changes resources.
// Token.Validate uses it as the backstop at read time. The live checks of the
// claim and the lease end a token earlier.
const MaxLifetime = runner.StartAllowance + job.MaxSteps*(job.MaxStepTimeout+runner.GraceChanges) + runner.ReportTime

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrInvalid reports a token with a missing or bad field, an expiry that
	// is not after the issue time or is above the deadline of the phase, or a
	// starter that is not the principal that started the job.
	ErrInvalid = errors.New("invalid job token")
	// ErrNotFound reports a record, a job, a claim operation or a starter
	// principal that does not exist.
	ErrNotFound = errors.New("job token not found")
	// ErrNotLive reports an issue whose claim or job is not live.
	ErrNotLive = errors.New("job token is not live")
	// ErrAlreadyExists reports a digest that a record already uses.
	ErrAlreadyExists = errors.New("job token already exists")
)

// SecretPrefix starts each secret, so that secret scanners can find a job
// token in code, logs and chat. The rest is 43 characters of unpadded
// base64url: 256 bits from crypto/rand.
const SecretPrefix = "odj_"

// Secret sizes.
const (
	secretEntropyBytes = 32
	secretEncodedLen   = 43
	// SecretLength is the length of a secret in bytes.
	SecretLength = len(SecretPrefix) + secretEncodedLen
)

// redacted replaces a secret in every formatted output.
const redacted = "[job token]"

// Secret is the plain value of a job token. Its String and GoString methods
// redact it, so it cannot leak through a log or an error by accident. Use
// Reveal only to give the secret to the runner of its job.
type Secret struct {
	value string
}

// NewSecret returns a secret with 256 bits from crypto/rand.
func NewSecret() (Secret, error) {
	b := make([]byte, secretEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return Secret{}, fmt.Errorf("generate job token: %w", err)
	}

	return Secret{value: SecretPrefix + base64.RawURLEncoding.EncodeToString(b)}, nil
}

// ParseSecret returns the secret that raw holds, or ErrInvalid. It never
// changes raw, so a value that is not a secret is refused.
func ParseSecret(raw string) (Secret, error) {
	if !LooksLikeSecret(raw) {
		return Secret{}, fmt.Errorf("%w: secret", ErrInvalid)
	}

	return Secret{value: raw}, nil
}

// Reveal returns the plain value of s, to give it to the runner of its job.
func (s Secret) Reveal() string {
	return s.value
}

// String redacts the secret.
func (Secret) String() string {
	return redacted
}

// GoString redacts the secret in %#v output too.
func (Secret) GoString() string {
	return redacted
}

// Digest is what a store keeps in place of a secret: the SHA-256 of the
// secret.
type Digest [sha256.Size]byte

// DigestOf returns the digest of the secret s.
func DigestOf(s Secret) Digest {
	return sha256.Sum256([]byte(s.value))
}

// digestOfRaw returns the digest of a secret as a client sent it.
func digestOfRaw(raw string) Digest {
	return sha256.Sum256([]byte(raw))
}

// IsZero reports whether d is the digest of no secret.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// String returns the digest in lower-case hex, the form a store keeps.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// ParseDigest reads the hex form of String.
func ParseDigest(s string) (Digest, error) {
	var d Digest

	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != len(d) {
		return Digest{}, fmt.Errorf("%w: digest", ErrInvalid)
	}

	copy(d[:], raw)

	return d, nil
}

// Equal reports, in constant time, whether d and other are the same digest.
func (d Digest) Equal(other Digest) bool {
	return subtle.ConstantTimeCompare(d[:], other[:]) == 1
}

// JobStateLive reports whether s is a job state in which a job token can be
// live: the job is dispatched, running or canceling. Every other state,
// including the empty state and an unknown state, is not live.
func JobStateLive(s job.State) bool {
	switch s {
	case job.StateQueued, job.StateAwaitingApproval, job.StateAwaitingState, job.StateSucceeded,
		job.StateFailed, job.StateCanceled, job.StateRejected, job.StateExpired:
		return false
	case job.StateDispatched, job.StateRunning, job.StateCanceling:
		return true
	}

	return false
}

// LooksLikeSecret reports whether raw has the form of a secret: the prefix
// and 43 base64url characters. A check of the form needs no store read, so a
// random value never reaches the store.
func LooksLikeSecret(raw string) bool {
	rest, ok := strings.CutPrefix(raw, SecretPrefix)
	if !ok || len(rest) != secretEncodedLen {
		return false
	}

	_, err := base64.RawURLEncoding.DecodeString(rest)

	return err == nil
}

// Token is the stored record of one job token, without its secret. Digest is
// the SHA-256 of the secret. StarterID names the principal that started the
// job. JobID names the job. Namespace is the namespace of the unit. Unit is
// the unit reference with its stable unit ID. ClaimOperation is the durable
// operation of the claim, and ClaimToken is the claim token of that
// operation. ExpiresAt is the hard expiry, which the caller sets from the
// step timeout and a grace period. CreatedAt is the time of the issue.
type Token struct {
	Digest         Digest
	StarterID      string
	JobID          job.ID
	Namespace      namespace.Name
	Unit           identity.Object
	ClaimOperation durable.ID
	ClaimToken     int64
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

// Validate returns ErrInvalid for a token with a missing field, a unit that
// is not a unit reference with a stable ID, a namespace that differs from the
// namespace of the unit, an expiry that is not after the creation time, or an
// expiry that is after the creation time plus MaxLifetime.
func (t Token) Validate() error {
	if err := t.validateFields(); err != nil {
		return err
	}

	return t.validateTimes()
}

// validateFields checks the identity fields of t.
func (t Token) validateFields() error {
	switch {
	case t.Digest.IsZero():
		return fmt.Errorf("%w: missing digest", ErrInvalid)
	case t.StarterID == "":
		return fmt.Errorf("%w: missing starter principal", ErrInvalid)
	case t.JobID == "":
		return fmt.Errorf("%w: missing job", ErrInvalid)
	case t.Unit.UnitID() == "":
		return fmt.Errorf("%w: the unit has no stable ID", ErrInvalid)
	case t.Unit.Namespace() != t.Namespace:
		return fmt.Errorf("%w: the unit is in another namespace", ErrInvalid)
	case t.ClaimOperation == "":
		return fmt.Errorf("%w: missing claim operation", ErrInvalid)
	case t.ClaimToken <= 0:
		return fmt.Errorf("%w: missing claim token", ErrInvalid)
	default:
		return nil
	}
}

// validateTimes checks the times of t.
func (t Token) validateTimes() error {
	switch {
	case t.CreatedAt.IsZero():
		return fmt.Errorf("%w: missing creation time", ErrInvalid)
	case !t.ExpiresAt.After(t.CreatedAt):
		return fmt.Errorf("%w: the expiry is not after the creation time", ErrInvalid)
	case t.ExpiresAt.After(t.CreatedAt.Add(MaxLifetime)):
		return fmt.Errorf("%w: the job token lifetime is too long", ErrInvalid)
	default:
		return nil
	}
}

// Claim is the live state that the store reads at the same time as a token
// record: the state of the durable operation, its claim token, the end of its
// lease, the ID of the job, the state of the job, the current operation of the
// job, the unit ID of the job, the principal that started the job, the starter
// principal and whether the starter is enabled. JobID is the ID of the job
// that the store read. JobStarterID is the ID of the principal that the store
// resolved from the subject in job.StartedBy; a subject with no principal
// gives ErrNotFound. Starter is the principal record of Token.StarterID,
// with the fields that a request carries. StarterRecordID is the ID of that
// principal record. StarterEnabled is true only when the store read the
// starter and the starter is not disabled.
type Claim struct {
	OperationState      durable.State
	LeaseEndsAt         time.Time
	JobID               job.ID
	JobState            job.State
	JobOperation        durable.ID
	JobUnitID           string
	JobStarterID        string
	Starter             identity.Principal
	StarterRecordID     string
	OperationClaimToken int64
	StarterEnabled      bool
}

// Match is the answer of a lookup by digest: the token record and the claim
// that the store reads with it.
type Match struct {
	Token Token
	Claim Claim
}
