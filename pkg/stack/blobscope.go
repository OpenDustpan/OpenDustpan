package stack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// pulumiDir is the directory of the stack blobs inside the blob scope of a
// namespace: checkpoints and journal batches. The full keys are
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence> and
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/journal/<lowest
// sequence ID>.
const pulumiDir = "pulumi/"

// OutOfScopeRecorder records that a call tried to use a blob key outside the
// blob scope of the namespace of the call. A recorder never holds the key of
// the call: the key can name another namespace, and it can hold text that an
// attacker chose.
type OutOfScopeRecorder interface {
	// RecordOutOfScope records one refusal in the namespace ns, for the
	// operation op ("put", "get", "delete" or "list"). Where says how far the
	// key was out of scope. It returns an error when it cannot record the
	// refusal.
	RecordOutOfScope(ctx context.Context, ns namespace.ID, op string, where OutOfScopeWhere) error
}

// OutOfScopeWhere says where a refused key was: outside the namespace of the
// call, or inside the namespace and outside its stack blob directory.
type OutOfScopeWhere string

// The places of a refused key.
const (
	// OutsideNamespace is a key that is not inside the namespace of the call.
	OutsideNamespace OutOfScopeWhere = "namespace"
	// OutsideStackBlobs is a key inside the namespace of the call and outside
	// its stack blob directory, for example the key of an OpenTofu state
	// version.
	OutsideStackBlobs OutOfScopeWhere = "stack-blobs"
)

// blobAccess builds the scoped blob view of one namespace for one call. It
// holds the blob store and the recorder of refusals, and no view.
type blobAccess struct {
	store    blob.Store
	refusals OutOfScopeRecorder
}

// newBlobAccess returns the access over store. Both arguments are required.
func newBlobAccess(store blob.Store, refusals OutOfScopeRecorder) (blobAccess, error) {
	if store == nil {
		return blobAccess{}, fmt.Errorf("%w: blob store", ErrMissingDependency)
	}

	if refusals == nil {
		return blobAccess{}, fmt.Errorf("%w: out-of-scope recorder", ErrMissingDependency)
	}

	return blobAccess{store: store, refusals: refusals}, nil
}

// scope returns the blob access of the namespace ns for one call. The caller
// takes ns from the namespace row that it resolved and authorized, never from
// a request field.
func (a blobAccess) scope(ns namespace.ID) (*scopedBlobs, error) {
	view, err := blob.NewScoped(a.store, ns)
	if err != nil {
		return nil, fmt.Errorf("scope of stack blobs: %w", err)
	}

	return &scopedBlobs{view: view, ns: ns, refusals: a.refusals}, nil
}

// scopedBlobs is the blob store of the stack blobs of one namespace. It
// refuses each key outside namespaces/<namespace ID>/pulumi/ with
// blob.ErrOutOfScope, before any call to the store, and writes one security
// entry to the audit log for each refusal. It implements blob.Store.
type scopedBlobs struct {
	view     *blob.Scoped
	refusals OutOfScopeRecorder
	ns       namespace.ID
}

var _ blob.Store = (*scopedBlobs)(nil)

// Key returns the key of rel, which is relative to the stack blob directory.
func (b *scopedBlobs) Key(rel string) (blob.Key, error) {
	key, err := b.view.Key(pulumiDir + rel)
	if err != nil {
		return "", fmt.Errorf("stack blob key: %w", err)
	}

	return key, nil
}

// Put implements blob.Store. It returns blob.ErrOutOfScope when key is not a
// key of the stack blob directory of the namespace.
func (b *scopedBlobs) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := b.check(ctx, "put", key); err != nil {
		return err
	}

	err := b.view.Put(ctx, key, r)

	return b.refuse(ctx, "put", err)
}

// Get implements blob.Store. It returns blob.ErrOutOfScope when key is not a
// key of the stack blob directory of the namespace.
func (b *scopedBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if err := b.check(ctx, "get", key); err != nil {
		return nil, err
	}

	rc, err := b.view.Get(ctx, key)
	if err != nil {
		return nil, b.refuse(ctx, "get", err)
	}

	return rc, nil
}

// Delete implements blob.Store. It returns blob.ErrOutOfScope when key is not
// a key of the stack blob directory of the namespace.
func (b *scopedBlobs) Delete(ctx context.Context, key blob.Key) error {
	if err := b.check(ctx, "delete", key); err != nil {
		return err
	}

	err := b.view.Delete(ctx, key)

	return b.refuse(ctx, "delete", err)
}

// List implements blob.Store. It returns blob.ErrOutOfScope when prefix is not
// a directory inside the stack blob directory of the namespace.
func (b *scopedBlobs) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	dir := strings.TrimSuffix(prefix, "/") + "/"

	if !strings.HasPrefix(dir, b.dir()) {
		return nil, b.refusePrefix(ctx, prefix)
	}

	objects, err := b.view.List(ctx, prefix)
	if err != nil {
		return nil, b.refuse(ctx, "list", err)
	}

	return objects, nil
}

// Ping implements blob.Store. It takes no key, so the scope does not apply.
func (b *scopedBlobs) Ping(ctx context.Context) error {
	if err := b.view.Ping(ctx); err != nil {
		return fmt.Errorf("ping stack blobs: %w", err)
	}

	return nil
}

// dir returns the prefix of the stack blobs of the scope, with the trailing
// slash.
func (b *scopedBlobs) dir() string {
	return b.view.Prefix() + pulumiDir
}

// stackPrefix returns the prefix of all blobs of the stack stackID.
func (b *scopedBlobs) stackPrefix(stackID ID) string {
	return b.dir() + stackID.String() + "/"
}

// updatePrefix returns the prefix of the checkpoint blobs of the update id.
func (b *scopedBlobs) updatePrefix(stackID ID, id UpdateID) string {
	return b.stackPrefix(stackID) + id.String() + "/"
}

// journalPrefix returns the prefix of the journal blobs of the update id.
func (b *scopedBlobs) journalPrefix(stackID ID, id UpdateID) string {
	return b.updatePrefix(stackID, id) + "journal/"
}

// check refuses key when it is not a key of the stack blob directory of the
// namespace.
//
// Defense in depth: the view refuses a key outside the namespace too. This
// check stays so that a key of another kind of blob in the same namespace,
// for example the key of an OpenTofu state version in a row, never reaches
// the stack code, and it does not rely on the view.
func (b *scopedBlobs) check(ctx context.Context, op string, key blob.Key) error {
	if strings.HasPrefix(key.String(), b.dir()) {
		return nil
	}

	return b.record(ctx, op, b.whereOf(key))
}

// refusePrefix refuses the listing of a prefix outside the stack blob
// directory.
func (b *scopedBlobs) refusePrefix(ctx context.Context, prefix string) error {
	where := OutsideNamespace
	if strings.HasPrefix(strings.TrimSuffix(prefix, "/")+"/", b.view.Prefix()) {
		where = OutsideStackBlobs
	}

	return b.record(ctx, "list", where)
}

// whereOf says where the refused key is. A key that a valid key of the view
// would be, in the namespace of the view, is outside the stack blob
// directory. Every other key is outside the namespace: another namespace, a
// parent segment, an empty key or a key that is not a valid key at all.
func (b *scopedBlobs) whereOf(key blob.Key) OutOfScopeWhere {
	if _, err := blob.ParseKey(key.String()); err == nil && strings.HasPrefix(key.String(), b.view.Prefix()) {
		return OutsideStackBlobs
	}

	return OutsideNamespace
}

// refuse writes the refusal to the recorder when err is blob.ErrOutOfScope,
// which the view returns for a key outside the namespace. Any other error
// passes through.
func (b *scopedBlobs) refuse(ctx context.Context, op string, err error) error {
	if !errors.Is(err, blob.ErrOutOfScope) {
		return err
	}

	return b.record(ctx, op, OutsideNamespace)
}

// record writes one refusal to the recorder and returns blob.ErrOutOfScope,
// joined with the error of the recorder when the record fails, so the caller
// still sees the refusal.
func (b *scopedBlobs) record(ctx context.Context, op string, where OutOfScopeWhere) error {
	if recErr := b.refusals.RecordOutOfScope(ctx, b.ns, op, where); recErr != nil {
		return errors.Join(blob.ErrOutOfScope, fmt.Errorf("record the refusal: %w", recErr))
	}

	return blob.ErrOutOfScope
}

// Actions and fields of the audit entry of a refusal.
const (
	// ActionBlobOutOfScope is the action of the audit entry of a refusal.
	ActionBlobOutOfScope = "blob:out-of-scope"
	// outOfScopeSubject is the subject of that entry: the server itself.
	outOfScopeSubject = "server"
)

// ErrInvalidChannel reports a channel that an out-of-scope record cannot have.
var ErrInvalidChannel = errors.New("audit channel of a refusal is not api or console")

// ErrInvalidRefusal reports an out-of-scope record with an operation, a place
// or a namespace ID that the entry cannot hold.
var ErrInvalidRefusal = errors.New("invalid out-of-scope refusal")

// AuditOutOfScope is an OutOfScopeRecorder that writes one audit entry of the
// kind security in the scope @server.
type AuditOutOfScope struct {
	entries audit.ServerStore
	channel audit.Channel
	now     func() time.Time
}

var _ OutOfScopeRecorder = (*AuditOutOfScope)(nil)

// NewAuditOutOfScope returns an OutOfScopeRecorder that appends to entries.
// The caller gives the channel of its requests: audit.ChannelAPI or
// audit.ChannelConsole. now may be nil; it then is time.Now.
func NewAuditOutOfScope(
	entries audit.ServerStore, channel audit.Channel, now func() time.Time,
) (*AuditOutOfScope, error) {
	if entries == nil {
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	}

	if channel != audit.ChannelAPI && channel != audit.ChannelConsole {
		return nil, fmt.Errorf("%w: %q", ErrInvalidChannel, channel)
	}

	if now == nil {
		now = time.Now
	}

	return &AuditOutOfScope{entries: entries, channel: channel, now: now}, nil
}

// RecordOutOfScope appends the entry. The object is the ID of the namespace of
// the call, and the reason names op. The entry never holds the refused key.
func (a *AuditOutOfScope) RecordOutOfScope(
	ctx context.Context, ns namespace.ID, op string, where OutOfScopeWhere,
) error {
	switch op {
	case "put", "get", "delete", "list":
	default:
		return fmt.Errorf("%w: operation %q", ErrInvalidRefusal, op)
	}

	var place string

	switch where {
	case OutsideNamespace:
		place = "outside the namespace"
	case OutsideStackBlobs:
		place = "outside the stack blob directory of the namespace"
	default:
		return fmt.Errorf("%w: place %q", ErrInvalidRefusal, where)
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
		Reason:        "stack blob " + op + " refused: key " + place,
	}

	if _, err := a.entries.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// CheckJournalBlobKey returns nil when key is the blob key of a journal batch
// of the update in the stack stackID of the namespace ns:
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/journal/<lowest
// sequence ID>. Otherwise it returns ErrJournalKey. The row stores call it
// before they write a row, so a row never names a blob of another stack, of
// another update, or outside the scope of a namespace. The error never holds
// the key.
//
// Defense in depth: the writer builds the key in the scoped view of the
// namespace of the call. This check stays so that a caller of the row store
// that builds a key by other means cannot store a row that names a foreign
// blob, and it does not rely on the writer.
func CheckJournalBlobKey(ns namespace.ID, stackID ID, update UpdateID, key blob.Key) error {
	if _, err := namespace.ParseID(ns.String()); err != nil {
		return fmt.Errorf("%w: namespace ID", ErrJournalKey)
	}

	want := "namespaces/" + ns.String() + "/" + pulumiDir + stackID.String() + "/" + update.String() + "/journal/"

	low, ok := strings.CutPrefix(key.String(), want)
	if !ok || low == "" || strings.Contains(low, "/") {
		return fmt.Errorf("%w: key is not a journal batch of the update in the namespace", ErrJournalKey)
	}

	if _, err := blob.ParseKey(key.String()); err != nil {
		return fmt.Errorf("%w: key is not valid", ErrJournalKey)
	}

	return nil
}
