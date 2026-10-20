package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// errUnknownBlobOp reports an operation name that the recorder does not know.
var errUnknownBlobOp = errors.New("unknown blob operation")

// OutOfScopeRecorder records that a call tried to use a blob key outside the
// namespace of the call. A recorder never holds the key of the call: the key
// can name another namespace, and it can hold text that an attacker chose.
type OutOfScopeRecorder interface {
	// RecordOutOfScope records one refusal in the namespace ns, for the
	// operation op ("put", "get", "delete" or "list"). It returns an error
	// when it cannot record the refusal.
	RecordOutOfScope(ctx context.Context, ns namespace.ID, op string) error
}

// stateBlobs reads and writes the state content blobs. It builds a
// *blob.Scoped for each call, from the namespace ID of the workspace row that
// the caller resolved and authorized, never from a request field. Each blob
// key is namespaces/<namespace ID>/tofu/<workspace ID>/<state version ID>/<attempt>.
//
// A call with a key of another namespace returns blob.ErrOutOfScope before
// any call to the store, and writes one refusal to the OutOfScopeRecorder.
type stateBlobs struct {
	store      blob.Store
	namespaces *namespace.Service
	refusals   OutOfScopeRecorder
}

// view returns the view of the store for the namespace called name, with the
// ID of that namespace. The ID comes from the namespace record, so a caller
// never chooses it.
func (b *stateBlobs) view(ctx context.Context, name namespace.Name) (*blob.Scoped, namespace.ID, error) {
	ns, err := b.namespaces.Get(ctx, name)
	if err != nil {
		return nil, "", fmt.Errorf("scope of state blobs: %w", err)
	}

	v, err := blob.NewScoped(b.store, ns.ID)
	if err != nil {
		return nil, "", fmt.Errorf("scope of state blobs: %w", err)
	}

	return v, ns.ID, nil
}

// attemptKey returns a fresh blob key for one upload attempt of the state
// version sv of the workspace ws, inside the namespace called name. IDs are
// lowercase, so the key is valid whatever the namespace is called.
func (b *stateBlobs) attemptKey(
	ctx context.Context, name namespace.Name, ws ID, sv StateVersionID,
) (blob.Key, error) {
	v, _, err := b.view(ctx, name)
	if err != nil {
		return "", err
	}

	key, err := v.Key(stateBlobDir + ws.String() + "/" + sv.String() + "/" + ident.NewID(""))
	if err != nil {
		return "", fmt.Errorf("state content key: %w", err)
	}

	return key, nil
}

// put stores r under key, in the namespace called name. It returns
// blob.ErrOutOfScope, and does not read r, when key is outside the state
// directory of that namespace.
func (b *stateBlobs) put(ctx context.Context, name namespace.Name, key blob.Key, r io.Reader) error {
	v, id, err := b.scopedKey(ctx, name, key, opPut)
	if err != nil {
		return err
	}

	return b.refuse(ctx, id, opPut, v.Put(ctx, key, r))
}

// get opens the blob under key, in the namespace called name. It returns
// blob.ErrOutOfScope when key is outside the state directory of that
// namespace.
func (b *stateBlobs) get(ctx context.Context, name namespace.Name, key blob.Key) (io.ReadCloser, error) {
	v, id, err := b.scopedKey(ctx, name, key, opGet)
	if err != nil {
		return nil, err
	}

	r, err := v.Get(ctx, key)
	if err != nil {
		return nil, b.refuse(ctx, id, opGet, err)
	}

	return r, nil
}

// delete removes the blob under key, in the namespace called name. A missing
// blob is not an error. It returns blob.ErrOutOfScope when key is outside the
// state directory of that namespace.
func (b *stateBlobs) delete(ctx context.Context, name namespace.Name, key blob.Key) error {
	v, id, err := b.scopedKey(ctx, name, key, opDelete)
	if err != nil {
		return err
	}

	return b.refuse(ctx, id, opDelete, v.Delete(ctx, key))
}

// deleteRetried is delete with a retry of a transient failure after a short
// backoff (blob.DeleteRetried). A refusal is not retried.
func (b *stateBlobs) deleteRetried(ctx context.Context, name namespace.Name, key blob.Key) error {
	v, id, err := b.scopedKey(ctx, name, key, opDelete)
	if err != nil {
		return err
	}

	return b.refuse(ctx, id, opDelete, blob.DeleteRetried(ctx, v, key))
}

// list returns the blobs of the workspace ws, in the namespace called name.
func (b *stateBlobs) list(ctx context.Context, name namespace.Name, ws ID) ([]blob.Object, error) {
	v, id, err := b.view(ctx, name)
	if err != nil {
		return nil, err
	}

	objects, err := v.List(ctx, v.Prefix()+stateBlobDir+ws.String()+"/")
	if err != nil {
		return nil, b.refuse(ctx, id, opList, fmt.Errorf("list state blobs: %w", err))
	}

	return objects, nil
}

// scopedKey returns the view of the namespace called name after it checks
// that key is inside the state directory of that namespace. A key of another
// namespace, or a key of another directory of the namespace, is a refusal.
//
// The shape of the rest of the key is not checked here: a purge and a sweep
// delete each blob that a listing of the workspace directory returns, also a
// blob of an unexpected shape, so no blob of a purged workspace stays. The
// view checks that the whole key is valid.
//
// Defense in depth: the view refuses a key outside the namespace too. This
// check stays so that a key of a blob of another kind in the same namespace,
// for example a checkpoint, never reaches the state code, and it does not
// rely on the view.
func (b *stateBlobs) scopedKey(
	ctx context.Context, name namespace.Name, key blob.Key, op string,
) (*blob.Scoped, namespace.ID, error) {
	v, id, err := b.view(ctx, name)
	if err != nil {
		return nil, "", err
	}

	if !strings.HasPrefix(key.String(), v.Prefix()+stateBlobDir) {
		return nil, "", b.refuse(ctx, id, op, blob.ErrOutOfScope)
	}

	return v, id, nil
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
func (b *stateBlobs) refuse(ctx context.Context, ns namespace.ID, op string, err error) error {
	if !errors.Is(err, blob.ErrOutOfScope) {
		return err
	}

	// The record must survive a canceled request: the refusal shows a fault or
	// an attack, so a client that disconnects must not erase it.
	recCtx, cancel := cleanupContext(ctx)
	defer cancel()

	if recErr := b.refusals.RecordOutOfScope(recCtx, ns, op); recErr != nil {
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

// Fields of the audit entry of a refusal.
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
	case opPut, opGet, opDelete, opList:
	default:
		return fmt.Errorf("record out-of-scope refusal: %w %q", errUnknownBlobOp, op)
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
		Reason:        "state blob " + op + " refused: key outside the namespace",
	}

	if _, err := a.entries.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}
