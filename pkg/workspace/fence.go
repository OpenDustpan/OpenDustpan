package workspace

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity.
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
)

// fenceLineagePrefix starts the lineage of a fence version. A fence changes the
// lineage, so a client that holds the old lineage fails its next write (ADR
// 44, decision 2).
const fenceLineagePrefix = "lin-"

// releaseWithFence releases the lock of w, which holds the lock, and makes a new
// fence version of the current state the current state (ADR 44, decision 1).
// The fence has a new lineage and the serial one more than the current serial.
// It keeps the outputs and the kept marks of cur, and seals the content when
// cur is sealed, through the data key of the scope. The store checks the lock,
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

	plain, err := s.openCurrentPlain(ctx, cur)
	if err != nil {
		return Workspace{}, fmt.Errorf("force-unlock %s: %w", w.Ref, err)
	}

	lineage := ident.NewID(fenceLineagePrefix)
	serial := cur.Serial + 1

	content, err := fenceContent(plain, lineage, serial)
	if err != nil {
		return Workspace{}, fmt.Errorf("force-unlock %s: %w", w.Ref, err)
	}

	sum := md5.Sum(content) //nolint:gosec // The TFE protocol names state by MD5 for integrity.
	now := s.clock()

	sv := StateVersion{
		CreatedAt:       now,
		FinalizedAt:     now,
		UploadExpiresAt: time.Time{},
		Upload:          token.Digest{},
		ID:              StateVersionID(ident.NewID("sv-")),
		Workspace:       w.ID,
		Holder:          "",
		LockID:          "",
		Lineage:         lineage,
		Status:          StatusFinalized,
		Content:         "",
		Outputs:         cur.Outputs,
		MD5:             sum,
		Serial:          serial,
		Size:            0,
		Force:           false,
		Sealed:          cur.Sealed,
		RestoredFrom:    "",
		SensitiveMarks:  cur.SensitiveMarks,
		FenceBy:         by,
	}

	key, err := attemptKey(sv)
	if err != nil {
		return Workspace{}, err
	}

	sv.Content = key

	size, err := s.store(ctx, storeInput{
		namespace: w.Ref.Namespace,
		workspace: w.ID,
		seal:      cur.Sealed,
		key:       key,
		want:      sum,
		content:   bytes.NewReader(content),
	})
	if err != nil {
		return Workspace{}, fmt.Errorf("force-unlock %s: %w", w.Ref, err)
	}

	sv.Size = size

	released := w
	released.Lock = Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	released.UpdatedAt = now
	released.Revision++

	if fenceErr := s.states.FenceAndUnlock(ctx, released, sv, fence); fenceErr != nil {
		return Workspace{}, s.fenceRefused(ctx, w, key, fenceErr)
	}

	return released, nil
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
		return nil, ErrClientEncrypted
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(plain, &doc); err != nil || doc == nil {
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

// fenceRefused removes the content of a fence that the store refused without
// writing it. An error with an unknown outcome keeps the content, because the
// version may have become current; the sweeper then owns it.
func (s *Service) fenceRefused(ctx context.Context, w Workspace, key blob.Key, err error) error {
	refused := errors.Is(err, ErrNotLocked) || errors.Is(err, ErrConcurrentModification) ||
		errors.Is(err, ErrStaleSerial) || errors.Is(err, ErrNotFound)
	if !refused {
		return fmt.Errorf("force-unlock %s: %w", w.Ref, err)
	}

	return fmt.Errorf("force-unlock %s: %w", w.Ref, errors.Join(err, s.blobs.Delete(ctx, key)))
}
