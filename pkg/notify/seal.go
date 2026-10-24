package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// SealedSecret is a secret item of a sink as the store keeps it: its ref,
// and its value sealed for the scope and the ID of the sink. Its fields
// are not exported, so a caller cannot put a plaintext value in it by
// mistake: SinkSealer.Seal makes it from a value, and a store adapter
// makes it from a stored row with StoredSecret. Only SinkSealer.Open
// gives the value back, and it opens only a value that Seal sealed for
// the same scope, sink and ref.
type SealedSecret struct {
	scope      namespace.Name
	sink       string
	ref        string
	ciphertext []byte
}

// StoredSecret returns the SealedSecret that a store reads back: the
// ciphertext of the secret item with the given ref of the sink in scope, as
// the store got it from SealedSecret.Ciphertext. Only a store adapter
// calls it.
func StoredSecret(scope namespace.Name, sink, ref string, ciphertext []byte) SealedSecret {
	return SealedSecret{scope: scope, sink: sink, ref: ref, ciphertext: slices.Clone(ciphertext)}
}

// Scope returns the scope of the sink that the value is sealed for.
func (s SealedSecret) Scope() namespace.Name { return s.scope }

// Sink returns the ID of the sink that the value is sealed for.
func (s SealedSecret) Sink() string { return s.sink }

// Ref returns the ref of the secret item.
func (s SealedSecret) Ref() string { return s.ref }

// Ciphertext returns a copy of the sealed value, for the store to keep.
func (s SealedSecret) Ciphertext() []byte { return slices.Clone(s.ciphertext) }

// MaxSealedLen caps the sealed value of a secret item of a sink, in bytes:
// the value, its ref and a separator, and at most 64 bytes that the seal
// adds.
const MaxSealedLen = MaxSecretLen + MaxRefLen + 1 + 64

// ValidateSealed returns ErrInvalid for sealed secret items of s that
// Seal did not make for s: an item sealed for another scope or another
// sink, an empty ref, a ref that s.SecretRefs does not hold, a ref that
// two items share, or a ciphertext that is empty or longer than
// MaxSealedLen. The store of Subscriptions.PutSink calls it.
func (s Sink) ValidateSealed(secrets []SealedSecret) error {
	seen := make([]string, 0, len(secrets))

	for _, sec := range secrets {
		switch {
		case sec.scope != s.Scope || sec.sink != s.ID:
			return fmt.Errorf("%w: a secret of sink %s is sealed for another sink", ErrInvalid, s.ID)
		case sec.ref == "" || !slices.Contains(s.SecretRefs, sec.ref) || slices.Contains(seen, sec.ref):
			return fmt.Errorf("%w: each new secret of a sink has its own ref of the sink", ErrInvalid)
		case len(sec.ciphertext) == 0 || len(sec.ciphertext) > MaxSealedLen:
			return fmt.Errorf("%w: a sealed secret of a sink has 1 to %d bytes", ErrInvalid, MaxSealedLen)
		}

		seen = append(seen, sec.ref)
	}

	return nil
}

// ScopeSealer seals and opens values under the data key of a namespace
// scope. *secret.Service implements it.
type ScopeSealer interface {
	// Encrypt seals each plaintext under the data key of scope, in order.
	Encrypt(ctx context.Context, scope secret.Scope, plaintexts [][]byte) ([][]byte, error)
	// Decrypt opens each ciphertext that Encrypt made for scope. It
	// returns an error that matches secret.ErrDecrypt for a ciphertext of
	// another scope.
	Decrypt(ctx context.Context, scope secret.Scope, ciphertexts [][]byte) ([][]byte, error)
}

// InstallationSealer seals and opens values under the data key of an
// installation scope. *secret.InstallationSecrets implements it.
type InstallationSealer interface {
	// Seal seals plaintext under the data key of the installation scope id.
	Seal(ctx context.Context, id string, plaintext []byte) ([]byte, error)
	// Open opens a ciphertext that Seal made for id. It returns an error
	// that matches secret.ErrDecrypt for a ciphertext of another scope.
	Open(ctx context.Context, id string, ciphertext []byte) ([]byte, error)
}

// SinkSealer seals the secret values of sinks before they reach the store,
// and opens them for a send. Each sink has its own data key: the
// secret scope of the namespace of the sink and the sink ID, or for a sink
// of audit.ServerScope the installation scope of the sink ID. So a value
// sealed for one sink does not open for another sink or another scope.
// The sealed value also holds its ref, so a value does not open under
// another ref of the sink. Seal and Open refuse a sink ID that does not
// start with SinkIDPrefix. The installation scope of a sink is its ID, so
// the prefix keeps the installation scopes of sinks apart from the other
// installation scopes.
type SinkSealer struct {
	Namespaces   ScopeSealer
	Installation InstallationSealer
}

// Seal validates the new secret items of s with s.ValidateSecrets and
// seals each value for s. The API calls it before Subscriptions.PutSink,
// so the store never gets a plaintext value.
func (b SinkSealer) Seal(ctx context.Context, s Sink, secrets []SinkSecret) ([]SealedSecret, error) {
	if err := checkSinkID(s); err != nil {
		return nil, err
	}

	if err := s.ValidateSecrets(secrets); err != nil {
		return nil, err
	}

	out := make([]SealedSecret, 0, len(secrets))

	for _, sec := range secrets {
		c, err := b.sealValue(ctx, s, bind(sec.Ref, sec.Value))
		if err != nil {
			return nil, fmt.Errorf("seal secret %s of sink %s: %w", sec.Ref, s.ID, err)
		}

		out = append(out, SealedSecret{scope: s.Scope, sink: s.ID, ref: sec.Ref, ciphertext: c})
	}

	return out, nil
}

// Open returns the values of the sealed secret items of s, in the order of
// s.SecretRefs. The dispatcher calls it with the result of
// Subscriptions.SinkSecrets just before a send, and keeps the values only
// in memory for that send. It returns ErrInvalid for items that are not
// one for each ref of s, in that order, or that are sealed for another
// sink, and the error of the sealer for a value that does not open for s,
// which matches secret.ErrDecrypt.
func (b SinkSealer) Open(ctx context.Context, s Sink, sealed []SealedSecret) ([][]byte, error) {
	if err := checkSinkID(s); err != nil {
		return nil, err
	}

	if len(sealed) != len(s.SecretRefs) {
		return nil, fmt.Errorf("%w: sink %s has %d secrets, got %d", ErrInvalid, s.ID, len(s.SecretRefs), len(sealed))
	}

	out := make([][]byte, 0, len(sealed))

	for i, sec := range sealed {
		if sec.scope != s.Scope || sec.sink != s.ID || sec.ref != s.SecretRefs[i] {
			return nil, fmt.Errorf("%w: secret %d is not sealed for sink %s", ErrInvalid, i, s.ID)
		}

		p, err := b.openValue(ctx, s, sec.ciphertext)
		if err != nil {
			return nil, fmt.Errorf("open secret %s of sink %s: %w", sec.ref, s.ID, err)
		}

		v, ok := unbind(sec.ref, p)
		if !ok {
			clear(p)

			return nil, fmt.Errorf("open secret %s of sink %s: %w: the value is of another ref",
				sec.ref, s.ID, secret.ErrDecrypt)
		}

		out = append(out, v)
	}

	return out, nil
}

// Destination is what a send to a sink needs from its secret items, opened
// for that one send: the URL of a chat sink or of a webhook sink, and the
// signing keys of a webhook sink. URL is empty for an email sink. Only the
// dispatcher and the test of a sink hold one, in memory, and never log it.
type Destination struct {
	URL  string
	Keys [][]byte
}

// Clear overwrites the signing keys. The caller calls it after the send.
func (d *Destination) Clear() {
	for _, k := range d.Keys {
		clear(k)
	}
}

// OpenDestination opens the sealed secret items of s, for a send. A chat
// sink gives its URL, and a webhook sink its URL and its signing keys. The
// URL must have the host s.Target: the host is what a person approved. An
// email sink has no secret item and gives an empty Destination.
//
// It fails closed. It returns an error that matches ErrNoURL, and also
// secret.ErrDecrypt when the sealer refused a value, for an item that is
// missing, an item that does not open, an empty URL, and a URL that has
// another host than the target. The error never holds a value.
func (b SinkSealer) OpenDestination(ctx context.Context, s Sink, sealed []SealedSecret) (Destination, error) {
	if s.Kind == SinkEmail {
		return Destination{URL: "", Keys: nil}, nil
	}

	values, err := b.Open(ctx, s, sealed)
	if err != nil {
		return Destination{URL: "", Keys: nil}, fmt.Errorf("%w: %w", ErrNoURL, err)
	}

	// Defense in depth: Sink.Validate keeps one URL item and, for a webhook, at
	// least one key. This check stays so that a sink that skipped it never
	// makes an index panic here.
	if len(values) == 0 || s.Kind == SinkWebhook && len(values) < 2 {
		for _, v := range values {
			clear(v)
		}

		return Destination{URL: "", Keys: nil}, fmt.Errorf("%w: sink %s lacks a secret item", ErrNoURL, s.ID)
	}

	d := Destination{URL: string(values[0]), Keys: values[1:]}
	clear(values[0])

	if s.Kind == SinkChat {
		d.Keys = nil
	}

	if err = checkDestination(s, d.URL); err != nil {
		d.Clear()

		return Destination{URL: "", Keys: nil}, err
	}

	return d, nil
}

// checkDestination returns an error that matches ErrNoURL when the URL of an
// opened item does not parse, or has another host than the target of the
// sink. It checks the host and nothing else: the outbound rules for the
// scheme, the address and the port are the job of the sender and of the
// check before a send, and a URL they refuse is a refusal, not a missing URL.
//
// Defense in depth: the API checked the host when it sealed the value, and
// the sender checks the address again. This check stays so that a value that
// a restore or another version of the server left behind never leaves for a
// host that nobody approved.
func checkDestination(s Sink, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u == nil || !strings.EqualFold(u.Host, s.Target) {
		return fmt.Errorf("%w: the URL is not on the host of sink %s", ErrNoURL, s.ID)
	}

	return nil
}

// checkSinkID returns ErrInvalid for a sink ID that does not start with
// SinkIDPrefix.
func checkSinkID(s Sink) error {
	if !strings.HasPrefix(s.ID, SinkIDPrefix) {
		return fmt.Errorf("%w: the sink ID %q does not start with %q", ErrInvalid, s.ID, SinkIDPrefix)
	}

	return nil
}

// errNoSealer reports a SinkSealer with no sealer for the scope of a sink.
var errNoSealer = errors.New("no sealer for the scope of the sink")

func (b SinkSealer) sealValue(ctx context.Context, s Sink, p []byte) ([]byte, error) {
	defer clear(p)

	if s.Scope == audit.ServerScope {
		if b.Installation == nil {
			return nil, errNoSealer
		}

		c, err := b.Installation.Seal(ctx, s.ID, p)
		if err != nil {
			return nil, fmt.Errorf("installation scope %s: %w", s.ID, err)
		}

		return c, nil
	}

	if b.Namespaces == nil {
		return nil, errNoSealer
	}

	scope, err := secret.NewScope(s.Scope, s.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	cs, err := b.Namespaces.Encrypt(ctx, scope, [][]byte{p})
	if err != nil || len(cs) != 1 {
		return nil, fmt.Errorf("scope %s: %w", scope, sealErr(err))
	}

	return cs[0], nil
}

func (b SinkSealer) openValue(ctx context.Context, s Sink, c []byte) ([]byte, error) {
	if s.Scope == audit.ServerScope {
		if b.Installation == nil {
			return nil, errNoSealer
		}

		p, err := b.Installation.Open(ctx, s.ID, c)
		if err != nil {
			return nil, fmt.Errorf("installation scope %s: %w", s.ID, err)
		}

		return p, nil
	}

	if b.Namespaces == nil {
		return nil, errNoSealer
	}

	scope, err := secret.NewScope(s.Scope, s.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	ps, err := b.Namespaces.Decrypt(ctx, scope, [][]byte{c})
	if err != nil || len(ps) != 1 {
		return nil, fmt.Errorf("scope %s: %w", scope, sealErr(err))
	}

	return ps[0], nil
}

// sealErr returns err, or secret.ErrDecrypt for a sealer that returned no
// error and the wrong count of values.
func sealErr(err error) error {
	if err != nil {
		return err
	}

	return secret.ErrDecrypt
}

// bind returns the value v with its ref in front, and a zero byte after
// the ref. Sink.Validate allows no zero byte in a ref.
func bind(ref string, v []byte) []byte {
	return slices.Concat([]byte(ref), []byte{0}, v)
}

// unbind returns the value of p, which bind made for ref, and false when p
// holds another ref.
func unbind(ref string, p []byte) ([]byte, bool) {
	return bytes.CutPrefix(p, slices.Concat([]byte(ref), []byte{0}))
}
