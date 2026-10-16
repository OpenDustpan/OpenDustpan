package workspace

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity.
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// fenceLineagePrefix starts the lineage of a fence version. A fence changes the
// lineage, so a client that holds the old lineage fails its next write (ADR
// 44, decision 2).
const fenceLineagePrefix = "lin-"

// releaseWithFence releases the lock of w, which holds the lock, and makes a new
// fence version of the current state the current state (ADR 44, decision 1).
// The fence has a new lineage and the serial one more than the current serial.
// It keeps the outputs and the kept marks of cur. A service with a sealer seals
// the content and the outputs through the data key of the scope, also when cur
// is plain, so a fence never writes new plain state. The store checks the lock,
// the revision and the serial in the transaction that writes the fence.
func (s *Service) releaseWithFence(
	ctx context.Context, w Workspace, cur StateVersion, by string,
) (Workspace, error) {
	if by == "" {
		return Workspace{}, fmt.Errorf("force-unlock %s: %w: no principal", w.Ref, ErrInvalidState)
	}

	if cur.Sealed && s.sealer == nil {
		return Workspace{}, fmt.Errorf("force-unlock %s: %w", w.Ref, ErrSealerNotConfigured)
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return Workspace{}, err
	}

	sv, content, err := s.buildFence(ctx, w, cur, by)
	if err != nil {
		return Workspace{}, fmt.Errorf("force-unlock %s: %w", w.Ref, err)
	}

	// The pending row names the key before the bytes land, so no sweep
	// deletes the blob before the commit names it. When the commit outcome
	// stays unknown, the expired-version sweep deletes the row and the blob.
	// A force-unlock is the way out of a stuck lock, so the pending cap does
	// not stop it.
	if stageErr := s.states.CreateStateVersion(ctx, sv, fence, math.MaxInt); stageErr != nil {
		return Workspace{}, fmt.Errorf("force-unlock %s: %w", w.Ref, stageErr)
	}

	size, err := s.store(ctx, storeInput{
		namespace: w.Ref.Namespace,
		workspace: w.ID,
		seal:      sv.Sealed,
		key:       sv.Content,
		want:      sv.MD5,
		content:   bytes.NewReader(content),
		unchecked: false,
	})
	if err != nil {
		return Workspace{}, fmt.Errorf("force-unlock %s: %w", w.Ref, errors.Join(err, s.dropFence(ctx, sv, fence)))
	}

	sv.Status = StatusFinalized
	sv.FinalizedAt = sv.CreatedAt
	sv.Size = size

	released := w
	released.Lock = Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	released.UpdatedAt = sv.FinalizedAt
	released.Revision++

	if fenceErr := s.states.FenceAndUnlock(ctx, released, sv, cur.ID, fence); fenceErr != nil {
		return Workspace{}, s.fenceRefused(ctx, w, sv, fence, fenceErr)
	}

	return released, nil
}

// buildFence returns the pending fence version of w that follows cur, and the
// plain content it holds. The version has a new lineage and the next serial,
// keeps the outputs and the kept marks of cur, and names by. Its content key
// is set, and its MD5 is that of the plain content. It is sealed when the
// service seals; the outputs of a plain cur are sealed for it. It has no
// upload capability, so no client uploads into it.
func (s *Service) buildFence(
	ctx context.Context, w Workspace, cur StateVersion, by string,
) (StateVersion, []byte, error) {
	plain, err := s.openCurrentPlain(ctx, cur)
	if err != nil {
		return StateVersion{}, nil, err
	}

	lineage := ident.NewID(fenceLineagePrefix)

	content, err := fenceContent(plain, lineage, cur.Serial+1)
	if err != nil {
		return StateVersion{}, nil, err
	}

	sealed := s.sealer != nil

	outputs, err := s.fenceOutputs(ctx, w, cur)
	if err != nil {
		return StateVersion{}, nil, err
	}

	now := s.clock()

	sv := StateVersion{
		CreatedAt:       now,
		FinalizedAt:     time.Time{},
		UploadExpiresAt: now.Add(UploadWindow),
		Upload:          token.Digest{},
		ID:              StateVersionID(ident.NewID("sv-")),
		Workspace:       w.ID,
		Holder:          "",
		LockID:          "",
		Lineage:         lineage,
		Status:          StatusPending,
		Content:         "",
		Outputs:         outputs,
		MD5:             md5.Sum(content), //nolint:gosec // The TFE protocol names state by MD5 for integrity.
		Serial:          cur.Serial + 1,
		Size:            0,
		Force:           false,
		Sealed:          sealed,
		RestoredFrom:    "",
		SensitiveMarks:  cur.SensitiveMarks,
		FenceBy:         by,
		UnlockedWrite:   false,
	}

	key, err := attemptKey(sv)
	if err != nil {
		return StateVersion{}, nil, err
	}

	sv.Content = key

	return sv, content, nil
}

// fenceOutputs returns the stored outputs document of the fence that follows
// cur: the outputs of cur as they are, or sealed with the data key of the
// workspace scope when the service seals and cur is plain.
func (s *Service) fenceOutputs(ctx context.Context, w Workspace, cur StateVersion) ([]byte, error) {
	if s.sealer == nil || cur.Sealed || len(cur.Outputs) == 0 {
		return cur.Outputs, nil
	}

	out, err := s.sealer.SealOutputs(ctx, w.Ref.Namespace, w.ID.String(), cur.Outputs)
	if err != nil {
		return nil, fmt.Errorf("seal outputs of %s: %w", w.Ref, err)
	}

	return out, nil
}

// openCurrentPlain returns the plain content of the current version. A sealed
// version opens through the sealer, so the fence never reads ciphertext.
func (s *Service) openCurrentPlain(ctx context.Context, cur StateVersion) ([]byte, error) {
	plain, _, err := s.openStateBytes(ctx, cur.ID)
	if err != nil {
		return nil, err
	}

	return plain, nil
}

// fenceContent sets the lineage and the serial of a state document and leaves
// the rest of it as it is. A client-encrypted state fails with
// ErrClientEncrypted: the server cannot set its lineage, so the force-unlock
// is refused and the lock stays.
func fenceContent(plain []byte, lineage string, serial int64) ([]byte, error) {
	encrypted, err := IsClientEncrypted(plain)
	if err != nil {
		return nil, err
	}

	if encrypted {
		return nil, fmt.Errorf(
			"%w: The state is encrypted by the client, so the server cannot fence it. "+
				"Let the run that holds the lock finish; it releases the lock",
			ErrClientEncrypted,
		)
	}

	var doc map[string]json.RawMessage
	if decodeErr := json.Unmarshal(plain, &doc); decodeErr != nil || doc == nil {
		return nil, fmt.Errorf("%w: state is not a JSON object", ErrInvalidState)
	}

	rawLineage, err := json.Marshal(lineage)
	if err != nil {
		return nil, fmt.Errorf("encode lineage: %w", err)
	}

	rawSerial, err := json.Marshal(serial)
	if err != nil {
		return nil, fmt.Errorf("encode serial: %w", err)
	}

	doc["lineage"] = rawLineage
	doc["serial"] = rawSerial

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode state: %w", err)
	}

	return out, nil
}

// fenceRefused removes the content and the pending row of a fence that the
// store refused without writing it. An error with an unknown outcome keeps
// both, because the version may have become current; the expired-version
// sweep then owns them.
func (s *Service) fenceRefused(
	ctx context.Context, w Workspace, sv StateVersion, fence namespace.Fence, err error,
) error {
	refused := errors.Is(err, ErrNotLocked) || errors.Is(err, ErrConcurrentModification) ||
		errors.Is(err, ErrStaleSerial) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUploadDenied)
	if !refused {
		return fmt.Errorf("force-unlock %s: %w", w.Ref, err)
	}

	return fmt.Errorf("force-unlock %s: %w", w.Ref, errors.Join(err, s.dropFence(ctx, sv, fence)))
}

// dropFence deletes the content of the pending fence sv, then its row. When
// the delete fails, the row stays, so the expired-version sweep still finds
// the blob.
func (s *Service) dropFence(ctx context.Context, sv StateVersion, fence namespace.Fence) error {
	if err := s.blobs.Delete(ctx, sv.Content); err != nil {
		return err
	}

	return s.states.DiscardStateVersion(ctx, sv.ID, fence)
}
