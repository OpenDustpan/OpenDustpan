package stack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// pulumiDir is the directory of the stack blobs inside the blob scope of a
// namespace: checkpoints and journal batches. The full keys are
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence> and
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/journal/<lowest
// sequence ID>.
const pulumiDir = "pulumi/"

// BlobFamily is the family of the Pulumi checkpoint and journal blobs in
// the reason of a refusal.
func BlobFamily() scopeaudit.Family {
	return scopeaudit.Family{Blobs: "stack blob", Dir: strings.TrimSuffix(pulumiDir, "/")}
}

// blobAccess builds the scoped blob view of one namespace for one call. It
// holds the blob store and the recorder of refusals, and no view.
type blobAccess struct {
	store    blob.Store
	refusals scopeaudit.Recorder
}

// newBlobAccess returns the access over store. Both arguments are required.
func newBlobAccess(store blob.Store, refusals scopeaudit.Recorder) (blobAccess, error) {
	if store == nil {
		return blobAccess{}, fmt.Errorf("%w: blob store", ErrMissingDependency)
	}

	if refusals == nil {
		return blobAccess{}, fmt.Errorf("%w: out-of-scope recorder", ErrMissingDependency)
	}

	return blobAccess{store: store, refusals: refusals}, nil
}

// scope returns the blob access of the stack stackID of the namespace ns for
// one call. The caller takes ns from the namespace row that it resolved and
// authorized, and stackID from the stack row, never from a request field or
// from a row that names a blob.
func (a blobAccess) scope(ns namespace.ID, stackID ID) (*scopedBlobs, error) {
	view, err := blob.NewScoped(a.store, ns)
	if err != nil {
		return nil, fmt.Errorf("scope of stack blobs: %w", err)
	}

	// The ID of the stack is one segment of the key, so it must hold no
	// separator and no empty value: else the directory of the stack could
	// widen to the directory of the family or to another stack.
	if _, keyErr := blob.ParseKey(view.Prefix() + pulumiDir + stackID.String()); keyErr != nil ||
		strings.Contains(stackID.String(), "/") {
		return nil, fmt.Errorf("scope of stack blobs: %w: stack ID", ErrInvalidScope)
	}

	b := &scopedBlobs{view: view, refusals: a.refusals, ns: ns, stack: stackID, allowed: ""}
	b.allowed = b.stackPrefix(stackID)

	return b, nil
}

// ErrInvalidScope reports an ID that cannot make the scope of the blobs of a
// stack.
var ErrInvalidScope = errors.New("invalid scope of stack blobs")

// scopedBlobs is the blob store of the blobs of one stack of one namespace. It
// refuses each key outside namespaces/<namespace ID>/pulumi/<stack ID>/, the
// directory of the stack, with blob.ErrOutOfScope, before any call to the
// store, and writes one security entry to the audit log for each refusal. A
// key in the directory of another stack of the same namespace is a refusal
// too. It implements blob.Store.
type scopedBlobs struct {
	view     *blob.Scoped
	refusals scopeaudit.Recorder
	ns       namespace.ID
	stack    ID
	// allowed is the directory that the calls may use, with the trailing
	// slash: the directory of the stack, or a directory inside it after
	// narrow.
	allowed string
}

// Key returns the key of rel, which is relative to the stack blob directory.
func (b *scopedBlobs) Key(rel string) (blob.Key, error) {
	key, err := b.view.Key(pulumiDir + rel)
	if err != nil {
		return "", fmt.Errorf("stack blob key: %w", err)
	}

	return key, nil
}

// Put implements blob.Store. It returns blob.ErrOutOfScope when key is not a
// key of the directory of the stack.
func (b *scopedBlobs) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := b.check(ctx, "put", key); err != nil {
		return err
	}

	err := b.view.Put(ctx, key, r)

	return b.refuse(ctx, "put", err)
}

// Get implements blob.Store. It returns blob.ErrOutOfScope when key is not a
// key of the directory of the stack.
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
// a key of the directory of the stack.
func (b *scopedBlobs) Delete(ctx context.Context, key blob.Key) error {
	if err := b.check(ctx, "delete", key); err != nil {
		return err
	}

	err := b.view.Delete(ctx, key)

	return b.refuse(ctx, "delete", err)
}

// ListPage implements blob.Store. It returns blob.ErrOutOfScope when the
// prefix is not a directory inside the directory of the stack, and when the
// store returns a key outside that directory. In the second case it returns no
// key, so a caller that deletes what it lists deletes nothing.
//
// Defense in depth: the store lists by prefix. This check stays so that a
// fault of an adapter that lists more than the prefix never reaches the delete
// of a sweep, and it does not rely on the adapter.
func (b *scopedBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	dir := strings.TrimSuffix(req.Prefix, "/") + "/"

	if !strings.HasPrefix(dir, b.allowed) {
		return blob.Page{}, b.refusePrefix(ctx, req.Prefix)
	}

	page, err := b.view.ListPage(ctx, req)
	if err != nil {
		return blob.Page{}, b.refuse(ctx, "list", err)
	}

	for i := range page.Objects {
		if !strings.HasPrefix(page.Objects[i].Key.String(), b.allowed) {
			return blob.Page{}, b.record(ctx, "list", b.whereOf(page.Objects[i].Key))
		}
	}

	return page, nil
}

// Ping implements blob.Store. It takes no key, so the scope does not apply.
func (b *scopedBlobs) Ping(ctx context.Context) error {
	if err := b.view.Ping(ctx); err != nil {
		return fmt.Errorf("ping stack blobs: %w", err)
	}

	return nil
}

// listAll walks every page under prefix and returns the objects. A caller
// that needs the whole listing of one update or one stack uses it, and a
// caller that can act on each object as it arrives uses blob.Walk.
func (b *scopedBlobs) listAll(ctx context.Context, prefix string) ([]blob.Object, error) {
	var out []blob.Object

	err := blob.Walk(ctx, b, prefix, func(o blob.Object) error {
		out = append(out, o)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// narrow returns the access that is limited to the directory prefix inside the
// directory of the stack, for example the journal directory of one update.
// A prefix outside the directory of the stack gives an access that refuses
// every key.
func (b *scopedBlobs) narrow(prefix string) *scopedBlobs {
	narrowed := *b

	if strings.HasPrefix(prefix, b.allowed) {
		narrowed.allowed = prefix
	} else {
		// Fail closed: a directory that cannot hold a key.
		narrowed.allowed = b.allowed + "\x00/"
	}

	return &narrowed
}

var _ blob.Store = (*scopedBlobs)(nil)

// dir returns the prefix of the stack blobs of the namespace, with the trailing
// slash: the directory of all stacks.
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

// check refuses key when it is not a key of the directory of the stack.
//
// Defense in depth: the view refuses a key outside the namespace too. This
// check stays so that a key of another kind of blob in the same namespace,
// for example the key of an OpenTofu state version in a row, and a key of
// another stack in the same namespace never reach the stack code, and it does
// not rely on the view, on the check of the row store or on the check at the
// write of a row.
func (b *scopedBlobs) check(ctx context.Context, op string, key blob.Key) error {
	if strings.HasPrefix(key.String(), b.allowed) {
		return nil
	}

	return b.record(ctx, op, b.whereOf(key))
}

// refusePrefix refuses the listing of a prefix outside the directory of the
// stack.
func (b *scopedBlobs) refusePrefix(ctx context.Context, prefix string) error {
	return b.record(ctx, "list", b.placeOf(strings.TrimSuffix(prefix, "/")+"/", true))
}

// whereOf says where the refused key is. See placeOf.
func (b *scopedBlobs) whereOf(key blob.Key) scopeaudit.Where {
	_, err := blob.ParseKey(key.String())

	return b.placeOf(key.String(), err == nil)
}

// placeOf says where a refused key or prefix is. A valid one in the stack
// blob directory of the namespace, which is outside the directory of the
// stack of the call, is in another stack of the same namespace or in a
// directory of the stack that the call does not use: the unit directory. A
// valid one in the namespace and outside the stack blob directory is in
// another kind of blob, for example the key of an OpenTofu state version: the
// directory. Every other one is outside the namespace: another namespace, a
// parent segment, an empty key or a key that is not a valid key at all.
func (b *scopedBlobs) placeOf(name string, valid bool) scopeaudit.Where {
	switch {
	case !valid || !strings.HasPrefix(name, b.view.Prefix()):
		return scopeaudit.OutsideNamespace
	case strings.HasPrefix(name, b.dir()):
		return scopeaudit.OutsideUnitDirectory
	default:
		return scopeaudit.OutsideDirectory
	}
}

// refuse writes the refusal to the recorder when err is blob.ErrOutOfScope,
// which the view returns for a key outside the namespace. Any other error
// passes through.
func (b *scopedBlobs) refuse(ctx context.Context, op string, err error) error {
	if !errors.Is(err, blob.ErrOutOfScope) {
		return err
	}

	return b.record(ctx, op, scopeaudit.OutsideNamespace)
}

// record writes one refusal to the recorder and returns blob.ErrOutOfScope,
// joined with the error of the recorder when the record fails, so the caller
// still sees the refusal.
func (b *scopedBlobs) record(ctx context.Context, op string, where scopeaudit.Where) error {
	if recErr := b.refusals.RecordOutOfScope(ctx, b.ns, op, where); recErr != nil {
		return errors.Join(blob.ErrOutOfScope, fmt.Errorf("record the refusal: %w", recErr))
	}

	return blob.ErrOutOfScope
}

// CheckCheckpointBlobKey returns nil when key is the blob key of a checkpoint
// save of the update in the stack stackID of the namespace ns:
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<number>. Otherwise
// it returns ErrCheckpointKey. The row stores call it before they write a
// checkpoint row, so a row never names a blob of another stack, of another
// update, of the journal, or outside the scope of a namespace. The error never
// holds the key.
//
// Defense in depth: the service builds the key in the scoped view of the stack
// of the call. This check stays so that a caller of the row store that builds
// a key by other means cannot store a row that names a foreign blob, and it
// does not rely on the service.
func CheckCheckpointBlobKey(ns namespace.ID, stackID ID, update UpdateID, key blob.Key) error {
	if _, err := namespace.ParseID(ns.String()); err != nil {
		return fmt.Errorf("%w: namespace ID", ErrCheckpointKey)
	}

	want := "namespaces/" + ns.String() + "/" + pulumiDir + stackID.String() + "/" + update.String() + "/"

	num, ok := strings.CutPrefix(key.String(), want)
	if !ok || num == "" || strings.Contains(num, "/") {
		return fmt.Errorf("%w: key is not a save of the update in the namespace", ErrCheckpointKey)
	}

	if _, err := strconv.ParseUint(num, 10, 63); err != nil {
		return fmt.Errorf("%w: key does not end in a number", ErrCheckpointKey)
	}

	if _, err := blob.ParseKey(key.String()); err != nil {
		return fmt.Errorf("%w: key is not valid", ErrCheckpointKey)
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
