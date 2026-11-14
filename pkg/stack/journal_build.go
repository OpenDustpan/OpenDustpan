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
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
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

// builtSave groups one built checkpoint with the write it needs: the
// references of the checkpoint and the fence of its transaction.
type builtSave struct {
	ref     Ref
	id      UpdateID
	stackID ID
	fence   namespace.Fence
	built   Checkpoint
	// origin is the writer of the state version that the built checkpoint
	// publishes: the one of the update.
	origin identity.StateOrigin
	blobs  *scopedBlobs
}

// buildJournalCheckpoint replays the given batches of the update on its
// base checkpoint and returns the checkpoint that results, without storing
// it. It holds the batches and the built deployment in memory; a later
// streaming build replaces this function without changing its callers. It
// returns ErrMissingDependency without a replayer or a journal writer. A build
// past MaxJournalBuildDuration is refused with ErrJournalBuildTimeout. A build
// past a replay bound is refused with ErrTooLarge, which names the bound and
// its limit. A canceled parent context is a cancel, not a timeout.
func (s *Updates) buildJournalCheckpoint(
	ctx context.Context, bl *scopedBlobs, ref Ref, u Update, batches [][]byte,
) (Checkpoint, error) {
	var zero Checkpoint

	if s.replayer == nil || s.journal == nil {
		return zero, fmt.Errorf("build checkpoint of update %s: %w", u.ID, ErrMissingDependency)
	}

	buildCtx, cancel := context.WithTimeout(ctx, MaxJournalBuildDuration)
	defer cancel()

	base, err := s.journalBase(buildCtx, bl, ref, u)
	if err != nil {
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return zero, fmt.Errorf("build checkpoint of update %s: %w", u.ID, ErrJournalBuildTimeout)
		}
		return zero, s.mapReplayBound(u.ID, err)
	}

	open := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(base)), nil
	}

	var out bytes.Buffer

	if err := s.replayer.Replay(buildCtx, open, batches, &out); err != nil {
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return zero, fmt.Errorf("build checkpoint of update %s: %w", u.ID, ErrJournalBuildTimeout)
		}
		return zero, s.mapReplayBound(u.ID, err)
	}

	return s.builtCheckpoint(ref, u, out.Bytes())
}

// mapReplayBound maps a replay bound error to ErrTooLarge, so the handler
// answers 413 with the message of ReplayBoundMessage. The error text names
// the bound, its limit and the same command that helps as the answer. Any
// other error passes through with the update ID.
func (s *Updates) mapReplayBound(id UpdateID, err error) error {
	if bound, ok := errors.AsType[ReplayBound](err); ok && bound != nil {
		return fmt.Errorf("build checkpoint of update %s: %w: %w: %s", id, ErrTooLarge, err, tooLargeRemedy)
	}
	return fmt.Errorf("build checkpoint of update %s: %w", id, err)
}

// ReplayBoundMessage returns the message the server answers for a replay
// bound refusal: one sentence that names the bound, its count and limit, and
// the one command that helps. It reports false when err holds no replay
// bound, so the caller answers the error text itself.
func ReplayBoundMessage(err error) (string, bool) {
	bound, ok := errors.AsType[ReplayBound](err)
	if !ok {
		return "", false
	}

	limit := bound.ReplayBound()

	return fmt.Sprintf(
		"This update has %d %s, more than the limit of %d: %s.",
		limit.Count, limit.Bound, limit.Limit, tooLargeRemedy,
	), true
}

// TooLargeMessage returns the one-sentence message the server answers for a
// size refusal of a journaled update: a replay bound or a journal batch over
// its bound. It reports false when err holds neither, so the caller answers
// the error text itself.
func TooLargeMessage(err error) (string, bool) {
	if batch, ok := errors.AsType[*batchLimitError](err); ok {
		return batch.Message(), true
	}

	return ReplayBoundMessage(err)
}

// journalBase returns the deployment the replay starts from: the checkpoint
// the server served at the start of the update, unwrapped like an export.
// A version without a checkpoint falls back to the latest one, and a stack
// without state falls back to an empty base.
func (s *Updates) journalBase(ctx context.Context, bl *scopedBlobs, ref Ref, u Update) ([]byte, error) {
	if u.Version > 1 {
		if cp, err := s.checkpoints.CheckpointByVersion(ctx, ref, u.Version-1); err == nil {
			return s.baseDeployment(ctx, bl, cp)
		} else if !errors.Is(err, ErrNoState) {
			return nil, fmt.Errorf("read base of update %s: %w", u.ID, err)
		}
	}

	if cp, err := s.checkpoints.LatestCheckpoint(ctx, ref); err == nil {
		return s.baseDeployment(ctx, bl, cp)
	} else if !errors.Is(err, ErrNoState) {
		return nil, fmt.Errorf("read base of update %s: %w", u.ID, err)
	}

	return []byte(emptyBaseDeployment), nil
}

// baseDeployment resolves the deployment of cp and returns its inner bytes,
// the same bytes an export serves.
func (s *Updates) baseDeployment(ctx context.Context, bl *scopedBlobs, cp Checkpoint) ([]byte, error) {
	resolved, err := s.resolveCheckpoint(ctx, bl, cp)
	if err != nil {
		return nil, fmt.Errorf("read base of update %s: %w", cp.Update, err)
	}

	return innerDeployment(resolved).Deployment, nil
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
		NamespaceID: "", StackID: "",
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

		// The commit of the build sets the start of its attempt.
		AttemptStartedAt: time.Time{}, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}, nil
}

// storeBuiltCheckpoint writes the blob of save through the checkpoint write
// of an ordinary save, then commits its row with the delete of the journal
// of the update. The blob has the key of its own attempt. A retry after a
// first attempt whose commit never landed writes a new blob, and the sweep
// collects the blob of the first attempt.
func (s *Updates) storeBuiltCheckpoint(ctx context.Context, save builtSave) error {
	prev, err := s.previousCheckpoint(ctx, save.ref, save.id)
	if err != nil {
		return fmt.Errorf("save built checkpoint of update %s: %w", save.id, err)
	}

	key, err := checkpointBlobKey(save.blobs, save.stackID, save.id, 0)
	if err != nil {
		return fmt.Errorf("save built checkpoint of update %s: %w", save.id, err)
	}

	return s.writeCheckpoint(ctx, checkpointWrite{
		op: "save built checkpoint", ref: save.ref, id: save.id, blobs: save.blobs, stackID: save.stackID,
		key: key, deployment: save.built.Deployment,
		commit: func(ctx context.Context, size int64, sum [md5.Size]byte, started time.Time) (bool, error) {
			return false, s.commitBuiltRow(ctx, builtRow(save, key, prev), size, sum, started)
		},
	})
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
		digest: save.built.Digest, prev: prev, key: key, origin: save.origin, blobs: save.blobs,
	}
}

// commitBuiltRow writes the row of a built checkpoint with the delete of its
// journal, in one fenced transaction, then deletes the journal blobs and
// sweeps the update like a checkpoint save does. The store checks the lease
// and the fence; a refused build deletes nothing and keeps the journal. A
// failed blob delete leaves the blob for the sweep and never fails the
// build.
func (s *Updates) commitBuiltRow(
	ctx context.Context, save checkpointSave, size int64, sum [md5.Size]byte, started time.Time,
) error {
	built, ok := s.checkpoints.(BuiltCheckpointStore)
	if !ok {
		return fmt.Errorf("save built checkpoint of update %s: %w", save.id, ErrMissingDependency)
	}

	cp := Checkpoint{
		NamespaceID: "", StackID: "",
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
		Origin:        save.origin,

		AttemptStartedAt: started,
	}

	keys, err := built.PutBuiltCheckpoint(ctx, cp, save.fence)
	if err != nil {
		return fmt.Errorf("save built checkpoint of update %s: %w", save.id, err)
	}

	// The guard comes after the commit that removed the journal rows, and again
	// before each delete. A running backup or restore keeps the batch blobs,
	// and the next sweep deletes them.
	if s.journal != nil {
		for _, key := range keys {
			if s.deletesHeld(ctx, "journal build", save.ref) {
				break
			}

			if err := s.journal.RemoveBlobs(ctx, save.blobs.ns, save.stackID, save.id, []blob.Key{key}); err != nil {
				s.logger.WarnContext(
					ctx,
					"delete journal blobs",
					slog.String("stack", save.ref.String()),
					slog.String("update", save.id.String()),
				)
			}
		}
	}

	s.afterCheckpointSave(ctx, save)

	return nil
}

// checkJournalComplete refuses a complete with succeeded while the journaled
// update holds a recorded refusal or a gap in its stored sequence IDs. The
// update stays in progress and the stack stays locked; the message names the
// one command that ends it. A preview, an update that saves checkpoints, and
// any other end status need no check.
func (s *Updates) checkJournalComplete(ctx context.Context, ref Ref, id UpdateID, u Update) error {
	if !u.Mutates() || u.JournalVersion == 0 {
		return nil
	}

	if s.journal == nil {
		return fmt.Errorf("complete update %s of %s: %w", id, ref, ErrMissingDependency)
	}

	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return fmt.Errorf("complete update %s of %s: %w", id, ref, err)
	}

	incomplete, err := s.journal.Incomplete(ctx, st.ID, id)
	if err != nil {
		return fmt.Errorf("complete update %s of %s: %w", id, ref, err)
	}

	if incomplete {
		return fmt.Errorf("complete update %s of %s: %w", id, ref, ErrJournalIncomplete)
	}

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

	ns, err := s.writable(ctx, ref)
	if err != nil {
		return err
	}

	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return fmt.Errorf("build checkpoint of update %s: %w", id, err)
	}

	bl, err := s.blobs.scope(ns.ID, st.ID)
	if err != nil {
		return fmt.Errorf("build checkpoint of update %s: %w", id, err)
	}

	batches, err := s.journal.Batches(ctx, ns.ID, st.ID, id)
	if err != nil {
		return fmt.Errorf("build checkpoint of update %s: %w", id, err)
	}

	if len(batches) == 0 {
		return nil
	}

	built, err := s.buildJournalCheckpoint(ctx, bl, ref, u, batches)
	if err != nil {
		return err
	}

	return s.storeBuiltCheckpoint(
		ctx, builtSave{
			ref: ref, id: id, stackID: st.ID, fence: ns.Fence(),
			built: built, origin: u.Origin(), blobs: bl,
		},
	)
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

	bl, err := s.blobs.scope(st.NamespaceID, st.ID)
	if err != nil {
		return zero, false, fmt.Errorf("read journaled state of %s: %w", ref, err)
	}

	batches, err := s.journal.Batches(ctx, st.NamespaceID, st.ID, u.ID)
	if err != nil {
		return zero, false, fmt.Errorf("read journaled state of %s: %w", ref, err)
	}

	if len(batches) == 0 {
		return zero, false, nil
	}

	built, err := s.buildJournalCheckpoint(ctx, bl, ref, u, batches)
	if err != nil {
		return zero, false, err
	}

	return built, true, nil
}
