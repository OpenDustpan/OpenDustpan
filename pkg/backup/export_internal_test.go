package backup

import (
	"bytes"
	"context"
	"crypto"
	"log/slog"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// CheckObjectBytes runs the object check of one manifest entry on data: the
// hash function h, the hex sum and the size that the manifest records. The
// restore paths and backup check run the same check.
func CheckObjectBytes(h crypto.Hash, sum string, size int64, data []byte) error {
	check, err := newObjectCheck("test-object", objectWant{size: size, hash: h, sum: sum})
	if err != nil {
		return err
	}

	if _, err := check.Write(data); err != nil {
		return err
	}

	return check.verify("test-object")
}

// VerifyLiveBlobForTest runs verifyLiveBlob: it checks that the live store
// holds ref with the size and checksum of the manifest.
func VerifyLiveBlobForTest(ctx context.Context, live blob.Store, ref ManifestBlob) error {
	var s RestoreService
	s.blobs = live

	return s.verifyLiveBlob(ctx, ref)
}

// ProtectedKeyForTest runs the deny layer of the restore key check.
func ProtectedKeyForTest(key blob.Key) bool {
	return protectedKey(key)
}

// WritableKeyForTest runs the allowlist layer of the restore key check.
func WritableKeyForTest(key string) bool {
	return writableKey(key)
}

// CheckRestoreKeyForTest runs both layers of the restore key check.
func CheckRestoreKeyForTest(key string) error {
	return checkRestoreKey(key)
}

// NewAttemptPrefixForTest runs the constructor of the attempt prefix.
func NewAttemptPrefixForTest(id ID, attempt string) error {
	_, err := newAttemptPrefix(id, attempt)

	return err
}

// AttemptOwnsForTest reports whether key is under the attempt directory of
// backup id.
func AttemptOwnsForTest(id ID, attempt string, key blob.Key) (bool, error) {
	p, err := newAttemptPrefix(id, attempt)
	if err != nil {
		return false, err
	}

	return p.owns(key), nil
}

// MaxAttemptLengthForTest is the longest attempt name that the constructor
// accepts.
const MaxAttemptLengthForTest = maxAttemptLength

// MaxLivePointerBytesForTest is the size bound of the live pointer.
const MaxLivePointerBytesForTest = maxLivePointerBytes

// WriteLivePointerForTest runs writeLivePointer for the attempt directory of
// backup id through a target that checks the claim of check right before the
// put, as the finish step does.
func WriteLivePointerForTest(
	ctx context.Context, target blob.Store, id ID, attempt string, check func(context.Context) error,
) error {
	p, err := newAttemptPrefix(id, attempt)
	if err != nil {
		return err
	}

	return writeLivePointer(ctx, fencedStore{Store: target, claimCheck: check}, p)
}

// SeedLivePointerForTest puts the fixed bytes of a pointer that names attempt
// with no read and no claim check, so a test can seed any pointer state.
func SeedLivePointerForTest(ctx context.Context, target blob.Store, id ID, attempt string) error {
	p, err := newAttemptPrefix(id, attempt)
	if err != nil {
		return err
	}

	key, err := livePointerKey(id)
	if err != nil {
		return err
	}

	data, err := livePointerBytes(p.attempt)
	if err != nil {
		return err
	}

	return target.Put(ctx, key, bytes.NewReader(data))
}

// ReadLivePointerForTest runs the reader of the live pointer and returns the
// attempt that it names.
func ReadLivePointerForTest(ctx context.Context, target blob.Store, id ID) (string, error) {
	p, err := readLivePointer(ctx, target, id)
	if err != nil {
		return "", err
	}

	return p.attempt, nil
}

// ReadLiveForTest reads the live pointer of backup id with the size bound limit
// and returns the attempt that it names.
func ReadLiveForTest(ctx context.Context, target blob.Store, id ID, limit int64) (string, error) {
	p, err := readLivePointerLimit(ctx, target, id, limit)
	if err != nil {
		return "", err
	}

	return p.attempt, nil
}

// CheckAttemptForTest runs the finish check on the attempt directory of backup
// id, with no live pointer.
func CheckAttemptForTest(ctx context.Context, target blob.Store, id ID, attempt string) error {
	p, err := newAttemptPrefix(id, attempt)
	if err != nil {
		return err
	}

	return checkAttempt(ctx, target, p)
}

// NewFencedServiceForTest returns a service whose target is fenced by check:
// each put and delete reads check right before the call, as the write step
// does with the claim.
func NewFencedServiceForTest(target blob.Store, check func(context.Context) error) *Service {
	return &Service{
		jobs: nil, exporter: nil, kinds: nil, registry: nil, source: nil, opener: nil,
		target: fencedStore{Store: target, claimCheck: check}, targetName: "test",
		now: nil, version: "test", maxSQL: 0, maxUnverified: 0, runner: nil, operations: nil, lease: 0,
		logger: slog.New(slog.DiscardHandler),
	}
}

// PutForTest stores data under key through the fenced target of s.
func (s *Service) PutForTest(ctx context.Context, key blob.Key, data []byte) error {
	return s.target.Put(ctx, key, bytes.NewReader(data))
}

// ClearPartialForTest runs the clear of the write step on the attempt
// directory of backup id.
func (s *Service) ClearPartialForTest(ctx context.Context, id ID, attempt string) error {
	p, err := newAttemptPrefix(id, attempt)
	if err != nil {
		return err
	}

	return s.clearPartial(ctx, p)
}

// CleanupOrphansForTest runs the cleanup of the finish step with live as the
// attempt that live.json names.
func (s *Service) CleanupOrphansForTest(ctx context.Context, id ID, live string) error {
	p, err := newAttemptPrefix(id, live)
	if err != nil {
		return err
	}

	return s.cleanupOrphans(ctx, p)
}

// SetMaxUnverifiedKeysForTest sets the bound on the unverified keys of a
// backup of s to limit, so a test proves the refusal with few keys.
func SetMaxUnverifiedKeysForTest(s *Service, limit int) {
	s.maxUnverified = limit
}

// StepWriteForTest runs the write step of the backup operation op with fence.
// It is the write step that the durable runner runs: it writes the target
// under the attempt of the fence token and records that attempt on the row.
func (s *Service) StepWriteForTest(ctx context.Context, op durable.Operation, fence durable.StepFence) error {
	_, err := s.stepWrite(ctx, durable.StepContext{Operation: op, Fence: fence})

	return err
}

// StepFinishForTest runs the finish step of the backup operation op with
// fence, as the durable runner does.
func (s *Service) StepFinishForTest(ctx context.Context, op durable.Operation, fence durable.StepFence) error {
	_, err := s.stepFinish(ctx, durable.StepContext{Operation: op, Fence: fence})

	return err
}

// ClaimCheckForTest returns the point-of-use claim check of fence, so a test
// can prove that the real check refuses a stale claim and a cancel.
func (s *Service) ClaimCheckForTest(fence durable.StepFence) func(context.Context) error {
	return s.claimCheckOf(fence)
}
