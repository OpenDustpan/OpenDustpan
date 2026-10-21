package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/scopeaudit"
)

// artifactDir is the directory, inside the scope of one namespace, of the
// blob keys of plans and logs.
const artifactDir = "job"

// Operations of the artifact blob code, as the recorder of a refusal names
// them.
const (
	opPut    = "put"
	opGet    = "get"
	opDelete = "delete"
)

// ArtifactBlobFamily is the family of the plan and log blobs, as the audit
// entry of a refusal names it.
func ArtifactBlobFamily() scopeaudit.Family {
	return scopeaudit.Family{Blobs: "job artifact blob", Dir: artifactDir}
}

// ArtifactBlobs reads and writes the sealed content of plans and logs in the
// blob store. It builds a *blob.Scoped for each call, from the namespace ID
// that the caller took from the row that it resolved and authorized, never
// from a request field. Each key is
// namespaces/<namespace ID>/job/<job ID>/plan/<plan ID>/<part> or
// namespaces/<namespace ID>/job/<job ID>/log/<version ID>/<chunk>, built from
// PlanPartKey and LogChunkKey.
//
// A call whose parts do not make such a key returns ErrInvalid before any
// call to the store. A key that leaves the namespace returns
// blob.ErrOutOfScope, and the call writes one refusal to the
// scopeaudit.Recorder.
type ArtifactBlobs struct {
	store    blob.Store
	refusals scopeaudit.Recorder
}

// NewArtifactBlobs returns ArtifactBlobs over store. Both arguments are
// required.
func NewArtifactBlobs(store blob.Store, refusals scopeaudit.Recorder) (*ArtifactBlobs, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: blob store", ErrInvalid)
	}

	if refusals == nil {
		return nil, fmt.Errorf("%w: out-of-scope recorder", ErrInvalid)
	}

	return &ArtifactBlobs{store: store, refusals: refusals}, nil
}

// checkParts returns ErrInvalid unless the job ID, the artifact ID and the
// name are one key segment each, with the prefixes of this package.
//
// Defense in depth: blob.ParseKey and the scoped view refuse a key that leaves
// the namespace. This check stays so that an ID with a "/" never makes a key
// in the directory of another job, which both of those accept.
func checkParts(id ID, prefix, artifact string) error {
	if !strings.HasPrefix(string(id), IDPrefix) || strings.Contains(string(id), "/") ||
		!strings.HasPrefix(artifact, prefix) || strings.Contains(artifact, "/") {
		return fmt.Errorf("%w: artifact blob key", ErrInvalid)
	}

	return nil
}

// planKey returns the scoped view and the key of the part k of the plan.
func (b *ArtifactBlobs) planKey(ns namespace.ID, id ID, plan string, k OutputKind) (*blob.Scoped, blob.Key, error) {
	if err := checkParts(id, PlanIDPrefix, plan); err != nil {
		return nil, "", err
	}

	if k != OutputPlanDisplay && k != OutputPlanApply {
		return nil, "", fmt.Errorf("%w: plan part %q", ErrInvalid, k)
	}

	rel, err := PlanPartKey(id, plan, k)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return b.scoped(ns, rel)
}

// logKey returns the scoped view and the key of the chunk seq of the log
// version.
func (b *ArtifactBlobs) logKey(ns namespace.ID, id ID, version string, seq int) (*blob.Scoped, blob.Key, error) {
	if err := checkParts(id, LogVersionIDPrefix, version); err != nil {
		return nil, "", err
	}

	if seq < 0 || seq >= MaxLogChunks {
		return nil, "", fmt.Errorf("%w: log chunk %d", ErrInvalid, seq)
	}

	rel, err := LogChunkKey(id, version, seq)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return b.scoped(ns, rel)
}

// scoped returns the view for ns and the key of rel inside it.
func (b *ArtifactBlobs) scoped(ns namespace.ID, rel blob.Key) (*blob.Scoped, blob.Key, error) {
	v, err := blob.NewScoped(b.store, ns)
	if err != nil {
		return nil, "", fmt.Errorf("scope of artifact blobs: %w", err)
	}

	key, err := v.Key(rel.String())
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return v, key, nil
}

// PutPlanPart stores r as the part k of the plan of the job id. It returns
// blob.ErrExists when the part exists already.
func (b *ArtifactBlobs) PutPlanPart(
	ctx context.Context, ns namespace.ID, id ID, plan string, k OutputKind, r io.Reader,
) error {
	v, key, err := b.planKey(ns, id, plan, k)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ns, opPut, v.Put(ctx, key, r), v, key)
}

// DeletePlanPart deletes the part k of the plan of the job id. A part that does
// not exist is not an error. The recorder uses it to remove the parts of a plan
// that it did not store.
func (b *ArtifactBlobs) DeletePlanPart(
	ctx context.Context, ns namespace.ID, id ID, plan string, k OutputKind,
) error {
	v, key, err := b.planKey(ns, id, plan, k)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ns, opDelete, v.Delete(ctx, key), v, key)
}

// OpenPlanPart opens the part k of the plan of the job id. It returns
// blob.ErrNotFound for a part that does not exist.
func (b *ArtifactBlobs) OpenPlanPart(
	ctx context.Context, ns namespace.ID, id ID, plan string, k OutputKind,
) (io.ReadCloser, error) {
	v, key, err := b.planKey(ns, id, plan, k)
	if err != nil {
		return nil, err
	}

	return b.open(ctx, ns, v, key)
}

// PutLogChunk stores r as the chunk seq of the log version of the job id. It
// returns blob.ErrExists when the chunk exists already.
func (b *ArtifactBlobs) PutLogChunk(
	ctx context.Context, ns namespace.ID, id ID, version string, seq int, r io.Reader,
) error {
	v, key, err := b.logKey(ns, id, version, seq)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ns, opPut, v.Put(ctx, key, r), v, key)
}

// OpenLogChunk opens the chunk seq of the log version of the job id. It
// returns blob.ErrNotFound for a chunk that does not exist.
func (b *ArtifactBlobs) OpenLogChunk(
	ctx context.Context, ns namespace.ID, id ID, version string, seq int,
) (io.ReadCloser, error) {
	v, key, err := b.logKey(ns, id, version, seq)
	if err != nil {
		return nil, err
	}

	return b.open(ctx, ns, v, key)
}

// open opens the blob under key.
func (b *ArtifactBlobs) open(
	ctx context.Context, ns namespace.ID, v *blob.Scoped, key blob.Key,
) (io.ReadCloser, error) {
	rc, err := v.Get(ctx, key)
	if err != nil {
		return nil, b.refuse(ctx, ns, opGet, err, v, key)
	}

	return rc, nil
}

// refuse writes the refusal to the recorder when err is blob.ErrOutOfScope.
// It returns err, joined with the error of the recorder when the record
// fails, so the caller still sees the refusal. Any other error passes
// through.
func (b *ArtifactBlobs) refuse(
	ctx context.Context, ns namespace.ID, op string, err error, v *blob.Scoped, key blob.Key,
) error {
	if !errors.Is(err, blob.ErrOutOfScope) {
		return err
	}

	if recErr := b.refusals.RecordOutOfScope(ctx, ns, op, scopeaudit.PlaceOf(v, key)); recErr != nil {
		return errors.Join(blob.ErrOutOfScope, fmt.Errorf("record the refusal: %w", recErr))
	}

	return blob.ErrOutOfScope
}
