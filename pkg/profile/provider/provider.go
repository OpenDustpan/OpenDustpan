// Package provider holds the profile provider port and its runtime. A
// profile provider is an adapter that the open of a profile calls for one
// provider block: it gets a short-lived credential (kind login), reads
// values from an external store (kind secrets), or reads the outputs of a
// unit (kind outputs).
//
// The Registry holds the adapters of an installation by kind and name, and
// Run evaluates the provider blocks of one open in order, each with a
// deadline. The package is tool-neutral and makes no network call: each
// adapter gets its HTTP client from the wiring.
package provider

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
)

// The errors of a provider. ErrInvalid is profile.ErrInvalid: a bad input,
// at save or at open. An adapter translates each error of its client into one
// of these errors or profile.ErrInvalid, and never returns an error of a
// library to the open.
var (
	// ErrRemoteDenied reports that the remote system refused the credential,
	// the token or the trust.
	ErrRemoteDenied = errors.New("the remote system refused the request")
	// ErrRemoteNotFound reports that the remote system has no such value or
	// object.
	ErrRemoteNotFound = errors.New("the remote system has no such value")
	// ErrUnavailable reports a timeout, a refused connection, a DNS failure,
	// a refused address or an answer of 5xx.
	ErrUnavailable = errors.New("the remote system is not available")
	// ErrTooLarge reports an answer or a result above MaxResultBytes.
	ErrTooLarge = errors.New("the answer of the remote system is too large")
	// ErrAdapter reports an error of an adapter that is of no class above.
	// Run uses it so that an unknown error never passes as a known class.
	ErrAdapter = errors.New("the provider adapter failed")
	// ErrUnknownProvider reports a block whose use names no adapter of the
	// registry. It wraps profile.ErrInvalid.
	ErrUnknownProvider = errors.New("no provider adapter has this name")
)

// Limits of a provider call.
const (
	// DefaultTimeout is the deadline of a block that sets no timeout.
	DefaultTimeout = 10 * time.Second
	// MaxTimeout is the longest deadline of a block.
	MaxTimeout = 30 * time.Second
	// MaxResultBytes is the largest result of one block, and the most bytes
	// of an answer that an adapter reads.
	MaxResultBytes = 1 << 20
	// MaxErrorBytes is the longest error message of one block.
	MaxErrorBytes = 1024
	// MaxNameBytes is the longest name of a block, of an adapter and of a
	// field of a spec.
	MaxNameBytes = 64
)

// Kind is what a provider gives to an open.
type Kind string

// The kinds of a provider.
const (
	// KindLogin gives a short-lived credential, for example an OIDC token of
	// the issuer of the namespace.
	KindLogin Kind = "login"
	// KindSecrets reads values from an external store.
	KindSecrets Kind = "secrets"
	// KindOutputs reads the outputs of a unit.
	KindOutputs Kind = "outputs"
)

// Valid reports whether k is a kind.
func (k Kind) Valid() bool {
	switch k {
	case KindLogin, KindSecrets, KindOutputs:
		return true
	default:
		return false
	}
}

// Leaf is one named value of an input or a result. Value is a string, a
// bool, a number (float64, int or int64), a []any or a map[string]any of the
// same types. Secret marks a value that came from a secret, or a credential.
// A secret value keeps its mark through each step of an open.
type Leaf struct {
	Value  any
	Secret bool
}

// Value holds the named leaves of the inputs or of the outputs of one block.
type Value map[string]Leaf

// Claims are the facts of an open, for an adapter to read. An adapter never
// changes them.
type Claims map[string]any

// TokenSource mints tokens of the issuer of the namespace for one block. The
// server binds it to the open, the block and the subject. Token returns
// profile.ErrInvalid for an audience or a lifetime that the issuer refuses,
// and the other errors of profile.Issuer.Login.
type TokenSource interface {
	Token(ctx context.Context, audience string, lifetime time.Duration) (profile.Token, error)
}

// Request is one call of Open.
type Request struct {
	// Inputs are the resolved inputs of the block.
	Inputs Value
	// Claims are the facts of the open.
	Claims Claims
	// Tokens mints tokens for this block. It is nil for an adapter whose
	// spec mints no token.
	Tokens TokenSource
	// Login is the result of the login block that this block names, or nil.
	Login *Result
}

// Result is the outputs of one block.
type Result struct {
	// Expires is when the shortest credential in Value ends, or zero. A
	// login result always sets it.
	Expires time.Time
	// Value holds the outputs. Each output that the spec marks as secret
	// has the secret mark.
	Value Value
	// Identity names the identity that the adapter used, for the audit
	// entry. It is never a credential.
	Identity string
}

// Provider is one integration of one kind. The open of a profile calls it
// once for each block that the open needs. An adapter keeps no state between
// two calls, except a cache with a limit.
type Provider interface {
	// Adapter is the name of the adapter, for example "oidc".
	Adapter() string
	// Kind is the kind of the adapter.
	Kind() Kind
	// Spec describes the inputs and the outputs.
	Spec() Spec
	// Check validates the inputs of one block. It calls no network.
	// profile.ErrInvalid names the bad input.
	Check(in Value) error
	// Open resolves one block for one open. profile.ErrInvalid for an input
	// that the remote system refuses as malformed, ErrRemoteDenied,
	// ErrRemoteNotFound, ErrUnavailable (also for a deadline that ends) and
	// ErrTooLarge. The message of an error holds no secret input, no token
	// and no byte of an answer, and has at most MaxErrorBytes.
	Open(ctx context.Context, r Request) (Result, error)
}
