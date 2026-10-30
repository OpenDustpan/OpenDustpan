package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// PlanPartRef names one part of a stored plan in the blob store.
type PlanPartRef struct {
	Namespace namespace.ID
	Job       ID
	Plan      string
	Kind      OutputKind
}

// LogChunkRef names one chunk of a log version in the blob store.
type LogChunkRef struct {
	Namespace namespace.ID
	Job       ID
	Version   string
	Seq       int
	// BlobKey is the key of the blob, as ChunkBlobKey reads it. The empty
	// text names the blob of the first layout.
	BlobKey string
}

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

// PutPlanPart stores r as the part ref. It returns blob.ErrExists when the
// part exists already.
func (b *ArtifactBlobs) PutPlanPart(ctx context.Context, ref PlanPartRef, r io.Reader) error {
	at, err := b.planKey(ref)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ref.Namespace, opPut, at.view.Put(ctx, at.key, r), at)
}

// DeletePlanPart deletes the part ref. A part that does not exist is not an
// error. The recorder uses it to remove the parts of a plan that it did not
// store.
func (b *ArtifactBlobs) DeletePlanPart(ctx context.Context, ref PlanPartRef) error {
	at, err := b.planKey(ref)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ref.Namespace, opDelete, at.view.Delete(ctx, at.key), at)
}

// OpenPlanPart opens the part ref. It returns blob.ErrNotFound for a part that
// does not exist.
func (b *ArtifactBlobs) OpenPlanPart(ctx context.Context, ref PlanPartRef) (io.ReadCloser, error) {
	at, err := b.planKey(ref)
	if err != nil {
		return nil, err
	}

	return b.open(ctx, ref.Namespace, at)
}

// PutLogChunk stores r as the chunk ref. It returns blob.ErrExists when the
// store refuses a second write of the key. A store may also replace the object
// with no error: the row of the chunk, not this call, says which attempt won.
func (b *ArtifactBlobs) PutLogChunk(ctx context.Context, ref LogChunkRef, r io.Reader) error {
	at, err := b.logKey(ref)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ref.Namespace, opPut, at.view.Put(ctx, at.key, r), at)
}

// DeleteLogChunk deletes the blob of one write attempt of a chunk. It refuses
// ref with no BlobKey with ErrInvalid: the blob of the first layout is the blob
// of a row that may name it, and a writer never deletes it. A blob that does
// not exist is not an error.
func (b *ArtifactBlobs) DeleteLogChunk(ctx context.Context, ref LogChunkRef) error {
	if ref.BlobKey == "" {
		return fmt.Errorf("%w: delete of a log chunk with no attempt key", ErrInvalid)
	}

	at, err := b.logKey(ref)
	if err != nil {
		return err
	}

	return b.refuse(ctx, ref.Namespace, opDelete, at.view.Delete(ctx, at.key), at)
}

// OpenLogChunk opens the chunk ref. It returns blob.ErrNotFound for a chunk
// that does not exist.
func (b *ArtifactBlobs) OpenLogChunk(ctx context.Context, ref LogChunkRef) (io.ReadCloser, error) {
	at, err := b.logKey(ref)
	if err != nil {
		return nil, err
	}

	return b.open(ctx, ref.Namespace, at)
}

// keyAt is a key and the view of the namespace that it is in.
type keyAt struct {
	view *blob.Scoped
	key  blob.Key
}

// maxIDSegment is the longest part of an ID after its prefix that checkParts
// accepts. The IDs of the server have 20 characters there.
const maxIDSegment = 64

// checkParts returns ErrInvalid unless the job ID and the artifact ID are one
// key segment each, with the prefixes of this package. After its prefix, an ID
// has 1 to maxIDSegment characters of the allowlist a-z, 0-9 and "-".
//
// Defense in depth: blob.ParseKey and the scoped view refuse a key that leaves
// the namespace. This check stays so that an ID with a "/", a "." or a control
// character never makes a key in the directory of another job, which both of
// those accept. It is an allowlist, so a character that nobody thought of is
// refused too.
func checkParts(id ID, prefix, artifact string) error {
	if !validIDSegment(string(id), IDPrefix) || !validIDSegment(artifact, prefix) {
		return fmt.Errorf("%w: artifact blob key", ErrInvalid)
	}

	return nil
}

// validIDSegment reports whether id is prefix followed by 1 to maxIDSegment
// characters of the allowlist.
func validIDSegment(id, prefix string) bool {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || rest == "" || len(rest) > maxIDSegment {
		return false
	}

	for _, c := range []byte(rest) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}

// planKey returns the key of the part ref and the view that it is in.
func (b *ArtifactBlobs) planKey(ref PlanPartRef) (keyAt, error) {
	if err := checkParts(ref.Job, PlanIDPrefix, ref.Plan); err != nil {
		return keyAt{}, err
	}

	if ref.Kind != OutputPlanDisplay && ref.Kind != OutputPlanApply {
		return keyAt{}, fmt.Errorf("%w: plan part %q", ErrInvalid, ref.Kind)
	}

	rel, err := PlanPartKey(ref.Job, ref.Plan, ref.Kind)
	if err != nil {
		return keyAt{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return b.scoped(ref.Namespace, rel)
}

// logKey returns the key of the chunk ref and the view that it is in.
func (b *ArtifactBlobs) logKey(ref LogChunkRef) (keyAt, error) {
	if err := checkParts(ref.Job, LogVersionIDPrefix, ref.Version); err != nil {
		return keyAt{}, err
	}

	if ref.Seq < 0 || ref.Seq >= MaxLogChunks {
		return keyAt{}, fmt.Errorf("%w: log chunk %d", ErrInvalid, ref.Seq)
	}

	rel, err := ChunkBlobKey(ref.Job, ref.Version, ref.Seq, ref.BlobKey)
	if err != nil {
		return keyAt{}, err
	}

	return b.scoped(ref.Namespace, rel)
}

// scoped returns the view for ns and the key of rel inside it.
func (b *ArtifactBlobs) scoped(ns namespace.ID, rel blob.Key) (keyAt, error) {
	v, err := blob.NewScoped(b.store, ns)
	if err != nil {
		return keyAt{}, fmt.Errorf("scope of artifact blobs: %w", err)
	}

	key, err := v.Key(rel.String())
	if err != nil {
		return keyAt{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return keyAt{view: v, key: key}, nil
}

// open opens the blob under the key of at.
func (b *ArtifactBlobs) open(ctx context.Context, ns namespace.ID, at keyAt) (io.ReadCloser, error) {
	rc, err := at.view.Get(ctx, at.key)
	if err != nil {
		return nil, b.refuse(ctx, ns, opGet, err, at)
	}

	return rc, nil
}

// refuse writes the refusal to the recorder when err is blob.ErrOutOfScope.
// It returns err, joined with the error of the recorder when the record
// fails, so the caller still sees the refusal. Any other error passes
// through.
func (b *ArtifactBlobs) refuse(ctx context.Context, ns namespace.ID, op string, err error, at keyAt) error {
	if !errors.Is(err, blob.ErrOutOfScope) {
		return err
	}

	if recErr := b.refusals.RecordOutOfScope(ctx, ns, op, scopeaudit.PlaceOf(at.view, at.key)); recErr != nil {
		return errors.Join(blob.ErrOutOfScope, fmt.Errorf("record the refusal: %w", recErr))
	}

	return blob.ErrOutOfScope
}
