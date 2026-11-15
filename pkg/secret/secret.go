// Package secret encrypts values for clients that leave key management to the
// server. The Pulumi CLI's default ("service") secrets provider sends every
// secret config value and state output here to be encrypted and decrypted.
// Large content seals in chunks instead: the server seals each OpenTofu state
// blob in 64 KiB chunks with the data key of its workspace scope, so it never
// holds a whole state in memory. Each chunk carries its position and a
// final-chunk flag in its nonce and associated data, so a cut, reordered or
// extended stream fails to open.
//
// It uses envelope encryption. Each Scope, in practice one stack, has its own
// random 256-bit data key, sealed by AES-256-GCM. Data keys are stored only
// wrapped by a key-encryption key that a Wrapper holds and never reveals: a
// local master key, a cloud KMS or an HSM. Every ciphertext is bound to its
// scope, so one stack's ciphertext cannot be decrypted as another's.
package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by its ports.
var (
	// ErrDecrypt reports a ciphertext that is corrupt, was made for another
	// scope, or whose key cannot be unwrapped. It deliberately says no more.
	ErrDecrypt = errors.New("cannot decrypt value")
	// ErrInvalidScope reports a scope that fails validation.
	ErrInvalidScope = errors.New("invalid secret scope")
	// ErrKeyNotFound reports that no data key exists for a scope.
	ErrKeyNotFound = errors.New("secret key not found")
	// ErrTooLarge reports a value or batch over its limit.
	ErrTooLarge = errors.New("secret too large")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
	// ErrKeyNotConfigured reports that no key-encryption key is configured.
	// The message names the settings that provide it, because this error
	// reaches operators directly from Encrypt, Decrypt and the start-up
	// check (ADR 23).
	ErrKeyNotConfigured = errors.New("no key-encryption key is configured: set " + keyEnvSetting)
	// ErrWrapperMismatch reports that the configured key-encryption key
	// does not open every stored data key. The message names the settings
	// that provide the key and the missing rewrap command, because this
	// error reaches operators directly from the start-up check (ADR 23).
	ErrWrapperMismatch = errors.New(
		"key-encryption key does not match stored keys: set " + keyEnvSetting + " to the matching key",
	)
)

// keyEnvSetting names the settings that provide the key-encryption key: the
// value in DUSTPAN_KEY_ENCRYPTION_KEY, or the file in
// DUSTPAN_KEY_ENCRYPTION_KEY_FILE (ADR 23).
const keyEnvSetting = "DUSTPAN_KEY_ENCRYPTION_KEY or DUSTPAN_KEY_ENCRYPTION_KEY_FILE"

const (
	formatV1      byte = 1
	dataKeyBytes       = 32
	maxScopeID         = 100
	maxValueBytes      = 1 << 20
	// sealOverhead is what a ciphertext adds to its plaintext: the format
	// byte, the 12-byte GCM nonce and the 16-byte tag.
	sealOverhead = 1 + 12 + 16
	// DefaultMaxBatchBytes bounds the total size of one Encrypt or Decrypt
	// batch. The CLI splits config batches only by size (100 MB chunks), never
	// by count, so a count limit would reject large configs.
	DefaultMaxBatchBytes = 32 << 20
	aadLabel             = "opendustpan/secret/v1"
)

// Scope names the data key values are encrypted under. ID must stay the same
// for as long as ciphertexts exist: use a stack's stable ID, never its name,
// which a rename changes.
type Scope struct {
	Namespace namespace.Name
	ID        string
}

// NewScope validates a scope.
func NewScope(ns namespace.Name, id string) (Scope, error) {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return Scope{}, fmt.Errorf("%w: %w", ErrInvalidScope, err)
	}

	if !ident.Valid(id, maxScopeID) {
		return Scope{}, fmt.Errorf("%w: id %q", ErrInvalidScope, id)
	}

	return Scope{Namespace: ns, ID: id}, nil
}

// String formats the scope as namespace/id.
func (s Scope) String() string {
	return s.Namespace.String() + "/" + s.ID
}

// aadSource gives the associated data that binds a sealed stream to what it
// seals: a Scope for a stream under a data key, an itemContent (the item and
// the ID of its key record) for a stream under an item key.
type aadSource interface {
	aad() []byte
}

// aad is the associated data that binds keys and values to the scope. It
// separates its parts with a zero byte, and it relies on NewScope to refuse a
// namespace or a scope ID with a zero byte. The encoding itself does not
// enforce that.
func (s Scope) aad() []byte {
	return []byte(aadLabel + "\x00" + s.Namespace.String() + "\x00" + s.ID)
}

// WrappedKey is a data key as stored: encrypted by the key-encryption key
// named WrapperID.
type WrappedKey struct {
	CreatedAt  time.Time
	Scope      Scope
	WrapperID  string
	Ciphertext []byte
}

// Wrapper holds a key-encryption key and never reveals it.
type Wrapper interface {
	// ID names the key-encryption key. It is stored with each wrapped key, so
	// keys wrapped before a rotation can still be traced to their wrapper.
	ID() string
	// Wrap encrypts a data key and binds it to aad.
	Wrap(ctx context.Context, key, aad []byte) ([]byte, error)
	// Unwrap reverses Wrap. It returns ErrDecrypt if ciphertext or aad differ
	// from what Wrap produced and was given.
	Unwrap(ctx context.Context, ciphertext, aad []byte) ([]byte, error)
}

// KeyStore persists wrapped data keys.
type KeyStore interface {
	// CreateKey stores k unless a key for k.Scope exists, and returns the
	// stored key either way, so concurrent first encryptions agree on one
	// key. It fails with namespace.ErrFenced if the fence is stale.
	CreateKey(ctx context.Context, k WrappedKey, fence namespace.Fence) (WrappedKey, error)
	// GetKey returns the scope's key, or ErrKeyNotFound.
	GetKey(ctx context.Context, scope Scope) (WrappedKey, error)
	// ListWrapperIDs returns the distinct wrapper IDs of stored keys, ordered,
	// for the start-up check that refuses a key-encryption key that opens
	// less than every one of them. It returns an empty list when no key exists.
	ListWrapperIDs(ctx context.Context) ([]string, error)
}

// Config holds the dependencies of a Service.
type Config struct {
	Keys KeyStore
	// Wrapper holds the key-encryption key. It may be nil when no key is
	// configured; Encrypt and Decrypt then fail with ErrKeyNotConfigured.
	Wrapper    Wrapper
	Namespaces *namespace.Service
	Now        func() time.Time
	// MaxBatchBytes bounds one batch; zero means DefaultMaxBatchBytes.
	MaxBatchBytes int
}

// Service encrypts and decrypts values per scope.
type Service struct {
	keys       KeyStore
	wrapper    Wrapper
	namespaces *namespace.Service
	now        func() time.Time
	maxBatch   int
}

// NewService returns a Service. Keys and Namespaces are required. Wrapper
// may be nil when no key-encryption key is configured; Encrypt and Decrypt
// then fail with ErrKeyNotConfigured.
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.Keys == nil:
		return nil, fmt.Errorf("%w: key store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	maxBatch := cfg.MaxBatchBytes
	if maxBatch <= 0 {
		maxBatch = DefaultMaxBatchBytes
	}

	return &Service{keys: cfg.Keys, wrapper: cfg.Wrapper, namespaces: cfg.Namespaces, now: now, maxBatch: maxBatch}, nil
}

// MaxBatchBytes returns the largest batch Encrypt and Decrypt accept.
func (s *Service) MaxBatchBytes() int {
	return s.maxBatch
}

// CheckWrapper reports whether wrapper can open the data keys in keys, for
// the start-up check that refuses a key-encryption key that opens less than
// every one of them (ADR 23). It returns nil when keys holds no wrapped key:
// a server without a key-encryption key may start while the database holds
// no wrapped key. Otherwise wrapper must be set, and every stored wrapper ID
// must name it. A database with keys under more than one key stays refused
// until a rewrap command exists to move them to one key.
func CheckWrapper(ctx context.Context, keys KeyStore, wrapper Wrapper) error {
	if keys == nil {
		return fmt.Errorf("%w: key store", ErrMissingDependency)
	}

	ids, err := keys.ListWrapperIDs(ctx)
	if err != nil {
		return fmt.Errorf("list wrapped keys: %w", err)
	}

	return checkWrapperIDs(ids, wrapper)
}

// checkWrapperIDs needs wrapper to name each of the stored wrapper IDs ids.
// No ID passes with no wrapper.
func checkWrapperIDs(ids []string, wrapper Wrapper) error {
	if len(ids) == 0 {
		return nil
	}

	if wrapper == nil {
		return fmt.Errorf("server refuses to start: database holds wrapped keys: %w", ErrKeyNotConfigured)
	}

	for _, id := range ids {
		if id != wrapper.ID() {
			return fmt.Errorf(
				"server refuses to start: database holds keys wrapped by %q: server holds %q: "+
					"rewrap the database with one key-encryption key: no rewrap command exists yet (ADR 23): %w",
				ids,
				wrapper.ID(),
				ErrWrapperMismatch,
			)
		}
	}

	return nil
}

// OpenKey opens the wrapped data key k with wrapper. It binds the ciphertext
// to the scope of k, so a key of another scope does not open. Restore calls
// it on one key of the backup to prove that the key-encryption key opens the
// backup before it writes anything (ADR 30). A failure is ErrDecrypt.
func OpenKey(ctx context.Context, wrapper Wrapper, k WrappedKey) ([]byte, error) {
	if wrapper == nil {
		return nil, fmt.Errorf("open key of %s: %w", k.Scope, ErrKeyNotConfigured)
	}

	key, err := wrapper.Unwrap(ctx, k.Ciphertext, k.Scope.aad())
	if err != nil {
		return nil, fmt.Errorf("open key of %s: %w", k.Scope, err)
	}

	return key, nil
}

// Encrypt seals each plaintext under the scope's data key, creating the key on
// first use. The result has one ciphertext per plaintext, in order.
func (s *Service) Encrypt(ctx context.Context, scope Scope, plaintexts [][]byte) ([][]byte, error) {
	if err := checkBatch(plaintexts, maxValueBytes, s.maxBatch); err != nil {
		return nil, err
	}

	aead, err := s.dataKey(ctx, scope, true)
	if err != nil {
		return nil, err
	}

	out := make([][]byte, len(plaintexts))

	for i, p := range plaintexts {
		out[i], err = sealValue(aead, scope.aad(), p)
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

// sealValue seals p with aead and binds it to aad. The result is the
// format byte, the nonce and the sealed value.
func sealValue(aead cipher.AEAD, aad, p []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize(), 1+aead.NonceSize()+len(p)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	sealed := aead.Seal(nil, nonce, p, aad)

	return append(append([]byte{formatV1}, nonce...), sealed...), nil
}

// openValue reverses sealValue. It returns false for a value that is
// malformed or does not open with aead and aad.
func openValue(aead cipher.AEAD, aad, c []byte) ([]byte, bool) {
	if len(c) < 1+aead.NonceSize()+aead.Overhead() || c[0] != formatV1 {
		return nil, false
	}

	nonce, sealed := c[1:1+aead.NonceSize()], c[1+aead.NonceSize():]

	p, err := aead.Open(nil, nonce, sealed, aad)
	if err != nil {
		return nil, false
	}

	return p, true
}

// Decrypt opens each ciphertext made by Encrypt for the same scope. Any
// failure is ErrDecrypt; the batch fails as a whole.
func (s *Service) Decrypt(ctx context.Context, scope Scope, ciphertexts [][]byte) ([][]byte, error) {
	limit, total := maxValueBytes+sealOverhead, s.maxBatch+len(ciphertexts)*sealOverhead
	if err := checkBatch(ciphertexts, limit, total); err != nil {
		return nil, err
	}

	aead, err := s.dataKey(ctx, scope, false)
	if err != nil {
		return nil, err
	}

	out := make([][]byte, len(ciphertexts))

	for i, c := range ciphertexts {
		p, ok := openValue(aead, scope.aad(), c)
		if !ok {
			return nil, fmt.Errorf("%w: value %d of %s", ErrDecrypt, i, scope)
		}

		out[i] = p
	}

	return out, nil
}

// dataKey returns the scope's data key as an AEAD, creating it if create is
// set and none exists yet.
// dataKey returns the AEAD of the data key of scope, for the value paths.
func (s *Service) dataKey(ctx context.Context, scope Scope, create bool) (cipher.AEAD, error) {
	key, err := s.unwrapDataKey(ctx, scope, create)
	if err != nil {
		return nil, err
	}

	defer clear(key)

	return newAEAD(key)
}

// unwrapDataKey returns the raw data key of scope, creating it on first use
// when create is set. The caller clears the key.
func (s *Service) unwrapDataKey(ctx context.Context, scope Scope, create bool) ([]byte, error) {
	stored, err := s.keys.GetKey(ctx, scope)

	switch {
	case errors.Is(err, ErrKeyNotFound) && create:
		stored, err = s.createKey(ctx, scope)
		if err != nil {
			return nil, err
		}
	case errors.Is(err, ErrKeyNotFound) && s.wrapper == nil:
		return nil, fmt.Errorf("%w for %s: %w", ErrDecrypt, scope, ErrKeyNotConfigured)
	case errors.Is(err, ErrKeyNotFound):
		// Nothing was ever encrypted in this scope.
		return nil, fmt.Errorf("%w: no key for %s", ErrDecrypt, scope)
	case err != nil:
		return nil, fmt.Errorf("get key for %s: %w", scope, err)
	default:
	}

	if s.wrapper == nil {
		return nil, fmt.Errorf("%w for %s: %w", ErrDecrypt, scope, ErrKeyNotConfigured)
	}

	return unwrapKey(
		ctx,
		s.wrapper,
		sealedKey{wrapperID: stored.WrapperID, ciphertext: stored.Ciphertext},
		scope.aad(),
		"key for "+scope.String(),
	)
}

func (s *Service) createKey(ctx context.Context, scope Scope) (WrappedKey, error) {
	if s.wrapper == nil {
		return WrappedKey{}, fmt.Errorf("create key for %s: %w", scope, ErrKeyNotConfigured)
	}

	ns, err := s.namespaces.WritableHere(ctx, scope.Namespace)
	if err != nil {
		return WrappedKey{}, fmt.Errorf("create key for %s: %w", scope, err)
	}

	wrapped, err := wrapNewKey(ctx, s.wrapper, scope.aad(), "key for "+scope.String())
	if err != nil {
		return WrappedKey{}, err
	}

	stored, err := s.keys.CreateKey(ctx, WrappedKey{
		CreatedAt:  s.now().UTC(),
		Scope:      scope,
		WrapperID:  s.wrapper.ID(),
		Ciphertext: wrapped,
	}, ns.Fence())
	if err != nil {
		return WrappedKey{}, fmt.Errorf("store key for %s: %w", scope, err)
	}

	return stored, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: data key: %w", ErrDecrypt, err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: data key: %w", ErrDecrypt, err)
	}

	return aead, nil
}

// checkBatch bounds each value to limit bytes and the batch to total bytes.
func checkBatch(values [][]byte, limit, total int) error {
	sum := 0

	for i, v := range values {
		if len(v) > limit {
			return fmt.Errorf("%w: value %d has %d bytes, limit %d", ErrTooLarge, i, len(v), limit)
		}

		sum += len(v)
	}

	if sum > total {
		return fmt.Errorf("%w: batch of %d bytes, limit %d", ErrTooLarge, sum, total)
	}

	return nil
}
