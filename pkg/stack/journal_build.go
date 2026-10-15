package stack

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The backup manifest records MD5 of stored bytes, for integrity, not security.
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// emptyBaseDeployment is the base of the first update of a stack: no
// resources and no other members. The replay keeps it only for the members
// the entries set.
const emptyBaseDeployment = `{"resources":[]}`

// builtOutput is the document the replay writes: the schema version, the
// features the resources need, and the deployment they build.
type builtOutput struct {
	Version    int             `json:"version"`
	Features   []string        `json:"features"`
	Deployment json.RawMessage `json:"deployment"`
}

// journalBatchBody is the shape of one stored batch: the entries in the
// order the CLI sent them. The build replays the entries of every batch in
// arrival order.
type journalBatchBody struct {
	Entries []json.RawMessage `json:"entries"`
}

// builtSave groups one built checkpoint with the write it needs: the
// references of the checkpoint and the fence of its transaction.
type builtSave struct {
	ref     Ref
	id      UpdateID
	stackID ID
	fence   namespace.Fence
	built   Checkpoint
}

// builtBlob groups a built save with its blob write: the key it wrote with
// the size and digest of the bytes this save tried to write, and the
// previous checkpoint of the update for the sweep after the commit.
type builtBlob struct {
	save builtSave
	key  blob.Key
	size int64
	sum  [md5.Size]byte
	prev *Checkpoint
}

// buildJournalCheckpoint replays the stored batches of the update on its
// base checkpoint and returns the checkpoint that results, without storing
// it. It holds the batches and the built deployment in memory; a later
// streaming build replaces this function without changing its callers. It
// returns ErrMissingDependency without a replayer or a journal writer.
func (s *Updates) buildJournalCheckpoint(
	ctx context.Context, ref Ref, u Update, stackID ID,
) (Checkpoint, error) {
	var zero Checkpoint

	if s.replayer == nil || s.journal == nil {
		return zero, fmt.Errorf("build checkpoint of update %s: %w", u.ID, ErrMissingDependency)
	}

	base, err := s.journalBase(ctx, ref, u)
	if err != nil {
		return zero, err
	}

	batches, err := s.journal.Batches(ctx, stackID, u.ID)
	if err != nil {
		return zero, fmt.Errorf("build checkpoint of update %s: %w", u.ID, err)
	}

	combined, err := combineBatches(batches)
	if err != nil {
		return zero, fmt.Errorf("build checkpoint of update %s: %w", u.ID, err)
	}

	open := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(base)), nil
	}

	var out bytes.Buffer

	if err := s.replayer.Replay(ctx, open, bytes.NewReader(combined), &out); err != nil {
		return zero, fmt.Errorf("build checkpoint of update %s: %w", u.ID, err)
	}

	return s.builtCheckpoint(ref, u, out.Bytes())
}

// journalBase returns the deployment the replay starts from: the checkpoint
// the server served at the start of the update, unwrapped like an export.
// A version without a checkpoint falls back to the latest one, and a stack
// without state falls back to an empty base.
func (s *Updates) journalBase(ctx context.Context, ref Ref, u Update) ([]byte, error) {
	if u.Version > 1 {
		if cp, err := s.checkpoints.CheckpointByVersion(ctx, ref, u.Version-1); err == nil {
			return s.baseDeployment(ctx, cp)
		} else if !errors.Is(err, ErrNoState) {
			return nil, fmt.Errorf("read base of update %s: %w", u.ID, err)
		}
	}

	if cp, err := s.checkpoints.LatestCheckpoint(ctx, ref); err == nil {
		return s.baseDeployment(ctx, cp)
	} else if !errors.Is(err, ErrNoState) {
		return nil, fmt.Errorf("read base of update %s: %w", u.ID, err)
	}

	return []byte(emptyBaseDeployment), nil
}

// baseDeployment resolves the deployment of cp and returns its inner bytes,
// the same bytes an export serves.
func (s *Updates) baseDeployment(ctx context.Context, cp Checkpoint) ([]byte, error) {
	resolved, err := s.resolveCheckpoint(ctx, cp)
	if err != nil {
		return nil, fmt.Errorf("read base of update %s: %w", cp.Update, err)
	}

	return innerDeployment(resolved).Deployment, nil
}

// combineBatches merges the entries of every batch body into one batch body,
// in arrival order. The entries of a batch keep the order of the body.
func combineBatches(bodies [][]byte) ([]byte, error) {
	entries := make([]json.RawMessage, 0)

	for _, body := range bodies {
		var batch journalBatchBody
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("decode journal batch: %w", err)
		}

		entries = append(entries, batch.Entries...)
	}

	combined, err := json.Marshal(journalBatchBody{Entries: entries})
	if err != nil {
		return nil, fmt.Errorf("encode journal batches: %w", err)
	}

	return combined, nil
}

// builtCheckpoint splits the replay output into the checkpoint it stores:
// the schema version, the features and the inner deployment. The sequence
// is 0, like any other unsequenced save.
func (s *Updates) builtCheckpoint(ref Ref, u Update, document []byte) (Checkpoint, error) {
	var out builtOutput

	if err := json.Unmarshal(document, &out); err != nil {
		return Checkpoint{}, fmt.Errorf("decode built checkpoint of update %s: %w", u.ID, err)
	}

	if len(out.Deployment) == 0 {
		return Checkpoint{}, fmt.Errorf("%w: built checkpoint of update %s has no deployment", ErrInvalidUpdate, u.ID)
	}

	in := CheckpointInput{
		Features:      out.Features,
		Deployment:    []byte(out.Deployment),
		SchemaVersion: out.Version,
		Sequence:      0,
		Invalid:       false,
	}

	if err := s.validate(in); err != nil {
		return Checkpoint{}, fmt.Errorf("built checkpoint of update %s: %w", u.ID, err)
	}

	return Checkpoint{
		SavedAt:       s.clock(),
		Features:      in.Features,
		Deployment:    in.Deployment,
		Update:        u.ID,
		Ref:           ref,
		Lease:         u.Lease.Digest,
		SchemaVersion: in.SchemaVersion,
		Sequence:      0,
		Digest:        sha256.Sum256(in.Deployment),
		Invalid:       false,
		BlobKey:       "",
		BlobSize:      0,
		BlobMD5:       [md5.Size]byte{},
	}, nil
}

// storeBuiltCheckpoint writes the blob of save through the checkpoint blob
// path, then commits its row with the delete of the journal of the update.
// A blob that is already there belongs to a first attempt whose commit never
// landed: the retry adopts it when it holds the same content.
func (s *Updates) storeBuiltCheckpoint(ctx context.Context, save builtSave) error {
	prev, err := s.previousCheckpoint(ctx, save.ref, save.id)
	if err != nil {
		return fmt.Errorf("save built checkpoint of update %s: %w", save.id, err)
	}

	key, err := checkpointBlobKey(save.stackID, save.id, 0, prev)
	if err != nil {
		return fmt.Errorf("save built checkpoint of update %s: %w", save.id, err)
	}

	size, sum, err := s.putCheckpointBlob(ctx, key, save.built.Deployment)
	if err != nil {
		if errors.Is(err, blob.ErrExists) {
			return s.adoptBuiltBlob(ctx, builtBlob{save: save, key: key, size: size, sum: sum, prev: prev})
		}

		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("save built checkpoint of update %s: %w", save.id, cerr)
		}

		return fmt.Errorf("save built checkpoint of update %s: %w", save.id, ErrCheckpointUnavailable)
	}

	return s.commitBuiltRow(ctx, builtRow(save, key, prev), size, sum)
}

// builtRow returns the checkpoint row write of save under key: the built
// deployment with the lease that authorized it.
func builtRow(save builtSave, key blob.Key, prev *Checkpoint) checkpointSave {
	return checkpointSave{
		ref: save.ref, id: save.id, stackID: save.stackID, fence: save.fence, lease: save.built.Lease,
		in: CheckpointInput{
			Features:      save.built.Features,
			Deployment:    save.built.Deployment,
			SchemaVersion: save.built.SchemaVersion,
			Sequence:      0,
			Invalid:       false,
		},
		digest: save.built.Digest, prev: prev, key: key,
	}
}

// adoptBuiltBlob answers a blob write that met an existing key, like a
// retried checkpoint save does: the first build wrote it but never
// committed its row. Anything else is a concurrent save of the same update,
// which fails without touching the journal.
func (s *Updates) adoptBuiltBlob(ctx context.Context, stored builtBlob) error {
	raw, err := s.blobs.Get(ctx, stored.key)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("save built checkpoint of update %s: %w", stored.save.id, cerr)
		}

		return fmt.Errorf("save built checkpoint of update %s: %w", stored.save.id, ErrCheckpointUnavailable)
	}

	gave, err := readStoredBlob(raw, s.storedLimit())
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("save built checkpoint of update %s: %w", stored.save.id, cerr)
		}

		return fmt.Errorf("save built checkpoint of update %s: %w", stored.save.id, ErrCheckpointUnavailable)
	}

	if storedSize, storedSum := storedAttrs(gave); storedSize != stored.size || storedSum != stored.sum {
		deployment, derr := gunzipBytes(gave, s.maxCheckpoints)
		if derr != nil || sha256.Sum256(deployment) != stored.save.built.Digest {
			return fmt.Errorf("save built checkpoint of update %s: %w", stored.save.id, ErrConcurrentModification)
		}

		size, sum := storedAttrs(gave)

		return s.commitBuiltRow(ctx, builtRow(stored.save, stored.key, stored.prev), size, sum)
	}

	return s.commitBuiltRow(ctx, builtRow(stored.save, stored.key, stored.prev), stored.size, stored.sum)
}

// commitBuiltRow writes the row of a built checkpoint with the delete of its
// journal, in one fenced transaction, then deletes the journal blobs and
// sweeps the update like a checkpoint save does. The store checks the lease
// and the fence; a refused build deletes nothing and keeps the journal. A
// failed blob delete leaves the blob for the sweep and never fails the
// build.
func (s *Updates) commitBuiltRow(ctx context.Context, save checkpointSave, size int64, sum [md5.Size]byte) error {
	built, ok := s.checkpoints.(BuiltCheckpointStore)
	if !ok {
		return fmt.Errorf("save built checkpoint of update %s: %w", save.id, ErrMissingDependency)
	}

	cp := Checkpoint{
		SavedAt:       s.clock(),
		Features:      save.in.Features,
		Deployment:    save.in.Deployment,
		Update:        save.id,
		Ref:           save.ref,
		Lease:         save.lease,
		SchemaVersion: save.in.SchemaVersion,
		Sequence:      0,
		Digest:        save.digest,
		Invalid:       false,
		BlobKey:       save.key,
		BlobSize:      size,
		BlobMD5:       sum,
	}

	keys, err := built.PutBuiltCheckpoint(ctx, cp, save.fence)
	if err != nil {
		return fmt.Errorf("save built checkpoint of update %s: %w", save.id, err)
	}

	if len(keys) > 0 && s.journal != nil {
		if err := s.journal.RemoveBlobs(ctx, keys); err != nil {
			s.logger.WarnContext(
				ctx,
				"delete journal blobs",
				slog.String("stack", save.ref.String()),
				slog.String("update", save.id.String()),
			)
		}
	}

	s.afterCheckpointSave(ctx, save)

	return nil
}

// buildBeforeEnd builds and stores the checkpoint of a journaled update
// before it ends. An update that already ended, a preview, an update that
// saves checkpoints, and an update with no stored batches need no build. A
// failed build keeps the journal and returns an error, so the update stays
// running.
func (s *Updates) buildBeforeEnd(ctx context.Context, ref Ref, id UpdateID, u Update) error {
	if u.Status.Terminal() || !u.Mutates() || u.JournalVersion == 0 {
		return nil
	}

	if s.journal == nil || s.replayer == nil {
		return fmt.Errorf("build checkpoint of update %s: %w", id, ErrMissingDependency)
	}

	fence, err := s.fence(ctx, ref)
	if err != nil {
		return err
	}

	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return fmt.Errorf("build checkpoint of update %s: %w", id, err)
	}

	batches, err := s.journal.Batches(ctx, st.ID, id)
	if err != nil {
		return fmt.Errorf("build checkpoint of update %s: %w", id, err)
	}

	if len(batches) == 0 {
		return nil
	}

	built, err := s.buildJournalCheckpoint(ctx, ref, u, st.ID)
	if err != nil {
		return err
	}

	return s.storeBuiltCheckpoint(ctx, builtSave{ref: ref, id: id, stackID: st.ID, fence: fence, built: built})
}

// journaledState builds the state of the running journaled update of the
// stack for a read, without storing it. It reports false when no running
// journaled update with stored batches holds the stack, so the caller reads
// the stored checkpoint instead.
func (s *Updates) journaledState(ctx context.Context, ref Ref) (Checkpoint, bool, error) {
	var zero Checkpoint

	u, err := s.updates.ActiveUpdate(ctx, ref)
	if err != nil {
		if errors.Is(err, ErrUpdateNotFound) {
			return zero, false, nil
		}

		return zero, false, fmt.Errorf("read journaled state of %s: %w", ref, err)
	}

	if !u.Mutates() || u.JournalVersion == 0 {
		return zero, false, nil
	}

	return s.journaledUpdate(ctx, ref, u)
}

// journaledVersion builds the state of the running journaled update for an
// explicit version read, without storing it. It reports false unless version
// is the version of that update.
func (s *Updates) journaledVersion(ctx context.Context, ref Ref, version int64) (Checkpoint, bool, error) {
	var zero Checkpoint

	u, err := s.updates.ActiveUpdate(ctx, ref)
	if err != nil {
		if errors.Is(err, ErrUpdateNotFound) {
			return zero, false, nil
		}

		return zero, false, fmt.Errorf("read journaled state of %s: %w", ref, err)
	}

	if !u.Mutates() || u.JournalVersion == 0 || u.Version != version {
		return zero, false, nil
	}

	return s.journaledUpdate(ctx, ref, u)
}

// journaledUpdate builds the state of the running journaled update u for a
// read, without storing it. It reports false when the update holds no
// stored batches, so the caller reads the stored checkpoint instead.
func (s *Updates) journaledUpdate(ctx context.Context, ref Ref, u Update) (Checkpoint, bool, error) {
	var zero Checkpoint

	if s.journal == nil || s.replayer == nil {
		return zero, false, fmt.Errorf("read journaled state of %s: %w", ref, ErrMissingDependency)
	}

	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return zero, false, fmt.Errorf("read journaled state of %s: %w", ref, err)
	}

	batches, err := s.journal.Batches(ctx, st.ID, u.ID)
	if err != nil {
		return zero, false, fmt.Errorf("read journaled state of %s: %w", ref, err)
	}

	if len(batches) == 0 {
		return zero, false, nil
	}

	built, err := s.buildJournalCheckpoint(ctx, ref, u, st.ID)
	if err != nil {
		return zero, false, err
	}

	return built, true, nil
}
