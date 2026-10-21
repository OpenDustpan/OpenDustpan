package secret

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
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
	// operation op ("put", "get", "delete" or "list"). Where says how far the
	// key was out of scope. It returns an error when it cannot record the
	// refusal.
	RecordOutOfScope(ctx context.Context, ns namespace.ID, op string, where OutOfScopeWhere) error
}

// OutOfScopeWhere says where a refused key was: outside the namespace of the
// call, inside the namespace and outside the directory of the blobs of the
// call, or inside that directory and outside the directory of the unit of the
// call.
type OutOfScopeWhere string

// The places of a refused key.
const (
	// OutsideNamespace is a key that is not inside the namespace of the call.
	OutsideNamespace OutOfScopeWhere = "namespace"
	// OutsideDirectory is a key inside the namespace of the call and outside
	// the directory of the blobs of the call, for example the key of a
	// checkpoint in a call for an item key.
	OutsideDirectory OutOfScopeWhere = "directory"
	// OutsideUnitDirectory is a key inside the directory of the blobs of the
	// call and outside the directory of the unit of the call, for example the
	// key of another workspace in a call for one workspace.
	OutsideUnitDirectory OutOfScopeWhere = "unit-directory"
)

// OutOfScopeFamily names the blobs of one recorder in the reason of its
// entries.
type OutOfScopeFamily struct {
	// Blobs names the blobs, for example "item key blob".
	Blobs string
	// Dir is the directory of the blobs inside the namespace, for example
	// "item-keys".
	Dir string
}

// ItemKeyFamily is the family of the item key blobs.
func ItemKeyFamily() OutOfScopeFamily {
	return OutOfScopeFamily{Blobs: "item key blob", Dir: strings.TrimSuffix(itemKeyDir, "/")}
}

// maxFamilyNameLen bounds each name of an OutOfScopeFamily.
const maxFamilyNameLen = 32

// validFamilyName reports whether s is a name of an OutOfScopeFamily:
// lowercase ASCII letters, spaces and hyphens, at most maxFamilyNameLen bytes,
// with a letter first. The name goes into the reason of an audit entry, so the
// allowlist keeps line breaks and other control bytes out.
func validFamilyName(s string) bool {
	if s == "" || len(s) > maxFamilyNameLen || s[0] < 'a' || s[0] > 'z' {
		return false
	}

	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && c != ' ' && c != '-' {
			return false
		}
	}

	return true
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
		return b.refuse(ctx, ns, "put", fmt.Errorf("put item key blob: %w", err), OutsideNamespace)
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
		return nil, b.refuse(ctx, ns, "get", mapItemKeyBlobError(err), OutsideNamespace)
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
		return b.refuse(ctx, ns, "delete", fmt.Errorf("delete item key blob: %w", err), OutsideNamespace)
	}

	return nil
}

// Versions returns the blob keys of every version of the item key id in the
// namespace ns, in the order of the version number, for a purge or a sweep. It
// returns ErrInvalidItemRef when the listing holds a key that is not a version
// of the item key id.
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
		return nil, b.refuse(ctx, ns, "list", fmt.Errorf("list item key blobs: %w", err), OutsideNamespace)
	}

	// Defense in depth: the view lists only under the prefix of the item key.
	// Each key is checked again, and it must name this namespace and this item
	// key, so a listing fault of an adapter never gives the purge a key of
	// another item key.
	parsed := make([]ItemKeyBlob, 0, len(objects))

	for i := range objects {
		part, parseErr := ParseItemKeyBlobKey(objects[i].Key)
		if parseErr != nil || part.Namespace != ns || part.ID != id {
			return nil, fmt.Errorf(
				"%w: the listing holds a key that is not a version of the item key",
				ErrInvalidItemRef,
			)
		}

		parsed = append(parsed, part)
	}

	// The order is the order of the version number: v2 comes before v10, which
	// the order of the key text does not give.
	slices.SortFunc(parsed, func(a, b ItemKeyBlob) int { return cmp.Compare(a.Version, b.Version) })

	keys := make([]blob.Key, 0, len(parsed))

	for _, part := range parsed {
		key, keyErr := b.Key(ns, part.ID, part.Version)
		if keyErr != nil {
			return nil, keyErr
		}

		keys = append(keys, key)
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
		return nil, b.refuse(ctx, ns, op, blob.ErrOutOfScope, OutOfScopePlace(v, key))
	}

	if !validItemKeyRest(rest) {
		return nil, fmt.Errorf("%w: item key blob key", ErrInvalidItemRef)
	}

	return v, nil
}

// OutOfScopePlace says where a refused key is. A key that a valid key of the
// view would be, in the namespace of the view, is outside the directory of the
// blobs of the call. Every other key is outside the namespace: another
// namespace, a parent segment, an empty key or a key that is not a valid key at
// all.
func OutOfScopePlace(v *blob.Scoped, key blob.Key) OutOfScopeWhere {
	if _, err := blob.ParseKey(key.String()); err == nil && strings.HasPrefix(key.String(), v.Prefix()) {
		return OutsideDirectory
	}

	return OutsideNamespace
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
func (b *ItemKeyBlobs) refuse(
	ctx context.Context, ns namespace.ID, op string, err error, where OutOfScopeWhere,
) error {
	if !errors.Is(err, blob.ErrOutOfScope) {
		return err
	}

	if recErr := b.refusals.RecordOutOfScope(ctx, ns, op, where); recErr != nil {
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
// kind security in the scope @server. Each package that reads blobs through a
// scoped view uses this recorder, with its own OutOfScopeFamily, so all
// refusals have one shape.
type AuditOutOfScope struct {
	entries audit.ServerStore
	channel audit.Channel
	family  OutOfScopeFamily
	now     func() time.Time
}

var _ OutOfScopeRecorder = (*AuditOutOfScope)(nil)

// ErrInvalidChannel reports a channel that an out-of-scope record cannot have.
var ErrInvalidChannel = errors.New("audit channel of a refusal is not api or console")

// ErrInvalidFamily reports an OutOfScopeFamily with a name that is not valid.
var ErrInvalidFamily = errors.New("family of an out-of-scope recorder is not valid")

// NewAuditOutOfScope returns an OutOfScopeRecorder that appends to entries. The
// caller gives the channel of its requests: audit.ChannelAPI or
// audit.ChannelConsole, and the family of its blobs. now may be nil; it then
// is time.Now.
func NewAuditOutOfScope(
	entries audit.ServerStore, channel audit.Channel, family OutOfScopeFamily, now func() time.Time,
) (*AuditOutOfScope, error) {
	if entries == nil {
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	}

	if channel != audit.ChannelAPI && channel != audit.ChannelConsole {
		return nil, fmt.Errorf("%w: %q", ErrInvalidChannel, channel)
	}

	if !validFamilyName(family.Blobs) || !validFamilyName(family.Dir) {
		return nil, fmt.Errorf("%w: %q, %q", ErrInvalidFamily, family.Blobs, family.Dir)
	}

	if now == nil {
		now = time.Now
	}

	return &AuditOutOfScope{entries: entries, channel: channel, family: family, now: now}, nil
}

// RecordOutOfScope appends the entry. The object is the ID of the namespace of
// the call, and the reason names op. The entry never holds the refused key.
func (a *AuditOutOfScope) RecordOutOfScope(
	ctx context.Context, ns namespace.ID, op string, where OutOfScopeWhere,
) error {
	switch op {
	case "put", "get", "delete", "list":
	default:
		return fmt.Errorf("%w: operation %q", ErrInvalidItemRef, op)
	}

	var place string

	switch where {
	case OutsideNamespace:
		place = "outside the namespace"
	case OutsideDirectory:
		place = "outside the " + a.family.Dir + " directory of the namespace"
	case OutsideUnitDirectory:
		place = "outside the unit directory"
	default:
		return fmt.Errorf("%w: place %q", ErrInvalidItemRef, where)
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
		Channel:       a.channel,
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
		Reason:        a.family.Blobs + " " + op + " refused: key " + place,
	}

	if _, err := a.entries.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}
