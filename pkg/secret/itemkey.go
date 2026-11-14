package secret

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
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
	// ErrItemKeyBlobMismatch reports a wrapped item key blob whose size or
	// MD5 is not the size or the MD5 that its record names. The store
	// replaced the object, or someone changed it. The server does not unwrap
	// such a blob.
	ErrItemKeyBlobMismatch = errors.New("item key blob does not match its record")
	// ErrItemKeyAttemptExpired reports an item key transaction that did not
	// commit before its attempt deadline. The transaction rolls back, and its
	// blob stays for the reaper.
	ErrItemKeyAttemptExpired = errors.New("item key write passed its attempt deadline")
	// ErrItemKeyBlobClockSkew reports that the clock of the blob store and
	// the clock of the server differ by more than ItemKeyBlobClockSkew, ahead
	// or behind. The reaper deletes nothing in that run.
	ErrItemKeyBlobClockSkew = errors.New(
		"the blob store clock differs from the server clock by more than the allowed skew",
	)
)

// ItemKeyAttemptDeadline is the longest time from the start of an item key
// transaction to its commit. The writer makes the blob key and puts the
// wrapped key blob inside the transaction, so the time from the put to the
// commit is shorter. A blob that no committed record names becomes garbage
// only after ItemKeyBlobGracePeriod, which is longer than this deadline plus
// ItemKeyBlobClockSkew, so the reaper never deletes the blob of a write that
// can still commit.
const ItemKeyAttemptDeadline = 30 * time.Minute

// ItemKeyBlobClockSkew is the largest difference between the clock of the
// server and the clock of the blob store that the reaper rule allows. The
// reaper reads the age of a blob from the time of the store, so each run
// measures the difference with a probe blob first, and a larger difference
// in either direction fails the run.
const ItemKeyBlobClockSkew = 15 * time.Minute

// AttemptDeadline returns the attempt deadline of an item key transaction
// that starts at start under ctx: the earlier of the deadline of ctx and
// start plus ItemKeyAttemptDeadline.
func AttemptDeadline(ctx context.Context, start time.Time) time.Time {
	deadline := start.Add(ItemKeyAttemptDeadline)

	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		return d
	}

	return deadline
}

// CheckAttemptDeadline returns ErrItemKeyAttemptExpired when now is not
// before deadline. A store calls it after the function of an item key
// transaction and before the commit.
func CheckAttemptDeadline(deadline, now time.Time) error {
	if !now.Before(deadline) {
		return fmt.Errorf(
			"%w: the commit is %s late",
			ErrItemKeyAttemptExpired,
			now.Sub(deadline).Round(time.Millisecond),
		)
	}

	return nil
}

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

// ItemKeyBlobInfo describes the wrapped key blob that a record names: its
// version, the attempt part of its key, and the size and MD5 of its bytes, so
// that a backup copies and checks it like every other blob, and a read checks
// the bytes before it unwraps them. The MD5 is a checksum for integrity only.
// The AEAD tag of the wrapped key authenticates the bytes.
//
// Attempt is the attempt part of the blob key. Each write of a wrapped key
// makes a new attempt part, so two writes never share a key, and a store that
// replaces an object on a second write never replaces a key that a record
// names.
type ItemKeyBlobInfo struct {
	Attempt string
	MD5     [16]byte
	Size    int64
	Version int
}

// ItemKey is the record of one item key, as SQL keeps it. The wrapped
// key itself is not part of the record: it is the blob that ItemKeyBlobs.Key
// names with Blob.Version and Blob.Attempt. ShreddedAt is the zero time while the key exists.
type ItemKey struct {
	CreatedAt  time.Time
	ShreddedAt time.Time
	Ref        ItemRef
	ID         ItemKeyID
	WrapperID  string
	Blob       ItemKeyBlobInfo
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

// ItemKeyTx is the item key part of one open transaction of the store that
// holds the item rows. Its writes commit or roll back with every other write
// of that transaction, so a record never exists without its item, and an item
// never exists without its record. A caller that creates an item opens the
// transaction with its fence, passes the ItemKeyTx to ItemSealer.NewItemKey,
// and writes the item row in the same transaction. An ItemKeyTx is valid only
// inside the function that the store gives it to.
//
// The transaction holds the fence that the caller gave when it opened the
// transaction. CreateItemKey checks the namespace of its record against it.
// The reads do not check the fence.
type ItemKeyTx interface {
	// CreateItemKey stores the record k unless a record for k.Ref exists,
	// and returns the stored record either way. It returns ErrShredded with
	// the stored record when the record for k.Ref is shredded, and
	// namespace.ErrFenced when the namespace of k is not the namespace of the
	// fence of the transaction.
	CreateItemKey(ctx context.Context, k ItemKey) (ItemKey, error)
	// GetItemKey returns the record of ref, with the errors of
	// ItemKeyIndex.GetItemKey.
	GetItemKey(ctx context.Context, ref ItemRef) (ItemKey, error)
	// GetNamespace reads a namespace inside the transaction. The sealer needs
	// the ID of the namespace to name the key blob, and a read through the
	// store could wait for the transaction that the caller holds open. It
	// returns namespace.ErrNotFound for a name that the store does not hold.
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// ItemKeyStore is an ItemKeyIndex that also runs a function in one fenced
// transaction. The SQL store adapters implement it.
type ItemKeyStore interface {
	ItemKeyIndex
	// InItemKeyTx runs fn in one write transaction that checks fence first.
	// It commits when fn returns nil and rolls back otherwise. It returns
	// namespace.ErrFenced when the fence is stale. fn must not start another
	// transaction of the store.
	//
	// The transaction has the deadline that AttemptDeadline gives for the
	// start of the call. When the deadline passes before the commit, the
	// transaction rolls back and InItemKeyTx returns
	// ErrItemKeyAttemptExpired.
	InItemKeyTx(ctx context.Context, fence namespace.Fence, fn func(ItemKeyTx) error) error
}

// ItemSealer seals and opens the content of history items, each item
// under its own random item key. It never derives an item key from
// another key, so a shred makes every copy of the content unreadable, also
// the copies in backups. binding is the extra associated data of the
// content, for example the state version ID, so that content of one item
// does not open as another part of the same item.
type ItemSealer interface {
	// NewItemKey makes the item key of ref: it writes the wrapped key
	// blob, then the record through tx, so the record commits in the
	// transaction of the caller, with the item. It returns the stored record
	// when ref already has one. It returns ErrShredded when the key of ref is
	// shredded, ErrKeyNotConfigured when no key-encryption key is configured,
	// and namespace.ErrFenced when fence is not the namespace of ref or the
	// fence of tx is stale. The caller passes the fence that it opened tx
	// with. tx holds that fence too, but NewItemKey compares the namespace of
	// ref with fence before it writes the blob, and tx compares the
	// namespaces only when it writes the record, after the blob. When the
	// transaction of the caller rolls back, the blob stays as an orphan for
	// the orphan cleanup, and no record stays.
	NewItemKey(ctx context.Context, tx ItemKeyTx, ref ItemRef, fence namespace.Fence) (ItemKey, error)
	// SealContent seals the plaintext in r with the item key of ref. The
	// caller streams the result. It returns ErrKeyNotFound when ref has no
	// item key and ErrShredded when the key is shredded.
	SealContent(ctx context.Context, ref ItemRef, binding []byte, r io.Reader) (io.Reader, error)
	// OpenContent opens a stream that SealContent sealed for ref and
	// binding. It reads the record before any blob, so it returns
	// ErrShredded at once after a shred on any instance. It returns
	// ErrKeyNotFound when ref has no item key, and ErrDecrypt for a
	// stream of another item, record or binding, or a changed stream. The
	// reader can hold the raw item key until it reads the stream header. The
	// caller closes the reader: Close clears the key, also when the reader
	// was never read, and a Read after Close returns ErrStreamClosed.
	OpenContent(ctx context.Context, ref ItemRef, binding []byte, r io.Reader) (io.ReadCloser, error)
	// SealContents seals the Data of each part with the item key of ref and
	// the Binding of the part, as SealContent does, and opens the key one
	// time for all of them. It returns the sealed bytes in the order of
	// parts, and the errors of SealContent. On an error it returns no part.
	// The caller bounds the number and the size of the parts.
	SealContents(ctx context.Context, ref ItemRef, parts []ContentPart) ([][]byte, error)
	// OpenContents opens the Data of each part, as OpenContent does, and
	// opens the key one time for all of them. It returns the plain bytes in
	// the order of parts, and the errors of OpenContent, also ErrDecrypt for
	// a part of another item or binding. On an error it returns no part.
	OpenContents(ctx context.Context, ref ItemRef, parts []ContentPart) ([][]byte, error)
}

// ContentPart is one content of a batch of SealContents or OpenContents:
// its binding and its bytes.
type ContentPart struct {
	Binding []byte
	Data    []byte
}

// itemKeyAADLabel starts the associated data of a wrapped item key, and
// itemContentAADLabel starts the associated data of each chunk of the content
// that an item key seals. Each purpose has its own label, so a wrapped key
// never opens as content and content never opens as a wrapped key. Both
// differ from aadLabel, so no item ciphertext opens as the value of a scope,
// and no value of a scope opens as an item. Both differ from streamKeyInfo,
// the HKDF info of a stream key.
const (
	itemKeyAADLabel     = "opendustpan/item-key/v1"
	itemContentAADLabel = "opendustpan/item-content/v1"
)

// itemAADLengthBase is the base of the length before each part of itemAAD.
const itemAADLengthBase = 10

// itemAAD is the associated data of one purpose, label, for the item ref and
// the item key record id. Each part has its length before it, in decimal
// digits and then a colon, so the encoding alone keeps the parts apart: no
// value of one part, a zero byte or a colon included, can move bytes into the
// next part.
//
// Defense in depth: NewItemRef and ParseItemKeyID refuse a part with a zero
// byte. The length prefix stays so that the encoding does not rely on them.
func itemAAD(label string, ref ItemRef, id ItemKeyID) []byte {
	parts := []string{
		label, ref.Scope.Namespace.String(), ref.Scope.ID, string(ref.Kind), ref.Item, string(id),
	}

	var aad []byte

	for _, p := range parts {
		aad = strconv.AppendInt(aad, int64(len(p)), itemAADLengthBase)
		aad = append(aad, ':')
		aad = append(aad, p...)
	}

	return aad
}

// itemKeyAAD is the associated data that binds a wrapped item key to the
// scope, the kind and the item ID of ref, and to the ID of its record. A
// wrapped key that is put under another record does not unwrap.
func itemKeyAAD(ref ItemRef, id ItemKeyID) []byte {
	return itemAAD(itemKeyAADLabel, ref, id)
}

// itemContent is the aadSource of the content that one item key seals: the
// reference of the item and the ID of its key record.
type itemContent struct {
	ref ItemRef
	id  ItemKeyID
}

// aad binds each chunk of the content to the scope, the kind and the item ID
// of the reference, and to the ID of the key record. Content that is put
// under another record does not open.
func (c itemContent) aad() []byte {
	return itemAAD(itemContentAADLabel, c.ref, c.id)
}
