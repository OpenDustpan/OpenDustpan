package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
)

// SecretItemsScope is the installation scope of the data key that seals the
// secret items. The reference of each item is the binding of its value, so a
// value opens only under its own reference.
const SecretItemsScope = "agent-secret-items"

// The bounds of a secret item.
const (
	// maxSecretNameBytes is the largest length of the name of an item.
	maxSecretNameBytes = 128
	// maxSecretValueBytes is the largest length of the value of an item.
	maxSecretValueBytes = 16 * 1024
)

// SecretSealer seals and opens the value of a secret item under an
// installation scope and a binding. *secret.InstallationSecrets implements
// it.
type SecretSealer interface {
	// SealBound returns plaintext sealed under the scope id and binding.
	SealBound(ctx context.Context, id string, binding, plaintext []byte) ([]byte, error)
	// OpenBound opens a value that SealBound sealed with the same scope id
	// and binding.
	OpenBound(ctx context.Context, id string, binding, ciphertext []byte) ([]byte, error)
}

// NewSecretRef returns a new random reference of the form itm-....
func NewSecretRef() SecretRef {
	return SecretRef("itm-" + strings.ToLower(rand.Text()))
}

// CheckSecretItem returns ErrInvalid unless name is 1 to 128 bytes of
// printable UTF-8 and value is 1 byte to 16 KiB with no CR or LF.
func CheckSecretItem(name string, value []byte) error {
	switch {
	case name == "", len(name) > maxSecretNameBytes:
		return fmt.Errorf("%w: the name of a secret item has 1 to %d bytes", ErrInvalid, maxSecretNameBytes)
	case !utf8.ValidString(name), strings.ContainsFunc(name, isControlRune):
		return fmt.Errorf("%w: the name of a secret item holds a control character", ErrInvalid)
	case len(value) == 0:
		return fmt.Errorf("%w: the value of a secret item is empty", ErrInvalid)
	case len(value) > maxSecretValueBytes:
		return fmt.Errorf("%w: the value of a secret item is larger than %d bytes", ErrInvalid, maxSecretValueBytes)
	case bytes.ContainsAny(value, "\r\n"):
		return fmt.Errorf("%w: the value of a secret item holds CR or LF", ErrInvalid)
	}

	return nil
}

// ErrSecretNotFound reports a secret item the store does not know.
var ErrSecretNotFound = errors.New("agent: secret item not found")

// ErrSecretInUse reports a secret item a stored document still refers to,
// so it cannot be deleted.
var ErrSecretInUse = errors.New("agent: secret item is in use")

// SecretItem is the metadata of one sealed secret item of the scope @server.
// It never holds the value.
type SecretItem struct {
	// Ref is the stable ID of the item: itm-....
	Ref SecretRef
	// Name is the name the caller gave it when it was created.
	Name string
	// CreatedAt is the time the item was created, in Unix milliseconds.
	CreatedAt int64
	// ChangedAt is the time the item was last replaced, in Unix
	// milliseconds. It equals CreatedAt when the item was never replaced.
	ChangedAt int64
	// LastUsedAt is the time the value was last read, in Unix
	// milliseconds, or 0 when it was never read.
	LastUsedAt int64
}

// SecretItems is the persistence port for the secret items of the scope
// @server. Each write appends its audit entry in its own transaction.
// No method returns a value to a route: the value never crosses the HTTP
// boundary.
type SecretItems interface {
	// Create stores value under name and returns the item with its
	// reference. It returns ErrInvalid for an empty name or an empty value.
	Create(ctx context.Context, name string, value []byte, e audit.Entry) (SecretItem, error)
	// List returns the items, newest first, without their values.
	List(ctx context.Context) ([]SecretItem, error)
	// Get returns the metadata of the item ref, without its value, or
	// ErrSecretNotFound.
	Get(ctx context.Context, ref SecretRef) (SecretItem, error)
	// Replace sets the value of ref and returns the item with its new
	// ChangedAt. It returns ErrSecretNotFound for an unknown ref and
	// ErrInvalid for an empty value.
	Replace(ctx context.Context, ref SecretRef, value []byte, e audit.Entry) (SecretItem, error)
	// Delete removes ref. It returns ErrSecretInUse while a stored
	// document refers to the item, and ErrSecretNotFound for an unknown ref.
	Delete(ctx context.Context, ref SecretRef, e audit.Entry) error
	// Open returns the sealed value of ref for the adapter code of a later
	// change. It is not on the route layer. It returns ErrSecretNotFound
	// for an unknown ref.
	Open(ctx context.Context, ref SecretRef) ([]byte, error)
}
