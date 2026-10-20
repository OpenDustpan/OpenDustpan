package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxWrappedItemKeyBytes bounds the size of one wrapped item key blob. A
// 256-bit item key with the nonce and the tag of the wrapper is far smaller.
const MaxWrappedItemKeyBytes = 4096

// maxItemKeyVersion bounds the version of an item key blob. The version is the
// version of the namespace key that wraps the item key, and each rewrap writes a
// new blob, so a real value stays small.
const maxItemKeyVersion = 1 << 20

// OutOfScopeRecorder records that a call tried to use a blob key outside the
// namespace of the call. A recorder never holds the key of the call: the key
// can name another namespace, and it can hold text that an attacker chose.
type OutOfScopeRecorder interface {
	// RecordOutOfScope records one refusal in the namespace ns, for the
	// operation op ("put", "get" or "delete"). It returns an error when it
	// cannot record the refusal.
	RecordOutOfScope(ctx context.Context, ns namespace.ID, op string) error
}

// ItemKeyBlobs reads and writes the wrapped item key blobs. It builds a
// *blob.Scoped for each call, from the namespace ID that the caller took from
// the row that it resolved and authorized, never from a request field. Each
// blob key is namespaces/<namespace ID>/item-keys/<item key ID>/v<version>.
//
// A call with a key of another namespace returns blob.ErrOutOfScope before any
// call to the store, and writes one refusal to the OutOfScopeRecorder.
type ItemKeyBlobs struct {
	store    blob.Store
	refusals OutOfScopeRecorder
}

// NewItemKeyBlobs returns ItemKeyBlobs over store. Both arguments are
// required.
func NewItemKeyBlobs(store blob.Store, refusals OutOfScopeRecorder) (*ItemKeyBlobs, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: blob store", ErrMissingDependency)
	}

	if refusals == nil {
		return nil, fmt.Errorf("%w: out-of-scope recorder", ErrMissingDependency)
	}

	return &ItemKeyBlobs{store: store, refusals: refusals}, nil
}

// itemKeyRel returns the key of version of id, relative to the scope of a
// namespace. It returns ErrInvalidItemRef for an invalid ID or a version that
// is not in 1 to maxItemKeyVersion.
func itemKeyRel(id ItemKeyID, version int) (string, error) {
	if _, err := ParseItemKeyID(string(id)); err != nil {
		return "", err
	}

	if version < 1 || version > maxItemKeyVersion {
		return "", fmt.Errorf("%w: item key version %d", ErrInvalidItemRef, version)
	}

	return itemKeyDir + string(id) + "/v" + strconv.Itoa(version), nil
}

// ItemKeyBlob names the parts of one item key blob key.
type ItemKeyBlob struct {
	Namespace namespace.ID
	ID        ItemKeyID
	Version   int
}

// ParseItemKeyBlobKey splits a blob key of the form
// namespaces/<namespace ID>/item-keys/<item key ID>/v<version> into its
// parts. A backup check, a sweep and the purge ledger use it to name the item
// key that a blob holds. It returns ErrInvalidItemRef for any other key.
func ParseItemKeyBlobKey(key blob.Key) (ItemKeyBlob, error) {
	parts := strings.Split(key.String(), "/")
	if len(parts) != 5 || parts[0] != "namespaces" || parts[2] != strings.TrimSuffix(itemKeyDir, "/") {
		return ItemKeyBlob{}, fmt.Errorf("%w: not an item key blob key", ErrInvalidItemRef)
	}

	ns, err := namespace.ParseID(parts[1])
	if err != nil {
		return ItemKeyBlob{}, fmt.Errorf("%w: namespace ID of the key", ErrInvalidItemRef)
	}

	id, err := ParseItemKeyID(parts[3])
	if err != nil {
		return ItemKeyBlob{}, err
	}

	version, ok := parseItemKeyVersion(parts[4])
	if !ok {
		return ItemKeyBlob{}, fmt.Errorf("%w: item key version", ErrInvalidItemRef)
	}

	return ItemKeyBlob{Namespace: ns, ID: id, Version: version}, nil
}

// parseItemKeyVersion parses "v<version>": a version in 1 to maxItemKeyVersion
// in its plain decimal form, with no sign and no leading zero.
func parseItemKeyVersion(s string) (int, bool) {
	digits, ok := strings.CutPrefix(s, "v")
	if !ok {
		return 0, false
	}

	n, err := strconv.Atoi(digits)
	if err != nil || digits != strconv.Itoa(n) {
		return 0, false
	}

	return n, n >= 1 && n <= maxItemKeyVersion
}

// Key returns the blob key of version of the item key id in the namespace ns:
// namespaces/<ns>/item-keys/<id>/v<version>. The server writes each version
// one time. A backup copies it like every other blob, and every backup in one
// target shares the copy, so one delete in the target reaches all of them. It
// returns ErrInvalidItemRef for an invalid ID or version, and blob.ErrInvalidKey
// for an invalid namespace ID.
func (b *ItemKeyBlobs) Key(ns namespace.ID, id ItemKeyID, version int) (blob.Key, error) {
	rel, err := itemKeyRel(id, version)
	if err != nil {
		return "", err
	}

	v, err := b.view(ns)
	if err != nil {
		return "", err
	}

	key, err := v.Key(rel)
	if err != nil {
		return "", fmt.Errorf("item key blob key: %w", err)
	}

	return key, nil
}

// Put stores the wrapped item key under key, in the namespace ns. It returns
// ErrTooLarge for more than MaxWrappedItemKeyBytes, blob.ErrOutOfScope when
// key is outside the namespace ns or outside its item-keys directory, and
// blob.ErrExists when the key already holds a blob.
func (b *ItemKeyBlobs) Put(ctx context.Context, ns namespace.ID, key blob.Key, wrapped []byte) error {
	if len(wrapped) > MaxWrappedItemKeyBytes {
		return fmt.Errorf(
			"%w: wrapped item key has %d bytes, limit %d",
			ErrTooLarge,
			len(wrapped),
			MaxWrappedItemKeyBytes,
		)
	}

	v, err := b.scopedKey(ctx, ns, key, "put")
	if err != nil {
		return err
	}

	if err := v.Put(ctx, key, bytes.NewReader(wrapped)); err != nil {
		return b.refuse(ctx, ns, "put", fmt.Errorf("put item key blob: %w", err))
	}

	return nil
}

// Get returns the wrapped item key under key, in the namespace ns. It returns
// blob.ErrOutOfScope when key is outside the namespace ns or outside its
// item-keys directory, ErrKeyNotFound when the blob is missing, and ErrTooLarge
// for a blob over MaxWrappedItemKeyBytes.
func (b *ItemKeyBlobs) Get(ctx context.Context, ns namespace.ID, key blob.Key) ([]byte, error) {
	v, err := b.scopedKey(ctx, ns, key, "get")
	if err != nil {
		return nil, err
	}

	rc, err := v.Get(ctx, key)
	if err != nil {
		return nil, b.refuse(ctx, ns, "get", mapItemKeyBlobError(err))
	}

	return readWrappedKey(rc)
}

// readWrappedKey reads at most MaxWrappedItemKeyBytes from rc and closes it.
func readWrappedKey(rc io.ReadCloser) ([]byte, error) {
	data, readErr := io.ReadAll(io.LimitReader(rc, MaxWrappedItemKeyBytes+1))

	if closeErr := rc.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}

	if readErr != nil {
		return nil, fmt.Errorf("read item key blob: %w", readErr)
	}

	if len(data) > MaxWrappedItemKeyBytes {
		return nil, fmt.Errorf("%w: item key blob is longer than %d bytes", ErrTooLarge, MaxWrappedItemKeyBytes)
	}

	return data, nil
}

// Delete removes the blob under key, in the namespace ns. A missing blob is
// not an error. It returns blob.ErrOutOfScope when key is outside the
// namespace ns or outside its item-keys directory.
func (b *ItemKeyBlobs) Delete(ctx context.Context, ns namespace.ID, key blob.Key) error {
	v, err := b.scopedKey(ctx, ns, key, "delete")
	if err != nil {
		return err
	}

	if err := v.Delete(ctx, key); err != nil {
		return b.refuse(ctx, ns, "delete", fmt.Errorf("delete item key blob: %w", err))
	}

	return nil
}

// Versions returns the blob keys of every version of the item key id in the
// namespace ns, ordered, for a purge or a sweep.
func (b *ItemKeyBlobs) Versions(ctx context.Context, ns namespace.ID, id ItemKeyID) ([]blob.Key, error) {
	if _, err := ParseItemKeyID(string(id)); err != nil {
		return nil, err
	}

	v, err := b.view(ns)
	if err != nil {
		return nil, err
	}

	objects, err := v.List(ctx, v.Prefix()+itemKeyDir+string(id)+"/")
	if err != nil {
		return nil, b.refuse(ctx, ns, "list", fmt.Errorf("list item key blobs: %w", err))
	}

	keys := make([]blob.Key, 0, len(objects))
	for i := range objects {
		keys = append(keys, objects[i].Key)
	}

	return keys, nil
}

// view returns the view of the store for the namespace ns.
func (b *ItemKeyBlobs) view(ns namespace.ID) (*blob.Scoped, error) {
	v, err := blob.NewScoped(b.store, ns)
	if err != nil {
		return nil, fmt.Errorf("scope of item key blobs: %w", err)
	}

	return v, nil
}

// scopedKey returns the view of ns after it checks that key is a key of the
// item-keys directory of ns. A key of another namespace, or a key of another
// directory of ns, is a refusal.
//
// Defense in depth: the view refuses a key outside the namespace too. This
// check stays so that a key of a blob of another kind in the same namespace,
// for example a checkpoint, never reaches the item key code, and it does not
// rely on the view.
func (b *ItemKeyBlobs) scopedKey(ctx context.Context, ns namespace.ID, key blob.Key, op string) (*blob.Scoped, error) {
	v, err := b.view(ns)
	if err != nil {
		return nil, err
	}

	rest, ok := strings.CutPrefix(key.String(), v.Prefix()+itemKeyDir)
	if !ok {
		return nil, b.refuse(ctx, ns, op, blob.ErrOutOfScope)
	}

	if !validItemKeyRest(rest) {
		return nil, fmt.Errorf("%w: item key blob key", ErrInvalidItemRef)
	}

	return v, nil
}

// validItemKeyRest reports whether rest, the part of a key after the item-keys
// directory, is <item key ID>/v<version>.
func validItemKeyRest(rest string) bool {
	id, ver, ok := strings.Cut(rest, "/")
	if !ok {
		return false
	}

	if _, err := ParseItemKeyID(id); err != nil {
		return false
	}

	_, ok = parseItemKeyVersion(ver)

	return ok
}

// refuse writes the refusal to the recorder when err is blob.ErrOutOfScope. It
// returns err, joined with the error of the recorder when the record fails, so
// the caller still sees the refusal. Any other error passes through.
func (b *ItemKeyBlobs) refuse(ctx context.Context, ns namespace.ID, op string, err error) error {
	if !errors.Is(err, blob.ErrOutOfScope) {
		return err
	}

	if recErr := b.refusals.RecordOutOfScope(ctx, ns, op); recErr != nil {
		return errors.Join(blob.ErrOutOfScope, fmt.Errorf("record the refusal: %w", recErr))
	}

	return blob.ErrOutOfScope
}

// mapItemKeyBlobError translates a missing blob into ErrKeyNotFound, as the
// ItemSealer documents.
func mapItemKeyBlobError(err error) error {
	if errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("item key blob: %w", ErrKeyNotFound)
	}

	return fmt.Errorf("get item key blob: %w", err)
}

// Actions and fields of the audit entry of a refusal.
const (
	// ActionBlobOutOfScope is the action of the audit entry of a refusal.
	ActionBlobOutOfScope = "blob:out-of-scope"
	// outOfScopeSubject is the subject of that entry: the server itself.
	outOfScopeSubject = "server"
)

// AuditOutOfScope is an OutOfScopeRecorder that writes one audit entry of the
// kind security in the scope @server.
type AuditOutOfScope struct {
	entries audit.ServerStore
	now     func() time.Time
}

var _ OutOfScopeRecorder = (*AuditOutOfScope)(nil)

// NewAuditOutOfScope returns an OutOfScopeRecorder that appends to entries. now
// may be nil; it then is time.Now.
func NewAuditOutOfScope(entries audit.ServerStore, now func() time.Time) (*AuditOutOfScope, error) {
	if entries == nil {
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	}

	if now == nil {
		now = time.Now
	}

	return &AuditOutOfScope{entries: entries, now: now}, nil
}

// RecordOutOfScope appends the entry. The object is the ID of the namespace of
// the call, and the reason names op. The entry never holds the refused key.
func (a *AuditOutOfScope) RecordOutOfScope(ctx context.Context, ns namespace.ID, op string) error {
	switch op {
	case "put", "get", "delete", "list":
	default:
		return fmt.Errorf("%w: operation %q", ErrInvalidItemRef, op)
	}

	if _, err := namespace.ParseID(ns.String()); err != nil {
		return fmt.Errorf("record out-of-scope refusal: %w", err)
	}

	entry := audit.Entry{
		At:            a.now().UTC(),
		Namespace:     audit.ServerScope,
		Subject:       outOfScopeSubject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        ActionBlobOutOfScope,
		Object:        ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindSecurity,
		Outcome:       audit.OutcomeDenied,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "item key blob " + op + " refused: key outside the namespace",
	}

	if _, err := a.entries.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}
