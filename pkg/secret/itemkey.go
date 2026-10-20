package secret

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of item keys.
var (
	// ErrShredded reports an item key that a redaction, a retention period
	// or a purge destroyed. The content that it sealed cannot be opened
	// again, by this server or from a backup.
	ErrShredded = errors.New("item key is shredded")
	// ErrInvalidItemRef reports an item reference that fails validation.
	ErrInvalidItemRef = errors.New("invalid item reference")
)

// ItemKind names what one item key seals. The kinds are the same for
// every tool.
type ItemKind string

// The item kinds.
const (
	// ItemKindState is one state item: one Pulumi update (every checkpoint
	// blob, its update event payloads, and its journal entry blobs) or one
	// OpenTofu state version with its outputs document.
	ItemKindState ItemKind = "state"
	// ItemKindPlan is one stored plan of a job.
	ItemKindPlan ItemKind = "plan"
	// ItemKindJobLog is one version of a job log, with all its chunks.
	ItemKindJobLog ItemKind = "job-log"
)

// Valid reports whether k is one of the item kinds.
func (k ItemKind) Valid() bool {
	switch k {
	case ItemKindState, ItemKindPlan, ItemKindJobLog:
		return true
	default:
		return false
	}
}

// maxItemID bounds the item ID of an item reference.
const maxItemID = 100

// ItemRef names one history item: the scope of its unit, its kind, and
// the ID that the tool gives the item, for example an update ID or a state
// version ID. The core keeps the item ID opaque.
type ItemRef struct {
	Scope Scope
	Kind  ItemKind
	Item  string
}

// NewItemRef validates an item reference. It returns
// ErrInvalidItemRef for an unknown kind or a malformed item ID, and
// ErrInvalidScope for a malformed scope.
func NewItemRef(scope Scope, kind ItemKind, item string) (ItemRef, error) {
	if _, err := NewScope(scope.Namespace, scope.ID); err != nil {
		return ItemRef{}, err
	}

	if !kind.Valid() {
		return ItemRef{}, fmt.Errorf("%w: kind %q", ErrInvalidItemRef, kind)
	}

	if !ident.Valid(item, maxItemID) {
		return ItemRef{}, fmt.Errorf("%w: item %q", ErrInvalidItemRef, item)
	}

	return ItemRef{Scope: scope, Kind: kind, Item: item}, nil
}

// String formats the reference as namespace/scope/kind/item.
func (r ItemRef) String() string {
	return r.Scope.String() + "/" + string(r.Kind) + "/" + r.Item
}

// ItemKeyID is the stable, opaque ID of one item key, with the prefix
// "ik-".
type ItemKeyID string

// itemKeyPrefix starts every item key ID.
const itemKeyPrefix = "ik-"

// itemKeyDir is the directory, inside the scope of one namespace, of the
// blob keys of the wrapped item keys.
const itemKeyDir = "item-keys/"

// NewItemKeyID returns a new random item key ID.
func NewItemKeyID() ItemKeyID {
	return ItemKeyID(ident.NewID(itemKeyPrefix))
}

// ParseItemKeyID validates s as an item key ID. It returns
// ErrInvalidItemRef when s lacks the prefix or is not one blob key
// segment.
func ParseItemKeyID(s string) (ItemKeyID, error) {
	if !strings.HasPrefix(s, itemKeyPrefix) || len(s) == len(itemKeyPrefix) || strings.Contains(s, "/") {
		return "", fmt.Errorf("%w: item key ID %q", ErrInvalidItemRef, s)
	}

	if _, err := blob.ParseKey(itemKeyDir + s); err != nil {
		return "", fmt.Errorf("%w: item key ID %q", ErrInvalidItemRef, s)
	}

	return ItemKeyID(s), nil
}

// ItemKey is the record of one item key, as SQL keeps it. The wrapped
// key itself is not part of the record: it is the blob that ItemKeyBlobs.Key names.
// ShreddedAt is the zero time while the key exists.
type ItemKey struct {
	CreatedAt  time.Time
	ShreddedAt time.Time
	Ref        ItemRef
	ID         ItemKeyID
	WrapperID  string
}

// Shredded reports whether the key is shredded.
func (k ItemKey) Shredded() bool {
	return !k.ShreddedAt.IsZero()
}

// ItemKeyIndex is the persistence port for item key records. Each
// history item has one record. A record never comes back after a shred: a
// new item gets a new key. The SQL adapter that implements it also
// implements the tool tables and the Redactor of the history package, so a
// key record and its item row, and a shred and the redaction, commit in one
// transaction.
type ItemKeyIndex interface {
	// CreateItemKey stores the record k unless a record for k.Ref exists,
	// and returns the stored record either way, so that two writers of the
	// first save of one item agree on one key. It returns ErrShredded with
	// the stored record when the record for k.Ref is shredded, and
	// namespace.ErrFenced when the fence is stale.
	CreateItemKey(ctx context.Context, k ItemKey, fence namespace.Fence) (ItemKey, error)
	// GetItemKey returns the record of ref. It returns ErrKeyNotFound
	// when no record exists, and ErrShredded with the record when the key
	// is shredded.
	GetItemKey(ctx context.Context, ref ItemRef) (ItemKey, error)
	// ShredItemKey marks the record of ref shredded at the time at, and
	// returns it. A second shred returns the record with the first shred
	// time and no error. It returns ErrKeyNotFound when no record exists,
	// and namespace.ErrFenced when the fence is stale. The caller deletes
	// the key blob after the commit.
	ShredItemKey(ctx context.Context, ref ItemRef, at time.Time, fence namespace.Fence) (ItemKey, error)
	// ListItemKeys returns at most limit records of scope, shredded or
	// not, ordered by ID, after the ID after (empty for the first page). A
	// purge reads them to shred every key of a unit.
	ListItemKeys(ctx context.Context, scope Scope, after ItemKeyID, limit int) ([]ItemKey, error)
	// ListItemKeyWrapperIDs returns the distinct wrapper IDs of records that
	// are not shredded, ordered, for the start-up check. It returns an empty
	// list when no such record exists.
	ListItemKeyWrapperIDs(ctx context.Context) ([]string, error)
}

// ItemSealer seals and opens the content of history items, each item
// under its own random item key. It never derives an item key from
// another key, so a shred makes every copy of the content unreadable, also
// the copies in backups. binding is the extra associated data of the
// content, for example the state version ID, so that content of one item
// does not open as another part of the same item.
type ItemSealer interface {
	// NewItemKey makes the item key of ref: it writes the wrapped key
	// blob, then the record. It returns the stored record when ref already
	// has one. It returns ErrShredded when the key of ref is shredded,
	// ErrKeyNotConfigured when no key-encryption key is configured, and
	// namespace.ErrFenced when the fence is stale.
	NewItemKey(ctx context.Context, ref ItemRef, fence namespace.Fence) (ItemKey, error)
	// SealContent seals the plaintext in r with the item key of ref. The
	// caller streams the result. It returns ErrKeyNotFound when ref has no
	// item key and ErrShredded when the key is shredded.
	SealContent(ctx context.Context, ref ItemRef, binding []byte, r io.Reader) (io.Reader, error)
	// OpenContent opens a stream that SealContent sealed for ref and
	// binding. It reads the record before any blob, so it returns
	// ErrShredded at once after a shred on any instance. It returns
	// ErrKeyNotFound when ref has no item key, and ErrDecrypt for a
	// stream of another item or binding, or a changed stream.
	OpenContent(ctx context.Context, ref ItemRef, binding []byte, r io.Reader) (io.Reader, error)
}
