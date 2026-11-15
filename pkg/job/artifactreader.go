package job

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ArtifactReader is the read side of ArtifactStore: it opens the plaintext of
// stored plans and logs. The caller checks the actions.
type ArtifactReader interface {
	// OpenPlanPart returns the plaintext of the part k of the plan with the
	// ID plan. It returns ErrNotFound for an unknown plan or part, and
	// secret.ErrShredded for a plan whose item key a redaction shredded.
	// The caller checks the actions, and masks the readable plan.
	OpenPlanPart(ctx context.Context, ns namespace.Name, plan string, k OutputKind) (io.ReadCloser, error)
	// OpenLog returns the plaintext of the log version with the ID version,
	// its stored chunks in order, so a reader can show a log while the step
	// runs. It returns ErrNotFound for an unknown version, and
	// secret.ErrShredded for a version whose item key a redaction shredded.
	// The reader of a closed version checks the SHA-256 of the whole log at
	// its end, and returns secret.ErrDecrypt when it differs.
	OpenLog(ctx context.Context, ns namespace.Name, version string) (io.ReadCloser, error)
}

// ArtifactReads is the ArtifactReader over a RecordStore, the blob store and
// the item keys.
type ArtifactReads struct {
	store      RecordStore
	blobs      *ArtifactBlobs
	sealer     secret.ItemSealer
	namespaces NamespaceReader
}

var _ ArtifactReader = (*ArtifactReads)(nil)

// NewArtifactReader returns the reader of stored plans and logs. Every
// argument is required.
func NewArtifactReader(
	store RecordStore, blobs *ArtifactBlobs, sealer secret.ItemSealer, namespaces NamespaceReader,
) (*ArtifactReads, error) {
	switch {
	case store == nil:
		return nil, fmt.Errorf("%w: record store", ErrInvalid)
	case blobs == nil:
		return nil, fmt.Errorf("%w: artifact blobs", ErrInvalid)
	case sealer == nil:
		return nil, fmt.Errorf("%w: item sealer", ErrInvalid)
	case namespaces == nil:
		return nil, fmt.Errorf("%w: namespace reader", ErrInvalid)
	}

	return &ArtifactReads{store: store, blobs: blobs, sealer: sealer, namespaces: namespaces}, nil
}

// itemRef returns the reference of the item key of the plan or log version
// that rec names.
func itemRef(rec *ArtifactRecord, kind secret.ItemKind, item string) (secret.ItemRef, error) {
	unit := history.Unit{Namespace: rec.Unit.Namespace(), Tool: rec.Unit.Tool(), ID: rec.Unit.UnitID()}

	ref, err := history.ItemRef{Unit: unit, Kind: kind, Item: item}.KeyRef()
	if err != nil {
		return secret.ItemRef{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return ref, nil
}

// OpenPlanPart implements ArtifactReader.
func (a *ArtifactReads) OpenPlanPart(
	ctx context.Context, ns namespace.Name, plan string, k OutputKind,
) (io.ReadCloser, error) {
	rec, err := a.store.Plan(ctx, ns, plan)
	if err != nil {
		return nil, err
	}

	part := rec.Plan.Part(k)
	if part == nil {
		return nil, fmt.Errorf("%w: plan %s has no part %q", ErrNotFound, plan, k)
	}

	if !rec.Plan.RedactedAt.IsZero() {
		return nil, fmt.Errorf("plan %s: %w", plan, secret.ErrShredded)
	}

	ref, err := itemRef(&rec, secret.ItemKindPlan, plan)
	if err != nil {
		return nil, err
	}

	nsID, err := a.namespaceID(ctx, ns)
	if err != nil {
		return nil, err
	}

	key, err := PlanPartKey(rec.Job, plan, k)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	// The record of the item key answers before any blob read: a shredded key
	// fails here, also when a redaction deleted the blob.
	if probeErr := probeContent(ctx, a.sealer, ref, []byte(key.String())); probeErr != nil {
		return nil, fmt.Errorf("open plan part: %w", probeErr)
	}

	rc, err := a.blobs.OpenPlanPart(ctx, PlanPartRef{Namespace: nsID, Job: rec.Job, Plan: plan, Kind: k})
	if err != nil {
		return nil, mapBlobError(err, "plan part")
	}

	plain, err := a.sealer.OpenContent(ctx, ref, []byte(key.String()), rc)
	if err != nil {
		return nil, closeWith(rc, fmt.Errorf("open plan part: %w", err))
	}

	return &verifyReader{
		src: plain, closer: closePair{first: plain, second: rc}, sum: sha256.New(), want: part.SHA256,
		size: part.Size, check: true, read: 0,
	}, nil
}

// OpenLog implements ArtifactReader.
func (a *ArtifactReads) OpenLog(ctx context.Context, ns namespace.Name, version string) (io.ReadCloser, error) {
	rec, err := a.store.Log(ctx, ns, version)
	if err != nil {
		return nil, err
	}

	nsID, err := a.namespaceID(ctx, ns)
	if err != nil {
		return nil, err
	}

	return openLogVersion(ctx, a.blobs, a.sealer, nsID, &rec)
}

// namespaceID returns the ID of the namespace ns.
func (a *ArtifactReads) namespaceID(ctx context.Context, ns namespace.Name) (namespace.ID, error) {
	return namespaceIDOf(ctx, a.namespaces, ns)
}

// namespaceIDOf returns the ID of the namespace ns.
func namespaceIDOf(ctx context.Context, r NamespaceReader, ns namespace.Name) (namespace.ID, error) {
	n, err := r.GetNamespace(ctx, ns)
	if err != nil {
		return "", fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.ID, nil
}

// mapBlobError turns a missing blob into ErrNotFound. Any other error passes
// through, with the name of what failed.
func mapBlobError(err error, what string) error {
	if errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("%w: %s blob", ErrNotFound, what)
	}

	return fmt.Errorf("open %s blob: %w", what, err)
}

// closeWith closes c and returns err.
// probeContent opens no content: it reads the record of the item key of ref,
// so a shredded key or no key fails before any blob read. It closes the
// reader at once, which clears the key that the reader holds.
func probeContent(ctx context.Context, sealer secret.ItemSealer, ref secret.ItemRef, binding []byte) error {
	probe, err := sealer.OpenContent(ctx, ref, binding, bytes.NewReader(nil))
	if err != nil {
		return fmt.Errorf("probe item key: %w", err)
	}

	if err := probe.Close(); err != nil {
		return fmt.Errorf("probe item key: %w", err)
	}

	return nil
}

// closePair closes the opened content first, which clears its key, and then
// the blob object under it.
type closePair struct {
	first, second io.Closer
}

// Close closes both, also when the first Close fails.
func (c closePair) Close() error {
	return errors.Join(c.first.Close(), c.second.Close())
}

func closeWith(c io.Closer, err error) error {
	_ = c.Close() //nolint:errcheck // The error to return is the first one.

	return err
}

// chunkBinding returns the binding of the chunk seq of a log version: its blob
// key, so a chunk does not open at another position, in another version or in
// another job. The text stored is the blob key of the row of the chunk, as
// ChunkBlobKey reads it.
func chunkBinding(id ID, version string, seq int, stored string) ([]byte, error) {
	key, err := ChunkBlobKey(id, version, seq, stored)
	if err != nil {
		return nil, err
	}

	return []byte(key.String()), nil
}

// probeBinding returns a binding for the probe of the item key of a log
// version. The probe reads no content, so any valid binding of the version
// serves.
func probeBinding(id ID, version string) ([]byte, error) {
	key, err := LogChunkAttemptKey(id, version, 0, LogAttemptIDPrefix+"probe")
	if err != nil {
		return nil, err
	}

	return []byte(key.String()), nil
}

// openLogVersion opens the stored chunks of the log version in rec, in
// order. The version is open when rec.Log.ClosedAt is zero.
func openLogVersion(
	ctx context.Context, blobs *ArtifactBlobs, sealer secret.ItemSealer, nsID namespace.ID, rec *ArtifactRecord,
) (io.ReadCloser, error) {
	log := rec.Log

	if !log.RedactedAt.IsZero() {
		return nil, fmt.Errorf("log %s: %w", log.ID, secret.ErrShredded)
	}

	ref, err := itemRef(rec, secret.ItemKindJobLog, log.ID)
	if err != nil {
		return nil, err
	}

	// The record of the item key answers before any blob read: a shredded key
	// fails here, also for a log with no chunk.
	first, err := probeBinding(rec.Job, log.ID)
	if err != nil {
		return nil, err
	}

	if probeErr := probeContent(ctx, sealer, ref, first); probeErr != nil {
		return nil, fmt.Errorf("open log: %w", probeErr)
	}

	closed := !log.ClosedAt.IsZero()

	return &logReader{
		ctx: ctx, blobs: blobs, sealer: sealer, ns: nsID, job: rec.Job, ref: ref, log: log, keys: rec.ChunkKeys,
		cur: nil, closer: nil, next: 0, sum: sha256.New(), done: false, check: closed, end: nil,
	}, nil
}

// logReader reads the chunks of a log version one after the other.
type logReader struct {
	ctx    context.Context //nolint:containedctx // A reader of a stream holds the context of its call.
	blobs  *ArtifactBlobs
	sealer secret.ItemSealer
	ns     namespace.ID
	job    ID
	ref    secret.ItemRef
	log    *LogRef
	// keys holds the blob key of each confirmed chunk (ArtifactRecord.ChunkKeys).
	keys []string

	cur    io.Reader
	closer io.Closer
	next   int
	sum    hash.Hash
	done   bool
	// check is true for a closed version: the end of the stream checks the
	// hash of the whole log.
	check bool
	// end is the result of the check at the end of the log: io.EOF, or the
	// error of a hash that does not match. Each later read returns it again.
	end error
}

// Read implements io.Reader.
func (r *logReader) Read(p []byte) (int, error) {
	for {
		if r.cur == nil {
			if err := r.advance(); err != nil {
				return 0, err
			}
		}

		n, err := r.cur.Read(p)
		_, _ = r.sum.Write(p[:n])

		if errors.Is(err, io.EOF) {
			if closeErr := r.closeChunk(); closeErr != nil {
				return n, closeErr
			}

			err = nil
		}

		if n > 0 || err != nil {
			return n, err
		}
	}
}

// Close implements io.Closer.
func (r *logReader) Close() error {
	r.done = true

	if r.closer == nil {
		return nil
	}

	return r.closeChunk()
}

// verifyReader reads src and checks its SHA-256 and size at the end.
type verifyReader struct {
	src    io.Reader
	closer io.Closer
	sum    hash.Hash
	want   string
	size   int64
	read   int64
	check  bool
}

// Read implements io.Reader.
func (v *verifyReader) Read(p []byte) (int, error) {
	n, err := v.src.Read(p)
	_, _ = v.sum.Write(p[:n])
	v.read += int64(n)

	if errors.Is(err, io.EOF) && v.check &&
		(v.read != v.size || hex.EncodeToString(v.sum.Sum(nil)) != v.want) {
		return n, fmt.Errorf("%w: the content does not match its hash", secret.ErrDecrypt)
	}

	return n, err //nolint:wrapcheck // io.EOF must pass as it is.
}

// Close implements io.Closer.
func (v *verifyReader) Close() error {
	if err := v.closer.Close(); err != nil {
		return fmt.Errorf("close artifact: %w", err)
	}

	return nil
}

// openChunk opens the chunk r.next.
func (r *logReader) openChunk() error {
	if r.next >= len(r.keys) {
		return fmt.Errorf("%w: log chunk blob key", ErrNotFound)
	}

	stored := r.keys[r.next]

	rc, err := r.blobs.OpenLogChunk(
		r.ctx, LogChunkRef{Namespace: r.ns, Job: r.job, Version: r.log.ID, Seq: r.next, BlobKey: stored},
	)
	if err != nil {
		return mapBlobError(err, "log chunk")
	}

	binding, err := chunkBinding(r.job, r.log.ID, r.next, stored)
	if err != nil {
		return closeWith(rc, err)
	}

	plain, err := r.sealer.OpenContent(r.ctx, r.ref, binding, rc)
	if err != nil {
		return closeWith(rc, fmt.Errorf("open log chunk: %w", err))
	}

	r.cur, r.closer = plain, closePair{first: plain, second: rc}
	r.next++

	return nil
}

// closeChunk closes the chunk that r reads.
func (r *logReader) closeChunk() error {
	err := r.closer.Close()
	r.cur, r.closer = nil, nil

	if err != nil {
		return fmt.Errorf("close log chunk: %w", err)
	}

	return nil
}

// finish checks the hash of a closed log at its end.
func (r *logReader) finish() error {
	if !r.check {
		return io.EOF
	}

	if hex.EncodeToString(r.sum.Sum(nil)) != r.log.SHA256 {
		return fmt.Errorf("%w: the log %s does not match its hash", secret.ErrDecrypt, r.log.ID)
	}

	return io.EOF
}

// advance opens the next chunk. At the end of the log it returns io.EOF, or
// the error of the hash check of a closed log.
func (r *logReader) advance() error {
	if r.done {
		if r.end != nil {
			return r.end
		}

		return io.EOF
	}

	if r.next >= r.log.Chunks {
		r.done = true
		r.end = r.finish()

		return r.end
	}

	return r.openChunk()
}
