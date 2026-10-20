package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// stateBlobDir is the directory, inside the scope of one namespace, of the
// blob keys of the state content of OpenTofu workspaces.
const stateBlobDir = "tofu/"

// Operations of the state blob code, as the recorder of a refusal names them.
const (
	opPut    = "put"
	opGet    = "get"
	opDelete = "delete"
	opList   = "list"
)

// StateBlobFamily is the family of the state content blobs, as the audit entry
// of a refusal names it.
func StateBlobFamily() secret.OutOfScopeFamily {
	return secret.OutOfScopeFamily{Blobs: "state blob", Dir: strings.TrimSuffix(stateBlobDir, "/")}
}

// stateBlobs reads and writes the state content blobs. It builds a
// *blob.Scoped for each call, from the namespace ID of the workspace row that
// the caller resolved and authorized, never from a request field. It reads
// no namespace: the ID comes from Workspace.NamespaceID. Each blob key is
// namespaces/<namespace ID>/tofu/<workspace ID>/<state version ID>/<attempt>.
//
// A write or a delete accepts a key only inside the directory of the
// workspace, namespaces/<namespace ID>/tofu/<workspace ID>/. A key of another
// namespace, of another kind in the namespace, or of another workspace of the
// namespace returns blob.ErrOutOfScope before any call to the store, and
// writes one refusal to the secret.OutOfScopeRecorder.
type stateBlobs struct {
	store    blob.Store
	refusals secret.OutOfScopeRecorder
}

// view returns the view of the store for the namespace with ID ns.
func (b *stateBlobs) view(ns namespace.ID) (*blob.Scoped, error) {
	v, err := blob.NewScoped(b.store, ns)
	if err != nil {
		return nil, fmt.Errorf("scope of state blobs: %w", err)
	}

	return v, nil
}

// attemptKey returns a fresh blob key for one upload attempt of the state
// version sv of the workspace ws, inside the namespace with ID ns. IDs are
// lowercase, so the key is valid whatever the namespace is called.
func (b *stateBlobs) attemptKey(ns namespace.ID, ws ID, sv StateVersionID) (blob.Key, error) {
	v, err := b.view(ns)
	if err != nil {
		return "", err
	}

	key, err := v.Key(stateBlobDir + ws.String() + "/" + sv.String() + "/" + ident.NewID(""))
	if err != nil {
		return "", fmt.Errorf("state content key: %w", err)
	}

	return key, nil
}

// put stores r under key, for the workspace ws of the namespace with ID ns.
// It returns blob.ErrOutOfScope, and does not read r, when key is outside the
// directory of that workspace.
func (b *stateBlobs) put(ctx context.Context, ns namespace.ID, ws ID, key blob.Key, r io.Reader) error {
	v, err := b.scopedKey(ctx, ns, ws, key, opPut)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ns, opPut, v.Put(ctx, key, r), secret.OutOfScopePlace(v, key))
}

// get opens the blob under key, for the workspace ws of the namespace with ID
// ns. It returns blob.ErrOutOfScope when key is outside the directory of that
// workspace.
func (b *stateBlobs) get(ctx context.Context, ns namespace.ID, ws ID, key blob.Key) (io.ReadCloser, error) {
	v, err := b.scopedKey(ctx, ns, ws, key, opGet)
	if err != nil {
		return nil, err
	}

	r, err := v.Get(ctx, key)
	if err != nil {
		return nil, b.refuse(ctx, ns, opGet, err, secret.OutOfScopePlace(v, key))
	}

	return r, nil
}

// delete removes the blob under key, for the workspace ws of the namespace
// with ID ns. A missing blob is not an error. It returns blob.ErrOutOfScope
// when key is outside the directory of that workspace.
func (b *stateBlobs) delete(ctx context.Context, ns namespace.ID, ws ID, key blob.Key) error {
	v, err := b.scopedKey(ctx, ns, ws, key, opDelete)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ns, opDelete, v.Delete(ctx, key), secret.OutOfScopePlace(v, key))
}

// deleteRetried is delete with a retry of a transient failure after a short
// backoff (blob.DeleteRetried). A refusal is not retried.
func (b *stateBlobs) deleteRetried(ctx context.Context, ns namespace.ID, ws ID, key blob.Key) error {
	v, err := b.scopedKey(ctx, ns, ws, key, opDelete)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ns, opDelete, blob.DeleteRetried(ctx, v, key), secret.OutOfScopePlace(v, key))
}

// list returns the blobs of the workspace ws, in the namespace with ID ns.
func (b *stateBlobs) list(ctx context.Context, ns namespace.ID, ws ID) ([]blob.Object, error) {
	v, err := b.view(ns)
	if err != nil {
		return nil, err
	}

	objects, err := v.List(ctx, workspaceDir(v, ws))
	if err != nil {
		return nil, b.refuse(ctx, ns, opList, fmt.Errorf("list state blobs: %w", err), secret.OutsideNamespace)
	}

	return objects, nil
}

// workspaceDir returns the directory of the state blobs of the workspace ws in
// the view v: namespaces/<namespace ID>/tofu/<workspace ID>/.
func workspaceDir(v *blob.Scoped, ws ID) string {
	return v.Prefix() + stateBlobDir + ws.String() + "/"
}

// scopedKey returns the view of the namespace with ID ns after it checks that
// key is inside the directory of the workspace ws. A key of another namespace,
// of another directory of the namespace, or of another workspace is a refusal.
//
// The shape of the rest of the key is not checked here: a purge and a sweep
// delete each blob that a listing of the workspace directory returns, also a
// blob of an unexpected shape, so no blob of a purged workspace stays. The
// view checks that the whole key is valid.
//
// Defense in depth: the view refuses a key outside the namespace too, and the
// callers take the key from the row of the workspace. This check stays so
// that a row of one workspace never names, and so never deletes, a blob of
// another workspace of the same namespace, or a blob of another kind such as
// a checkpoint. It does not rely on the view or on the row.
func (b *stateBlobs) scopedKey(
	ctx context.Context, ns namespace.ID, ws ID, key blob.Key, op string,
) (*blob.Scoped, error) {
	v, err := b.view(ns)
	if err != nil {
		return nil, err
	}

	if ws == "" || !strings.HasPrefix(key.String(), workspaceDir(v, ws)) {
		return nil, b.refuse(ctx, ns, op, blob.ErrOutOfScope, secret.OutOfScopePlace(v, key))
	}

	return v, nil
}

// validStateRest reports whether rest, the part of a key after the state
// directory, is <workspace ID>/<state version ID>/<attempt>.
func validStateRest(rest string) bool {
	parts := strings.Split(rest, "/")
	if len(parts) != stateKeyRestParts {
		return false
	}

	return !slices.Contains(parts, "")
}

// stateKeyRestParts is the number of parts of a state content key after the
// state directory: the workspace ID, the state version ID and the attempt.
const stateKeyRestParts = 3

// refuse writes the refusal to the recorder when err is blob.ErrOutOfScope.
// It returns blob.ErrOutOfScope, joined with the error of the recorder when
// the record fails, so the caller still sees the refusal. Any other error, and
// nil, pass through.
func (b *stateBlobs) refuse(
	ctx context.Context, ns namespace.ID, op string, err error, where secret.OutOfScopeWhere,
) error {
	if !errors.Is(err, blob.ErrOutOfScope) {
		return err
	}

	// The record must survive a canceled request: the refusal shows a fault or
	// an attack, so a client that disconnects must not erase it.
	recCtx, cancel := cleanupContext(ctx)
	defer cancel()

	if recErr := b.refusals.RecordOutOfScope(recCtx, ns, op, where); recErr != nil {
		return errors.Join(blob.ErrOutOfScope, fmt.Errorf("record the refusal: %w", recErr))
	}

	return blob.ErrOutOfScope
}

// StateBlobKey names the parts of one state content blob key.
type StateBlobKey struct {
	Namespace namespace.ID
	Workspace ID
	Version   StateVersionID
	Attempt   string
}

// ParseStateBlobKey splits a blob key of the form
// namespaces/<namespace ID>/tofu/<workspace ID>/<state version ID>/<attempt>
// into its parts. It returns ErrStateVersionNotFound for any other key. The
// namespace ID of the result is only the claim of the key: a caller must take
// the namespace of a call from the row it resolved, and compare.
func ParseStateBlobKey(key blob.Key) (StateBlobKey, error) {
	parts := strings.Split(key.String(), "/")
	if len(parts) != stateBlobKeyParts || parts[0] != "namespaces" ||
		parts[2] != strings.TrimSuffix(stateBlobDir, "/") {
		return StateBlobKey{}, fmt.Errorf("%w: not a state content key", ErrStateVersionNotFound)
	}

	ns, err := namespace.ParseID(parts[1])
	if err != nil {
		return StateBlobKey{}, fmt.Errorf("%w: namespace ID of the key", ErrStateVersionNotFound)
	}

	if !validStateRest(strings.Join(parts[3:], "/")) {
		return StateBlobKey{}, fmt.Errorf("%w: not a state content key", ErrStateVersionNotFound)
	}

	return StateBlobKey{
		Namespace: ns,
		Workspace: ID(parts[3]),
		Version:   StateVersionID(parts[4]),
		Attempt:   parts[5],
	}, nil
}

// stateBlobKeyParts is the number of parts of a whole state content key:
// "namespaces", the namespace ID, "tofu", then the three parts of the rest.
const stateBlobKeyParts = 3 + stateKeyRestParts
