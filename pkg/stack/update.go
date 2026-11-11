package stack

import (
	"crypto/md5" //nolint:gosec // The backup manifest records MD5 of stored bytes, for integrity, not security.
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
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
	// ErrAccessTokenUnchecked reports a lease request for an update that an access
	// token started, when no token checker is wired (fail closed).
	ErrAccessTokenUnchecked = errors.New("access token of update cannot be checked")
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
	// ErrCheckpointUnavailable reports that the checkpoint content in the blob
	// store cannot be read or written. The error carries no blob key: keys
	// never appear in responses or logs.
	ErrCheckpointUnavailable = errors.New("checkpoint content is unavailable")
	// ErrCheckpointMismatch reports stored checkpoint content that does not
	// match its record: a blob with other content, or a key the record cannot
	// parse. The error carries no blob key.
	ErrCheckpointMismatch = errors.New("checkpoint content differs from its record")
	// ErrCheckpointNoKey reports a checkpoint write without a blob key. The
	// store keeps checkpoint content only in the blob store, so it refuses
	// such a write.
	ErrCheckpointNoKey = errors.New("checkpoint has no blob key")
	// ErrCheckpointKey reports a checkpoint write whose blob key does not
	// belong to the namespace, the stack and the update of the row. The error
	// carries no blob key.
	ErrCheckpointKey = errors.New("checkpoint blob key does not belong to the update")
	// ErrInvalidDelta reports a checkpoint delta the server does not apply:
	// no checkpoint to apply it to, a sequence number that is not next, an
	// edit outside the text, overlapping edits, a result over the size
	// limit, or a hash that does not match. The CLI answers such a delta
	// with a full save, so this always maps to a 409 conflict.
	ErrInvalidDelta = errors.New("invalid checkpoint delta")
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
	// KindRestore marks an update that copies the checkpoint of an older
	// stack version into a new one. A restore mutates: it takes a version,
	// holds the stack lock and appears in history. Issue #139 checks this
	// wire value against the supported CLI versions.
	KindRestore UpdateKind = "restore"
)

// ParseUpdateKind validates s as an update kind.
func ParseUpdateKind(s string) (UpdateKind, error) {
	switch k := UpdateKind(s); k {
	case KindUpdate, KindPreview, KindRefresh, KindDestroy, KindImport, KindResourceImport, KindRestore:
		return k, nil
	default:
		return "", fmt.Errorf("%w: kind %q", ErrInvalidUpdate, s)
	}
}

// Plan is what a client asks to run: a kind, whether it is a dry run, who
// asked, and for a restore which stack version it copies. The CLI runs the
// preview phase of `pulumi up`, `refresh` and `destroy` with the real kind
// and DryRun set, so the kind alone does not say whether an update changes
// state. CreatedBy is the subject of the principal that asked; it stays
// empty when the caller gives none. RestoredFrom is 0 unless Kind is
// KindRestore. GitHead is the commit the client reported in its metadata.
type Plan struct {
	Kind         UpdateKind
	DryRun       bool
	CreatedBy    string
	GitHead      string
	RestoredFrom int64
}

// UpdateStatus is where an update is in its lifecycle.
type UpdateStatus string

// Update statuses. The last three are terminal.
const (
	StatusNotStarted UpdateStatus = "not started"
	StatusRunning    UpdateStatus = "running"
	StatusSucceeded  UpdateStatus = "succeeded"
	StatusFailed     UpdateStatus = "failed"
	StatusCanceled   UpdateStatus = "cancelled" //nolint:misspell // The CLI's wire value; it must not change.
)

// ParseCompletion validates s as the status a client may complete an update with.
func ParseCompletion(s string) (UpdateStatus, error) {
	switch st := UpdateStatus(s); st {
	case StatusSucceeded, StatusFailed, StatusCanceled:
		return st, nil
	case StatusNotStarted, StatusRunning:
		return "", fmt.Errorf("%w: %q is not a final status", ErrInvalidUpdate, s)
	default:
		return "", fmt.Errorf("%w: status %q", ErrInvalidUpdate, s)
	}
}

// Terminal reports whether the status is final.
func (s UpdateStatus) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCanceled
}

// UpdateID identifies an update. It is opaque to clients.
type UpdateID string

// UpdateConflictError reports the mutating update that runs on the stack and
// blocks the call. It matches ErrUpdateConflict. The store fills it in the
// transaction that finds the running update, so the caller needs no second
// read to name that update.
type UpdateConflictError struct {
	// Active is the ID of the mutating update that runs on the stack.
	Active UpdateID
}

// Error returns the sentence of ErrUpdateConflict and the ID of the update
// that runs on the stack. An ID that ParseUpdateID refuses is left out.
func (e *UpdateConflictError) Error() string {
	// Defense in depth: RunningUpdateConflict parses the ID too; this check
	// stays so that an error that a caller builds by hand never puts an ID
	// that is not valid into the text of an answer.
	if _, err := ParseUpdateID(string(e.Active)); err != nil {
		return ErrUpdateConflict.Error()
	}

	return fmt.Sprintf("%s (update %s)", ErrUpdateConflict, e.Active)
}

// RunningUpdateConflict returns the error of a call that a running mutating
// update blocks, for the ID that a store read from the row of that update.
// It returns a *UpdateConflictError that names the update when ParseUpdateID
// accepts id. For any other id it returns an error that matches
// ErrUpdateConflict and names no update: a row from a restore or from another
// version can hold any text, and the text of the error reaches the client.
func RunningUpdateConflict(id string) error {
	active, err := ParseUpdateID(id)
	if err != nil {
		return fmt.Errorf("%w (the running update has an ID that is not valid)", ErrUpdateConflict)
	}

	return &UpdateConflictError{Active: active}
}

// Unwrap returns ErrUpdateConflict.
func (*UpdateConflictError) Unwrap() error {
	return ErrUpdateConflict
}

// ParseUpdateID validates s as an update ID.
func ParseUpdateID(s string) (UpdateID, error) {
	if !ident.Valid(s, maxUpdateIDLength) {
		return "", fmt.Errorf("%w: update ID %q", ErrInvalidUpdate, s)
	}

	return UpdateID(s), nil
}

// ParseStackVersion validates s as a stack version in a request path: a
// non-negative integer. Whether that version exists is decided later, when
// its checkpoint is read.
func ParseStackVersion(s string) (int64, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: stack version %q", ErrInvalidUpdate, s)
	}

	return int64(n), nil
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
	// DryRun marks a preview of any kind. See Mutates.
	DryRun bool
	// Version is the stack version this update produces, assigned by the store
	// when it starts. A preview carries the stack's current version.
	Version int64
	// Revision increases with every change. Stores accept a write only if they
	// hold Revision-1, which makes every transition a compare-and-swap.
	Revision int64
	// CreatedBy is the subject of the principal that created the update. It
	// is empty when the caller gave none.
	CreatedBy string
	// TokenID is the ID of the access token that started the update. It is
	// empty when no access token started it, as with a bootstrap administrator
	// token. A lease request checks this token.
	TokenID string
	// JobID is the job whose job token started the update, or empty. Every
	// checkpoint write of the update records it as the origin job of the
	// state version it publishes, and so does the end of the update.
	JobID string
	// GitHead is the commit the client reported with the update, or empty.
	// Every checkpoint write of the update records it as the origin commit
	// of the state version it publishes, and so does the end of the update.
	GitHead string
	// RestoredFrom is the stack version that a restore copied. It is 0 for
	// any update that is not a restore.
	RestoredFrom int64
	// JournalVersion is the journal version that the server answered when the
	// update started. It is 0 when the update saves checkpoints. The store
	// keeps it with the update, so a restart does not change how a running
	// update saves its state.
	JournalVersion int64
	// CheckpointBytes is the size in bytes of the checkpoint the update
	// saved. It is 0 when the update saved no checkpoint. ListUpdates and
	// GetUpdate fill it from the update's checkpoint row; it is not stored
	// on the update itself.
	CheckpointBytes int64
}

// Mutates reports whether the update may change the stack's state: neither a
// "preview" nor a dry run of another kind. Only one mutating update runs per
// stack at a time; previews never block or wait, take no version and are not
// history.
func (u Update) Mutates() bool {
	return !u.DryRun && u.Kind != KindPreview
}

// Origin is the writer that every state version of the update records: the
// job whose job token started it, and the commit the client reported with it.
// Both are empty when neither is known.
func (u Update) Origin() identity.StateOrigin {
	return identity.StateOrigin{Job: u.JobID, Commit: u.GitHead}
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
// The CLI retries completion when a response is lost, so repeating the status
// an update already ended with succeeds and changes nothing. The retry still
// carries the lease token: it must match the digest kept at completion, so a
// wrong token cannot observe success for an update it never held. The digest
// stays after the update ends; the expiry does not, so the check ignores it.
func (u Update) complete(tok token.Token, now time.Time, status UpdateStatus) (Update, error) {
	if !status.Terminal() {
		return Update{}, fmt.Errorf("%w: %q is not a final status", ErrInvalidUpdate, status)
	}

	if u.Status == status {
		if !u.Lease.Digest.IsZero() && !u.Lease.Digest.Matches(tok) {
			return Update{}, ErrLeaseInvalid
		}

		return u, nil
	}

	if err := u.authorize(tok, now); err != nil {
		return Update{}, err
	}

	return u.end(now, status), nil
}

// cancel ends an update on a user's request, without its lease: this is how a
// stack locked by a crashed client is released.
// Canceling a canceled update succeeds and changes nothing, for retries.
func (u Update) cancel(now time.Time) (Update, error) {
	if u.Status == StatusCanceled {
		return u, nil
	}

	if u.Status.Terminal() {
		return Update{}, fmt.Errorf("%w: cancel %s update", ErrWrongUpdateState, u.Status)
	}

	return u.end(now, StatusCanceled), nil
}

func (u Update) end(now time.Time, status UpdateStatus) Update {
	u.Status = status
	u.EndedAt = now
	// The digest stays so a retried completion can still prove it held the
	// lease. The expiry goes, so no later write can use it.
	u.Lease = Lease{ExpiresAt: time.Time{}, Digest: u.Lease.Digest}
	u.Revision++

	return u
}

// Checkpoint is a stack's state as the engine saved it during an update.
// Deployment is opaque JSON in the schema named by SchemaVersion; the server
// never interprets it, so newer CLIs can store formats this server predates.
//
// Without the delta capability the CLI sends whole checkpoints with no
// sequence number, and the last write wins. Sequence numbers arrive only with
// verbatim and delta checkpoints, which this server does not advertise yet.
// Every save writes the key of its own attempt, so a repeated save without a
// sequence number never writes a key twice.
//
// The content lives in the blob store under BlobKey. The checkpoint store
// keeps only the metadata, so Deployment is empty on a read from the store.
type Checkpoint struct {
	SavedAt    time.Time
	Features   []string
	Deployment []byte
	Update     UpdateID
	Ref        Ref
	// NamespaceID and StackID name the namespace row and the stack row of the
	// checkpoint. A read from the store fills them from the same query as the
	// row, so the service builds the scope of the blob of the row without a
	// second read. A write ignores them: the store resolves the rows itself.
	NamespaceID namespace.ID
	StackID     ID
	// Lease is the digest of the lease that authorized the write. The store
	// accepts the checkpoint only while the update still runs under it.
	Lease token.Digest
	// SchemaVersion is the deployment format version, 1 or higher.
	SchemaVersion int
	// Sequence is the client's idempotency counter, or 0 when it sends none.
	Sequence int64
	// Digest is the SHA-256 of Deployment, used to recognize retried writes.
	Digest [sha256.Size]byte
	// Invalid is the client's mark that it could not verify this snapshot.
	// It is stored as sent.
	Invalid bool
	// BlobKey names the gzip content in the blob store:
	// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>-<attempt ID>.
	// The store refuses a write without it, and a write with another key.
	BlobKey blob.Key
	// BlobSize is the size of the stored gzip bytes.
	BlobSize int64
	// BlobMD5 is the MD5 of the stored gzip bytes, for backups.
	BlobMD5 [md5.Size]byte
	// AttemptStartedAt is the time at which the write attempt of the blob
	// started, by the clock of the writer. The store checks it with
	// blob.CheckAttempt against the clock of the database, in the transaction
	// of the commit. The store does not keep it, and a read gives it as zero.
	AttemptStartedAt time.Time
	// Origin names the job whose token started the update that saves this
	// checkpoint, and the commit the update reported. The store records it
	// with the state version it publishes for the write; a read gives it as
	// zero.
	Origin identity.StateOrigin
}

// CheckpointInput is a checkpoint as a client submits it.
type CheckpointInput struct {
	Features      []string
	Deployment    []byte
	SchemaVersion int
	Sequence      int64
	Invalid       bool
}

// Event is one engine event of an update, kept for the console and history.
// Payload is opaque JSON.
type Event struct {
	Timestamp time.Time
	Payload   []byte
	// Sequence orders events within an update; retried events repeat it. The
	// CLI numbers from 0.
	Sequence int64
}

// EventBatch is events written to one update under its lease.
type EventBatch struct {
	Events []Event
	Ref    Ref
	Update UpdateID
	// Lease is the digest of the lease that authorized the write.
	Lease token.Digest
}

// Page selects a window of a newest-first list.
type Page struct {
	Limit  int
	Offset int
}
