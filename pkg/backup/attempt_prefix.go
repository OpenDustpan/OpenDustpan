package backup

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// maxAttemptLen bounds the decimal form of a claim fencing token. A token is
// an int64, so 20 digits always hold it.
const maxAttemptLen = 20

// attemptPrefix is the target prefix of one backup attempt:
// backups/<id>/<attempt>/. Every target write of a backup goes through it,
// so a stale holder addresses only its own attempt prefix and can never name
// the prefix of the survivor. The type is unexported and the only constructor
// checks the allowlist, so no caller builds an attempt key by joining
// strings.
type attemptPrefix struct {
	id      ID
	attempt string
}

// newAttemptPrefix returns the attempt prefix of backup id and attempt. The
// attempt is the decimal fencing token of the claim that runs the backup.
// Both parts pass an allowlist: the id parses as a backup ID, and the
// attempt holds 1 to 20 ASCII digits only, so dot-dot and slash never reach
// the target.
func newAttemptPrefix(id ID, attempt string) (attemptPrefix, error) {
	if _, err := ParseID(id.String()); err != nil {
		return attemptPrefix{}, err
	}

	if attempt == "" || len(attempt) > maxAttemptLen {
		return attemptPrefix{}, fmt.Errorf("invalid backup attempt %q: %w", attempt, ErrInvalidID)
	}

	for _, r := range attempt {
		if r < '0' || r > '9' {
			return attemptPrefix{}, fmt.Errorf("invalid backup attempt %q: %w", attempt, ErrInvalidID)
		}
	}

	return attemptPrefix{id: id, attempt: attempt}, nil
}

// attemptOf returns the attempt of the claim that fence carries: the decimal
// form of its fencing token. The token grows on each takeover, so each
// holder writes a new prefix.
func attemptOf(fence durable.StepFence) string {
	return strconv.FormatInt(fence.Token, 10)
}

// prefix returns the target prefix of the attempt with its trailing slash.
func (p attemptPrefix) prefix() string {
	return "backups/" + p.id.String() + "/" + p.attempt + "/"
}

// manifestKey returns the key of the manifest of the attempt.
func (p attemptPrefix) manifestKey() (blob.Key, error) {
	key, err := blob.ParseKey(p.prefix() + "manifest.json")
	if err != nil {
		return "", fmt.Errorf("name manifest of backup %s: %w", p.id, err)
	}

	return key, nil
}

// join returns the key of the table file path under the attempt. It refuses
// an empty path, an absolute path and a path that climbs out of the prefix,
// so a hostile descriptor never addresses another prefix.
func (p attemptPrefix) join(path string) (blob.Key, error) {
	if path == "" || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("name table file %q of backup %s: %w", path, p.id, ErrCorrupt)
	}

	for seg := range strings.SplitSeq(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("name table file %q of backup %s: %w", path, p.id, ErrCorrupt)
		}
	}

	key, err := blob.ParseKey(p.prefix() + path)
	if err != nil {
		return "", fmt.Errorf("name table file %q of backup %s: %w", path, p.id, err)
	}

	return key, nil
}

// liveAttempt returns the attempt of the live claim of backup id: the decimal
// token of the claim of its durable operation. It reports an error that wraps
// durable.ErrClaimLost when no operation holds a live claim for the backup,
// so a stale holder stops before it touches the target.
func (s *Service) liveAttempt(ctx context.Context, id ID) (string, error) {
	ops, err := s.operations.List(ctx, durable.Filter{Kind: OperationKind, States: nil, Limit: 0})
	if err != nil {
		return "", fmt.Errorf("read the live claim of backup %s: %w", id, err)
	}

	key := OperationKey(id)

	for i := range ops {
		if ops[i].Key != key {
			continue
		}

		if ops[i].Claim.Token == 0 {
			return "", fmt.Errorf("backup %s holds no live claim: %w", id, durable.ErrClaimLost)
		}

		return strconv.FormatInt(ops[i].Claim.Token, 10), nil
	}

	return "", fmt.Errorf("backup %s holds no live claim: %w", id, durable.ErrClaimLost)
}

// checkClaim refuses the target call unless fence still carries the live
// claim of backup id. Every Put, Delete and manifest write of the backup
// calls it right before the target call, so a holder that lost its claim
// writes nothing more, not even under its own prefix.
func (s *Service) checkClaim(ctx context.Context, fence durable.StepFence, id ID) error {
	live, err := s.liveAttempt(ctx, id)
	if err != nil {
		return err
	}

	if live != attemptOf(fence) {
		return fmt.Errorf("backup %s attempt %s is not the live claim: %w", id, attemptOf(fence),
			durable.ErrClaimLost)
	}

	return nil
}
