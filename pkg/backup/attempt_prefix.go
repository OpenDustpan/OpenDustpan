package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// ErrInvalidAttempt reports an attempt name that is not valid in the key of a
// backup object.
var ErrInvalidAttempt = errors.New("invalid backup attempt")

const (
	// maxAttemptLength bounds an attempt name. The server mints the decimal
	// text of a claim fencing token, which is far shorter.
	maxAttemptLength = 64
)

// attemptPrefix is the directory backups/<id>/<attempt>/ of one run of the
// backup write step. Each target write of a run takes an attemptPrefix, so a
// stale run can name only its own directory. Only newAttemptPrefix builds it.
type attemptPrefix struct {
	id      ID
	attempt string
}

// ValidAttempt reports whether attempt is valid in the key of a backup
// object: 1 to 64 characters from a-z, 0-9, "-" and "_". It refuses any
// other input, also dot-dot and slash. The store allowlist and the prefix
// share it, so a row and a key refuse the same input.
func ValidAttempt(attempt string) error {
	if attempt == "" || len(attempt) > maxAttemptLength {
		return fmt.Errorf("%w: length %d", ErrInvalidAttempt, len(attempt))
	}

	for _, c := range []byte(attempt) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return fmt.Errorf("%w: character %q", ErrInvalidAttempt, c)
		}
	}

	return nil
}

// newAttemptPrefix returns the prefix of attempt for backup id. It accepts a
// backup ID that ParseID accepts and an attempt that ValidAttempt accepts.
func newAttemptPrefix(id ID, attempt string) (attemptPrefix, error) {
	if _, err := ParseID(id.String()); err != nil {
		return attemptPrefix{}, err
	}

	if err := ValidAttempt(attempt); err != nil {
		return attemptPrefix{}, err
	}

	return attemptPrefix{id: id, attempt: attempt}, nil
}

// dir returns "backups/<id>/<attempt>", the prefix for a list.
func (p attemptPrefix) dir() string {
	return "backups/" + p.id.String() + "/" + p.attempt
}

// key returns the key of the object path under the prefix.
func (p attemptPrefix) key(path string) (blob.Key, error) {
	key, err := blob.ParseKey(p.dir() + "/" + path)
	if err != nil {
		return "", fmt.Errorf("name object %q of backup %s: %w", path, p.id, err)
	}

	return key, nil
}

// manifestKey returns the key of the manifest of the attempt.
func (p attemptPrefix) manifestKey() (blob.Key, error) {
	return p.key("manifest.json")
}

// owns reports whether key is under the prefix.
func (p attemptPrefix) owns(key blob.Key) bool {
	return strings.HasPrefix(key.String(), p.dir()+"/")
}

// fencedStore is the attempt writer: it writes and deletes the objects of
// one attempt of a backup. claimCheck re-reads the claim right before each
// put and each delete under backups/, so a holder whose claim moved reaches
// no object. fencedBy sets it to the re-read of the claim token; a mutation
// test replaces it with a function that returns nil to prove that the check
// is the guard.
type fencedStore struct {
	blob.Store

	claimCheck func(ctx context.Context) error
}

// Put checks the claim, then stores r under key. A copy of a live blob under
// blobs/ is content-addressed: the copy keeps the first object and checks it
// against the live bytes, so a stale holder cannot change what a finished
// backup reads there. That put needs no read of the claim, which would cost one
// read for each blob. Every put under backups/ reads the claim.
func (f fencedStore) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if strings.HasPrefix(key.String(), "backups/") {
		if err := f.claimCheck(ctx); err != nil {
			return err
		}
	}

	if err := f.Store.Put(ctx, key, r); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	return nil
}

// Delete checks the claim, then removes key.
func (f fencedStore) Delete(ctx context.Context, key blob.Key) error {
	if err := f.claimCheck(ctx); err != nil {
		return err
	}

	if err := f.Store.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}

	return nil
}

// claimCheckOf returns the check of the claim that fence names. It reads the
// operation, so the check sees a claim that another instance took, and a
// cancel.
func (s *Service) claimCheckOf(fence durable.StepFence) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		op, err := s.operations.Get(ctx, fence.Operation)
		if err != nil {
			return fmt.Errorf("read the claim of backup operation %s: %w", fence.Operation, err)
		}

		switch {
		case op.State != durable.StateRunning || op.Claim.Token != fence.Token:
			return durable.ErrClaimLost
		case op.CancelRequested:
			return durable.ErrCanceled
		}

		return nil
	}
}

// fencedBy returns a copy of the service whose target checks the claim of
// fence before each put and each delete.
func (s *Service) fencedBy(fence durable.StepFence) *Service {
	fenced := *s
	fenced.target = fencedStore{Store: s.target, claimCheck: s.claimCheckOf(fence)}

	return &fenced
}

// cleanupOrphans deletes each attempt prefix of backup live.id except live.
// It runs only when the live pointer names live, so a target with no pointer
// or a pointer of another attempt deletes nothing, and it never deletes the
// prefix that the pointer names or the pointer itself. The target of s checks
// the claim before each delete, so a holder that lost the claim deletes
// nothing. A second call lists the parent again, so it removes a prefix that
// a stale writer recreated after the first call.
func (s *Service) cleanupOrphans(ctx context.Context, live attemptPrefix) error {
	// The pointer must name the row attempt before the first delete. A
	// pointer that differs from the row, or no pointer at all, stops the
	// cleanup: no delete runs, and the live holder of the row sorts it out.
	// Defense in depth: the attempt compare below would also stop a missing
	// pointer, since the zero attempt never equals the row. This check stays
	// so that a cleanup with no pointer fails with the read error, without
	// relying on the compare. It does not rely on the claim check.
	current, err := readLivePointer(ctx, s.target, live.id)
	if err != nil {
		return fmt.Errorf("cleanup of backup %s reads no live pointer: %w", live.id, err)
	}

	if current.attempt != live.attempt {
		return fmt.Errorf(
			"cleanup of backup %s stopped: the live pointer names attempt %q, the row records %q: %w",
			live.id, current.attempt, live.attempt, ErrCorrupt,
		)
	}

	parent := "backups/" + live.id.String() + "/"

	listed, err := listObjects(ctx, s.target, "backups/"+live.id.String())
	if err != nil {
		return fmt.Errorf("list objects of backup %s: %w", live.id, err)
	}

	for _, obj := range listed {
		rest, ok := strings.CutPrefix(obj.Key.String(), parent)
		if !ok {
			continue
		}

		attempt, _, nested := strings.Cut(rest, "/")
		if !nested {
			// The pointer and any object that is not in an attempt directory.
			continue
		}

		other, err := newAttemptPrefix(live.id, attempt)
		if err != nil {
			s.logger.WarnContext(ctx, "cleanup of a backup keeps a directory that is not an attempt",
				slog.String("backup", live.id.String()))

			continue
		}

		// Defense in depth: the listing skips nothing that matters, but this
		// check stays so that a change of the loop never deletes the live
		// prefix. It does not rely on the pointer or on the claim check.
		if other == live || live.owns(obj.Key) || !other.owns(obj.Key) {
			continue
		}

		if err := s.target.Delete(ctx, obj.Key); err != nil {
			return fmt.Errorf("delete object %s of backup %s: %w", obj.Key, live.id, err)
		}
	}

	return nil
}
