package workspace

import (
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"errors"
	"fmt"
	"io"
	"log/slog"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// SealResult counts one run of SealUnsealedStates: the versions it sealed
// and the versions it skipped because their namespace is not writable here.
type SealResult struct {
	Sealed  int
	Skipped int
}

// SealUnsealedStates seals every finalized state version that still holds
// plain content (ADR 41 decision 11): it writes the sealed content to a new
// blob key (a key is written once), moves the content key with the sealed
// outputs and the flag in one fenced transaction, then deletes the old blob.
// The outputs document of the row seals in the same transaction. The job is
// idempotent and resumes after a crash: a retry of an already sealed version
// changes nothing, and the sweep below removes a sealed blob that a crash
// left unreferenced. It seals only rows of namespaces that are writable here
// (the home region): it logs and skips the others with a count. A service
// without a sealer seals nothing and reports no versions. The result holds
// counts, errors hold version IDs: never a key, a scope or content.
func (s *Service) SealUnsealedStates(ctx context.Context) (SealResult, error) {
	var out SealResult

	if s.sealer == nil {
		return out, nil
	}

	pending, err := s.states.ListUnsealedStateVersions(ctx)
	if err != nil {
		return out, fmt.Errorf("list unsealed state: %w", err)
	}

	affected := make(map[ID]struct{})

	for i := range pending {
		sv := pending[i]

		sealed, skip, sealErr := s.sealOne(ctx, sv)
		if sealErr != nil {
			return out, sealErr
		}

		if skip {
			out.Skipped++
		} else {
			affected[sv.Workspace] = struct{}{}

			if sealed {
				out.Sealed++
			}
		}
	}

	if err := s.sweepSealedOrphans(ctx, affected); err != nil {
		return out, err
	}

	s.logger.InfoContext(
		ctx,
		"sealed unsealed state",
		slog.Int("sealed", out.Sealed),
		slog.Int("skipped", out.Skipped),
	)

	return out, nil
}

// sealOne seals one unsealed version. It reports whether this run sealed
// the version and whether it was skipped for a foreign home region, in that
// order. A version whose workspace is gone is skipped the same way: its blob
// stays for the existing sweep. A version that another instance deleted
// between the listing and this seal is skipped too: a version that is gone
// has nothing to seal, so the start-up never fails on it.
//
//nolint:revive // Unnamed two-boolean result per nonamedreturns; order is documented.
func (s *Service) sealOne(ctx context.Context, sv StateVersion) (bool, bool, error) {
	w, fence, skip, err := s.sealWorkspace(ctx, sv)
	if err != nil {
		return false, false, err
	}

	if skip {
		return false, true, nil
	}

	oldKey := sv.Content

	key, sealErr := s.writeSealedBlob(ctx, w, sv)
	if sealErr != nil {
		return s.skipPrunedBeforeRead(ctx, sv, sealErr)
	}

	sealedOutputs, outputsErr := s.sealRowOutputs(ctx, w, sv)
	if outputsErr != nil {
		return false, false, errors.Join(outputsErr, s.blobs.Delete(ctx, key))
	}

	skip, sealErr = s.commitSeal(ctx, sv.ID, key, sealedOutputs, fence)
	if sealErr != nil {
		return false, false, sealErr
	}

	if skip {
		return false, true, nil
	}

	stored, err := s.states.GetStateVersion(ctx, sv.ID)
	if err != nil {
		return false, false, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	// Another run sealed the row first: the blob this run wrote is the
	// orphan. The committed row keeps the first seal, so this run reports
	// nothing sealed: the count names sealed rows, not seal attempts.
	if stored.Content != key {
		return false, false, s.blobs.Delete(ctx, key)
	}

	if err = s.deleteBlobRetried(ctx, oldKey); err != nil {
		return false, false, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	return true, false, nil
}

// sealWorkspace returns the workspace of sv with the fence its seal writes
// carry. It reports whether the version was skipped for a missing workspace
// or a foreign home region: the row stays for the existing sweep.
//
//nolint:revive // Unnamed boolean skip result per nonamedreturns; order is documented.
func (s *Service) sealWorkspace(
	ctx context.Context, sv StateVersion,
) (Workspace, namespace.Fence, bool, error) {
	var none Workspace

	var noFence namespace.Fence

	w, err := s.GetByID(ctx, sv.Workspace)
	if errors.Is(err, ErrNotFound) {
		s.logger.WarnContext(
			ctx,
			"skip seal of a missing workspace",
			slog.String("state_version", sv.ID.String()),
		)

		return none, noFence, true, nil
	}

	if err != nil {
		return none, noFence, false, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		if errors.Is(err, namespace.ErrNotHome) || errors.Is(err, namespace.ErrNotFound) {
			s.logger.WarnContext(
				ctx,
				"skip seal outside the home region",
				slog.String("state_version", sv.ID.String()),
			)

			return none, noFence, true, nil
		}

		return none, noFence, false, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	return w, fence, false, nil
}

// skipPrunedBeforeRead maps the blob-read failure of a seal to a skip when a
// fresh read also misses the version row: another instance pruned the version
// with its blob between the listing and this seal. A missing blob with the
// row still present is data loss and stays loud. It reports whether this run
// sealed the version and whether it was skipped, in the order sealOne uses.
//
//nolint:revive // Unnamed two-boolean result per nonamedreturns; order is documented.
func (s *Service) skipPrunedBeforeRead(
	ctx context.Context, sv StateVersion, sealErr error,
) (bool, bool, error) {
	if !errors.Is(sealErr, blob.ErrNotFound) {
		return false, false, sealErr
	}

	_, getErr := s.states.GetStateVersion(ctx, sv.ID)
	if errors.Is(getErr, ErrStateVersionNotFound) {
		s.logger.WarnContext(
			ctx,
			"skip seal of a missing state version",
			slog.String("state_version", sv.ID.String()),
		)

		return false, true, nil
	}

	if getErr != nil {
		return false, false, errors.Join(sealErr, getErr)
	}

	return false, false, sealErr
}

// commitSeal moves the finalized unsealed version id to the sealed blob under
// content with the sealed outputs document. A version that another instance
// deleted between the listing and this seal has nothing to seal: it counts
// as skipped and the new blob goes as usual, so the start-up never fails on
// it. It reports whether the version was skipped.
func (s *Service) commitSeal(
	ctx context.Context, id StateVersionID, content blob.Key, outputs []byte, fence namespace.Fence,
) (bool, error) {
	sealErr := s.states.SealStateVersion(ctx, id, content, outputs, fence)
	if sealErr == nil {
		return false, nil
	}

	if errors.Is(sealErr, ErrStateVersionNotFound) {
		s.logger.WarnContext(
			ctx,
			"skip seal of a missing state version",
			slog.String("state_version", id.String()),
		)

		return true, s.blobs.Delete(ctx, content)
	}

	// Only a rejection before the commit (a stale fence or a missing
	// namespace) proves that no row names the new blob, so only then does
	// the blob go. Any other error may hide a commit with an unknown
	// outcome: the row may already name the new blob. Keep it then; the
	// idempotent retry and the orphan sweep of a later run reconcile it,
	// and the sweep deletes only a blob that no row names.
	if errors.Is(sealErr, namespace.ErrFenced) || errors.Is(sealErr, namespace.ErrNotFound) {
		return false, errors.Join(
			fmt.Errorf("seal state %s: %w", id, sealErr),
			s.blobs.Delete(ctx, content),
		)
	}

	return false, fmt.Errorf("seal state %s: %w", id, sealErr)
}

// writeSealedBlob writes the sealed content of sv to a new blob key and
// returns the key. The size and MD5 stay those of the plain bytes: a blob
// that no longer matches the row fails instead of sealing changed bytes.
// The caller deletes the returned key when the seal does not commit.
func (s *Service) writeSealedBlob(
	ctx context.Context, w Workspace, sv StateVersion,
) (blob.Key, error) {
	old, err := s.blobs.Get(ctx, sv.Content)
	if err != nil {
		return "", fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	key, err := attemptKey(sv)
	if err != nil {
		return "", errors.Join(fmt.Errorf("seal state %s: %w", sv.ID, err), old.Close())
	}

	hash := md5.New() //nolint:gosec // Required by the TFE protocol; integrity only.
	counted := &countingReader{r: io.TeeReader(old, hash), n: 0}

	sealedReader, err := s.sealer.SealState(ctx, w.Ref.Namespace, w.ID.String(), string(sv.ID), counted)
	if err != nil {
		return "", errors.Join(fmt.Errorf("seal state %s: %w", sv.ID, err), old.Close())
	}

	putErr := s.blobs.Put(ctx, key, sealedReader)
	closeErr := old.Close()

	if putErr != nil {
		return "", errors.Join(fmt.Errorf("seal state %s: %w", sv.ID, putErr), closeErr)
	}

	if closeErr != nil {
		return "", errors.Join(
			fmt.Errorf("seal state %s: %w", sv.ID, closeErr),
			s.blobs.Delete(ctx, key),
		)
	}

	if counted.n != sv.Size || [md5.Size]byte(hash.Sum(nil)) != sv.MD5 {
		return "", errors.Join(
			fmt.Errorf("seal state %s: %w", sv.ID, ErrChecksumMismatch),
			s.blobs.Delete(ctx, key),
		)
	}

	return key, nil
}

// sealRowOutputs seals the outputs document of sv with the data key of its
// scope. A row without outputs keeps them as they are.
func (s *Service) sealRowOutputs(
	ctx context.Context, w Workspace, sv StateVersion,
) ([]byte, error) {
	if len(sv.Outputs) == 0 {
		return sv.Outputs, nil
	}

	sealed, err := s.sealer.SealOutputs(ctx, w.Ref.Namespace, w.ID.String(), string(sv.ID), sv.Outputs)
	if err != nil {
		return nil, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	return sealed, nil
}

// sweepSealedOrphans deletes the blobs under each sealed workspace that no
// version references: the sealed blob of a run that crashed before its SQL
// change, and the old blob of a run that crashed after it. Another instance
// may record a new upload attempt and put its blob while this sweep runs, so
// the sweep lists the blobs before reading the referenced keys: the upload
// path names the row before the bytes land, so a reference read after the
// listing names every listed blob of a live attempt. The reverse order could
// list a live blob that the earlier reference read does not name yet and
// delete a live upload. Unlike the declare sweep, this waits for no grace
// period: every listed blob that the later reference read does not name is a
// seal leftover.
func (s *Service) sweepSealedOrphans(ctx context.Context, workspaces map[ID]struct{}) error {
	for id := range workspaces {
		// Order matters: list first, then read the references (see above).
		listed, err := s.blobs.List(ctx, "tofu/"+id.String()+"/")
		if err != nil {
			return fmt.Errorf("list state content of %s: %w", id, err)
		}

		refs, err := s.states.ReferencedContentKeys(ctx, id)
		if err != nil {
			return fmt.Errorf("list referenced content of %s: %w", id, err)
		}

		named := make(map[blob.Key]struct{}, len(refs))

		for _, key := range refs {
			named[key] = struct{}{}
		}

		for i := range listed {
			if _, ok := named[listed[i].Key]; ok {
				continue
			}

			if err := s.deleteBlobRetried(ctx, listed[i].Key); err != nil {
				return fmt.Errorf("delete unreferenced content of %s: %w", id, err)
			}
		}
	}

	return nil
}
