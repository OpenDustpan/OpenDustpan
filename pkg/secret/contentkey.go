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

// Sentinel errors of content keys.
var (
	// ErrShredded reports a content key that a redaction, a retention period
	// or a purge destroyed. The content that it sealed cannot be opened
	// again, by this server or from a backup.
	ErrShredded = errors.New("content key is shredded")
	// ErrInvalidContentRef reports a content reference that fails
	// validation.
	ErrInvalidContentRef = errors.New("invalid content reference")
)

// ContentKind names what one content key seals. The kinds are the same for
// every tool.
type ContentKind string

// The content kinds.
const (
	// ContentKindState is one state item: the checkpoints of one Pulumi
	// update, or one OpenTofu state version with its outputs document.
	ContentKindState ContentKind = "state"
	// ContentKindPlan is one stored plan with all its parts.
	ContentKindPlan ContentKind = "plan"
	// ContentKindJobLog is the log of one job with all its chunks.
	ContentKindJobLog ContentKind = "job-log"
)

// Valid reports whether k is one of the content kinds.
func (k ContentKind) Valid() bool {
	switch k {
	case ContentKindState, ContentKindPlan, ContentKindJobLog:
		return true
	default:
		return false
	}
}

// maxContentItem bounds the item ID of a content reference.
const maxContentItem = 100

// ContentRef names one history item: the scope of its unit, its kind, and
// the ID that the tool gives the item, for example an update ID or a state
// version ID. The core keeps the item ID opaque.
type ContentRef struct {
	Scope Scope
	Kind  ContentKind
	Item  string
}

// NewContentRef validates a content reference. It returns
// ErrInvalidContentRef for an unknown kind or a malformed item ID, and
// ErrInvalidScope for a malformed scope.
func NewContentRef(scope Scope, kind ContentKind, item string) (ContentRef, error) {
	if _, err := NewScope(scope.Namespace, scope.ID); err != nil {
		return ContentRef{}, err
	}

	if !kind.Valid() {
		return ContentRef{}, fmt.Errorf("%w: kind %q", ErrInvalidContentRef, kind)
	}

	if !ident.Valid(item, maxContentItem) {
		return ContentRef{}, fmt.Errorf("%w: item %q", ErrInvalidContentRef, item)
	}

	return ContentRef{Scope: scope, Kind: kind, Item: item}, nil
}

// String formats the reference as namespace/scope/kind/item.
func (r ContentRef) String() string {
	return r.Scope.String() + "/" + string(r.Kind) + "/" + r.Item
}

// ContentKeyID is the stable, opaque ID of one content key, with the prefix
// "ck-".
type ContentKeyID string

// contentKeyPrefix starts every content key ID.
const contentKeyPrefix = "ck-"

// contentKeyObjectPrefix starts the blob key of every wrapped content key.
const contentKeyObjectPrefix = "content-keys/"

// NewContentKeyID returns a new random content key ID.
func NewContentKeyID() ContentKeyID {
	return ContentKeyID(ident.NewID(contentKeyPrefix))
}

// ParseContentKeyID validates s as a content key ID. It returns
// ErrInvalidContentRef when s lacks the prefix or is not one blob key
// segment.
func ParseContentKeyID(s string) (ContentKeyID, error) {
	if !strings.HasPrefix(s, contentKeyPrefix) || len(s) == len(contentKeyPrefix) || strings.Contains(s, "/") {
		return "", fmt.Errorf("%w: content key ID %q", ErrInvalidContentRef, s)
	}

	if _, err := blob.ParseKey(contentKeyObjectPrefix + s); err != nil {
		return "", fmt.Errorf("%w: content key ID %q", ErrInvalidContentRef, s)
	}

	return ContentKeyID(s), nil
}

// Object returns the blob key that holds the wrapped content key:
// content-keys/<id>. The server writes it one time. A backup copies it like
// every other blob, and every backup in one target shares the copy, so one
// delete in the target reaches all of them. id must be valid.
func (id ContentKeyID) Object() blob.Key {
	return blob.Key(contentKeyObjectPrefix + string(id))
}

// ContentKey is the record of one content key, as SQL keeps it. The wrapped
// key itself is not part of the record: it is the blob that Object names.
// ShreddedAt is the zero time while the key exists.
type ContentKey struct {
	CreatedAt  time.Time
	ShreddedAt time.Time
	Ref        ContentRef
	ID         ContentKeyID
	WrapperID  string
}

// Shredded reports whether the key is shredded.
func (k ContentKey) Shredded() bool {
	return !k.ShreddedAt.IsZero()
}

// ContentKeyIndex is the persistence port for content key records. Each
// history item has one record. A record never comes back after a shred: a
// new item gets a new key.
type ContentKeyIndex interface {
	// CreateContentKey stores the record k unless a record for k.Ref exists,
	// and returns the stored record either way, so that two writers of the
	// first save of one item agree on one key. It returns ErrShredded with
	// the stored record when the record for k.Ref is shredded, and
	// namespace.ErrFenced when the fence is stale.
	CreateContentKey(ctx context.Context, k ContentKey, fence namespace.Fence) (ContentKey, error)
	// GetContentKey returns the record of ref. It returns ErrKeyNotFound
	// when no record exists, and ErrShredded with the record when the key
	// is shredded.
	GetContentKey(ctx context.Context, ref ContentRef) (ContentKey, error)
	// ShredContentKey marks the record of ref shredded at the time at, and
	// returns it. A second shred returns the record with the first shred
	// time and no error. It returns ErrKeyNotFound when no record exists,
	// and namespace.ErrFenced when the fence is stale. The caller deletes
	// the key blob after the commit.
	ShredContentKey(ctx context.Context, ref ContentRef, at time.Time, fence namespace.Fence) (ContentKey, error)
	// ListContentKeys returns at most limit records of scope, shredded or
	// not, ordered by ID, after the ID after (empty for the first page). A
	// purge reads them to shred every key of a unit.
	ListContentKeys(ctx context.Context, scope Scope, after ContentKeyID, limit int) ([]ContentKey, error)
	// ListContentWrapperIDs returns the distinct wrapper IDs of records that
	// are not shredded, ordered, for the start-up check. It returns an empty
	// list when no such record exists.
	ListContentWrapperIDs(ctx context.Context) ([]string, error)
}

// ContentSealer seals and opens the content of history items, each item
// under its own random content key. It never derives a content key from
// another key, so a shred makes every copy of the content unreadable, also
// the copies in backups. binding is the extra associated data of the
// content, for example the state version ID, so that content of one item
// does not open as another part of the same item.
type ContentSealer interface {
	// NewContentKey makes the content key of ref: it writes the wrapped key
	// blob, then the record. It returns the stored record when ref already
	// has one. It returns ErrShredded when the key of ref is shredded,
	// ErrKeyNotConfigured when no key-encryption key is configured, and
	// namespace.ErrFenced when the fence is stale.
	NewContentKey(ctx context.Context, ref ContentRef, fence namespace.Fence) (ContentKey, error)
	// SealContent seals the plaintext in r with the content key of ref. The
	// caller streams the result. It returns ErrKeyNotFound when ref has no
	// content key and ErrShredded when the key is shredded.
	SealContent(ctx context.Context, ref ContentRef, binding []byte, r io.Reader) (io.Reader, error)
	// OpenContent opens a stream that SealContent sealed for ref and
	// binding. It reads the record before any blob, so it returns
	// ErrShredded at once after a shred on any instance. It returns
	// ErrKeyNotFound when ref has no content key, and ErrDecrypt for a
	// stream of another item or binding, or a changed stream.
	OpenContent(ctx context.Context, ref ContentRef, binding []byte, r io.Reader) (io.Reader, error)
}
