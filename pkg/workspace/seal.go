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
		} else if sealed {
			out.Sealed++

			affected[sv.Workspace] = struct{}{}
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

// sealOne seals one unsealed version. It reports whether the version ends
// sealed and whether it was skipped for a foreign home region, in that
// order. A version whose workspace is gone is skipped the same way: its blob
// stays for the existing sweep.
//
//nolint:revive // Unnamed two-boolean result per nonamedreturns; order is documented.
func (s *Service) sealOne(ctx context.Context, sv StateVersion) (bool, bool, error) {
	w, err := s.GetByID(ctx, sv.Workspace)
	if errors.Is(err, ErrNotFound) {
		s.logger.WarnContext(
			ctx,
			"skip seal of a missing workspace",
			slog.String("state_version", sv.ID.String()),
		)

		return false, true, nil
	}

	if err != nil {
		return false, false, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		if errors.Is(err, namespace.ErrNotHome) || errors.Is(err, namespace.ErrNotFound) {
			s.logger.WarnContext(
				ctx,
				"skip seal outside the home region",
				slog.String("state_version", sv.ID.String()),
				slog.String("namespace", w.Ref.Namespace.String()),
			)

			return false, true, nil
		}

		return false, false, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	oldKey := sv.Content

	key, sealErr := s.writeSealedBlob(ctx, w, sv)
	if sealErr != nil {
		return false, false, sealErr
	}

	sealedOutputs, outputsErr := s.sealRowOutputs(ctx, w, sv)
	if outputsErr != nil {
		return false, false, errors.Join(outputsErr, s.blobs.Delete(ctx, key))
	}

	sealErr = s.states.SealStateVersion(ctx, sv.ID, key, sealedOutputs, fence)
	if sealErr != nil {
		return false, false, errors.Join(
			fmt.Errorf("seal state %s: %w", sv.ID, sealErr),
			s.blobs.Delete(ctx, key),
		)
	}

	stored, err := s.states.GetStateVersion(ctx, sv.ID)
	if err != nil {
		return false, false, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	// Another run sealed the row first: the blob this run wrote is the
	// orphan. The committed row keeps the first seal.
	if stored.Content != key {
		return true, false, s.blobs.Delete(ctx, key)
	}

	if err = s.deleteBlobRetried(ctx, oldKey); err != nil {
		return false, false, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	return true, false, nil
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

	sealedReader, err := s.sealer.SealState(ctx, w.Ref.Namespace, w.ID.String(), counted)
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

	sealed, err := s.sealer.SealOutputs(ctx, w.Ref.Namespace, w.ID.String(), sv.Outputs)
	if err != nil {
		return nil, fmt.Errorf("seal state %s: %w", sv.ID, err)
	}

	return sealed, nil
}

// sweepSealedOrphans deletes the blobs under each sealed workspace that no
// version references: the sealed blob of a run that crashed before its SQL
// change, and the old blob of a run that crashed after it. The seal job runs
// at start-up and after a restore, while no upload streams, so every
// unreferenced blob under these workspaces is a seal leftover: unlike the
// declare sweep, this waits for no grace period.
func (s *Service) sweepSealedOrphans(ctx context.Context, workspaces map[ID]struct{}) error {
	for id := range workspaces {
		refs, err := s.states.ReferencedContentKeys(ctx, id)
		if err != nil {
			return fmt.Errorf("list referenced content of %s: %w", id, err)
		}

		named := make(map[blob.Key]struct{}, len(refs))

		for _, key := range refs {
			named[key] = struct{}{}
		}

		listed, err := s.blobs.List(ctx, "tofu/"+id.String()+"/")
		if err != nil {
			return fmt.Errorf("list state content of %s: %w", id, err)
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
