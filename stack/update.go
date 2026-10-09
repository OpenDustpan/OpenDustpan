package stack

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
)

// Sentinel errors of the update cycle, returned by Updates and by the update,
// checkpoint and event stores.
var (
	// ErrUpdateNotFound reports that an update does not exist.
	ErrUpdateNotFound = errors.New("update not found")
	// ErrUpdateConflict reports that another update of the stack is running.
	ErrUpdateConflict = errors.New("another update of this stack is in progress")
	// ErrInvalidUpdate reports an unknown kind or status, or an out-of-range value.
	ErrInvalidUpdate = errors.New("invalid update request")
	// ErrWrongUpdateState reports an operation the update's status does not allow,
	// such as completing an update that never started.
	ErrWrongUpdateState = errors.New("update does not allow this in its current state")
	// ErrLeaseInvalid reports a lease token that does not belong to the update.
	ErrLeaseInvalid = errors.New("invalid update lease token")
	// ErrLeaseExpired reports a lease that ran out before it was renewed.
	ErrLeaseExpired = errors.New("update lease expired")
	// ErrConcurrentModification reports that the update changed between read and write.
	ErrConcurrentModification = errors.New("update changed concurrently")
	// ErrStaleCheckpoint reports a checkpoint whose sequence number is older than
	// the last one saved for the update.
	ErrStaleCheckpoint = errors.New("checkpoint is older than the last one saved")
	// ErrCheckpointConflict reports a retried checkpoint whose content differs
	// from the one saved under the same sequence number.
	ErrCheckpointConflict = errors.New("checkpoint differs from the one saved with the same sequence number")
	// ErrNoState reports a stack that has never had a checkpoint.
	ErrNoState = errors.New("stack has no state")
	// ErrTooLarge reports a checkpoint or event batch over its size limit.
	ErrTooLarge = errors.New("payload too large")
)

const (
	// DefaultLease is how long a lease lasts when an update starts.
	DefaultLease = 5 * time.Minute
	// MaxLease is the longest renewal a client may ask for (the CLI asks for at most 300 seconds).
	MaxLease = 5 * time.Minute

	maxUpdateIDLength = 64
)

// UpdateKind is what an update does to a stack.
type UpdateKind string

// Update kinds, as the Pulumi CLI names them.
const (
	KindUpdate         UpdateKind = "update"
	KindPreview        UpdateKind = "preview"
	KindRefresh        UpdateKind = "refresh"
	KindDestroy        UpdateKind = "destroy"
	KindImport         UpdateKind = "import"
	KindResourceImport UpdateKind = "resource-import"
)

// ParseUpdateKind validates s as an update kind.
func ParseUpdateKind(s string) (UpdateKind, error) {
	switch k := UpdateKind(s); k {
	case KindUpdate, KindPreview, KindRefresh, KindDestroy, KindImport, KindResourceImport:
		return k, nil
	default:
		return "", fmt.Errorf("%w: kind %q", ErrInvalidUpdate, s)
	}
}

// Mutates reports whether the kind may change the stack's state. Only one
// mutating update runs per stack at a time; previews never block or wait.
func (k UpdateKind) Mutates() bool {
	return k != KindPreview
}

// UpdateStatus is where an update is in its lifecycle.
type UpdateStatus string

// Update statuses. The last three are terminal.
const (
	StatusNotStarted UpdateStatus = "not started"
	StatusRunning    UpdateStatus = "running"
	StatusSucceeded  UpdateStatus = "succeeded"
	StatusFailed     UpdateStatus = "failed"
	StatusCancelled  UpdateStatus = "cancelled" //nolint:misspell // The CLI's wire value; it must not change.
)

// ParseCompletion validates s as the status a client may complete an update with.
func ParseCompletion(s string) (UpdateStatus, error) {
	switch st := UpdateStatus(s); st {
	case StatusSucceeded, StatusFailed, StatusCancelled:
		return st, nil
	case StatusNotStarted, StatusRunning:
		return "", fmt.Errorf("%w: %q is not a final status", ErrInvalidUpdate, s)
	default:
		return "", fmt.Errorf("%w: status %q", ErrInvalidUpdate, s)
	}
}

// Terminal reports whether the status is final.
func (s UpdateStatus) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// UpdateID identifies an update. It is opaque to clients.
type UpdateID string

// ParseUpdateID validates s as an update ID.
func ParseUpdateID(s string) (UpdateID, error) {
	if !ident.Valid(s, maxUpdateIDLength) {
		return "", fmt.Errorf("%w: update ID %q", ErrInvalidUpdate, s)
	}

	return UpdateID(s), nil
}

// String returns the ID as a plain string.
func (id UpdateID) String() string {
	return string(id)
}

// Lease authorizes writes to a running update until ExpiresAt. Only the
// digest of its token is kept.
type Lease struct {
	ExpiresAt time.Time
	Digest    token.Digest
}

// check reports whether tok holds the lease at now.
func (l Lease) check(tok token.Token, now time.Time) error {
	if !l.Digest.Matches(tok) {
		return ErrLeaseInvalid
	}

	if !now.Before(l.ExpiresAt) {
		return fmt.Errorf("%w at %s", ErrLeaseExpired, l.ExpiresAt.Format(time.RFC3339))
	}

	return nil
}

// Update is one run of the Pulumi engine against a stack.
//
// An update is created "not started", becomes "running" when its client starts
// it and receives a lease, and ends succeeded, failed or canceled. A
// running update whose lease expired stays running, and keeps the stack
// locked, until someone cancels it: the server cannot know whether the engine
// is still changing resources.
type Update struct {
	CreatedAt time.Time
	StartedAt time.Time
	EndedAt   time.Time
	Lease     Lease
	ID        UpdateID
	Kind      UpdateKind
	Status    UpdateStatus
	Ref       Ref
	// Version is the stack version this update produces, assigned by the store
	// when it starts. A preview carries the stack's current version.
	Version int64
	// Revision increases with every change. Stores accept a write only if they
	// hold Revision-1, which makes every transition a compare-and-swap.
	Revision int64
}

// start moves a not-started update to running under lease.
func (u Update) start(now time.Time, lease Lease) (Update, error) {
	if u.Status != StatusNotStarted {
		return Update{}, fmt.Errorf("%w: start %s update", ErrWrongUpdateState, u.Status)
	}

	u.Status = StatusRunning
	u.StartedAt = now
	u.Lease = lease
	u.Revision++

	return u, nil
}

// authorize reports whether tok may write to the update at now.
func (u Update) authorize(tok token.Token, now time.Time) error {
	if u.Status != StatusRunning {
		return fmt.Errorf("%w: write to %s update", ErrWrongUpdateState, u.Status)
	}

	return u.Lease.check(tok, now)
}

// renew extends the lease to now+d. The token stays the same, so writes in
// flight during a renewal do not fail.
func (u Update) renew(tok token.Token, now time.Time, d time.Duration) (Update, error) {
	if d < time.Second || d > MaxLease {
		return Update{}, fmt.Errorf("%w: lease duration %s, want 1s to %s", ErrInvalidUpdate, d, MaxLease)
	}

	if err := u.authorize(tok, now); err != nil {
		return Update{}, err
	}

	u.Lease.ExpiresAt = now.Add(d)
	u.Revision++

	return u, nil
}

// complete ends a running update with a final status reported by its client.
func (u Update) complete(tok token.Token, now time.Time, status UpdateStatus) (Update, error) {
	if !status.Terminal() {
		return Update{}, fmt.Errorf("%w: %q is not a final status", ErrInvalidUpdate, status)
	}

	if err := u.authorize(tok, now); err != nil {
		return Update{}, err
	}

	return u.end(now, status), nil
}

// cancel ends an update on a user's request, without its lease: this is how a
// stack locked by a crashed client is released.
func (u Update) cancel(now time.Time) (Update, error) {
	if u.Status.Terminal() {
		return Update{}, fmt.Errorf("%w: cancel %s update", ErrWrongUpdateState, u.Status)
	}

	return u.end(now, StatusCancelled), nil
}

func (u Update) end(now time.Time, status UpdateStatus) Update {
	u.Status = status
	u.EndedAt = now
	u.Lease = Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}}
	u.Revision++

	return u
}

// Checkpoint is a stack's state as the engine saved it during an update.
// Deployment is opaque JSON in the schema named by SchemaVersion; the server
// never interprets it, so newer CLIs can store formats this server predates.
type Checkpoint struct {
	SavedAt    time.Time
	Features   []string
	Deployment []byte
	Update     UpdateID
	Ref        Ref
	// SchemaVersion is the deployment format version, 1 or higher.
	SchemaVersion int
	// Sequence is the client's idempotency counter for verbatim checkpoints, or
	// 0 when the client sends none.
	Sequence int64
	// Digest is the SHA-256 of Deployment, used to recognize retried writes.
	Digest [sha256.Size]byte
}

// CheckpointInput is a checkpoint as a client submits it.
type CheckpointInput struct {
	Features      []string
	Deployment    []byte
	SchemaVersion int
	Sequence      int64
}

// Event is one engine event of an update, kept for the console and history.
// Payload is opaque JSON.
type Event struct {
	Timestamp time.Time
	Payload   []byte
	// Sequence orders events within an update; retried events repeat it.
	Sequence int64
}

// Page selects a window of a newest-first list.
type Page struct {
	Limit  int
	Offset int
}
