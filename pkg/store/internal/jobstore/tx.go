// Package jobstore holds the logic of the SQL adapters of job.Store and
// job.FreezeStore that does not depend on the SQL dialect: the encoding of
// the stored job, the rules of a dispatch, the changes of each event and the
// close of a write-back job. An adapter gives it a Runner, which opens the
// transactions, and a Tx, which reads and writes the rows of one
// transaction. So the adapters differ in their queries only, and the checks
// that need one transaction are written once.
package jobstore

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ErrHolderIndex is what a Tx returns from InsertJob or UpdateJob when the
// partial unique index on the unit of the jobs that hold it refuses the row.
// The store turns it into job.ErrUnitBusy. An adapter never returns a driver
// error for this case.
var ErrHolderIndex = errors.New("another job holds the unit for a change")

// ErrUnitNotInInventory is what a Tx returns from CoveringLocks when the
// inventory holds no unit with the ID in the namespace: a unit that left the
// inventory has no lock to read, so the dispatch must not go on.
var ErrUnitNotInInventory = errors.New("the unit is not in the inventory")

// ErrKeyIndex is what a Tx returns from InsertJob when the unique index on
// the idempotency key of a namespace refuses the row: a second create with
// one key committed first. The store reads the first job and answers as it
// does for a repeated key.
var ErrKeyIndex = errors.New("a job with this idempotency key exists")

// Row is one stored job: the columns that a query reads, and the rest of the
// job as a JSON body.
type Row struct {
	ID        job.ID
	Namespace namespace.Name
	// NamespaceID is the stable ID of the namespace of the job, read from
	// the namespace_id column. The ID of a namespace is never given again,
	// so a job whose namespace a restore replaced keeps this ID.
	NamespaceID namespace.ID
	UnitID      string
	// Seq orders the jobs of one namespace, oldest first. The jobs of two
	// namespaces can have the same Seq.
	Seq   int64
	State job.State
	Wait  job.WaitReason
	// Phase is the index of the current phase.
	Phase int
	// Changes is true when the current phase changes resources, state or
	// the code in git.
	Changes bool
	// Operation is the durable operation of the current phase, or empty.
	Operation string
	Key       string
	Version   int64
	Body      string
	CreatedMs int64
	UpdatedMs int64
	// StateChangedMs is the time of the last change of State.
	StateChangedMs int64
}

// FreezeRow is one stored change freeze. A zero EndsMs means no end.
type FreezeRow struct {
	ID        string
	Namespace namespace.Name
	Reason    string
	StartsMs  int64
	EndsMs    int64
	CreatedBy string
	EndedBy   string
}

// FinalVersion is the last final state version of one unit.
type FinalVersion struct {
	UnitID    string
	VersionID string
	Commit    string
	Job       job.ID
	At        time.Time
}

// OrphanRow is a job that Orphans selects. Missing is true when the
// operation of its current phase does not exist.
type OrphanRow struct {
	Row     Row
	Missing bool
}

// Query selects jobs for List. The order is newest first by Seq, then by job
// ID, so the order is total across namespaces. The adapter applies After as:
// the jobs that come after the job After in this order.
type Query struct {
	Namespace namespace.Name
	UnitID    string
	States    []job.State
	Limit     int
	After     job.ID
	// StateChangedBeforeMs selects the jobs whose state last changed before
	// this time. 0 selects every job.
	StateChangedBeforeMs int64
	// StateChangedAfterMs selects the jobs whose state last changed after
	// this time. 0 selects every job.
	StateChangedAfterMs int64
}

// Tx reads and writes the rows of one transaction. An adapter implements it
// over its queries. Every method runs in the transaction that Runner.Write or
// Runner.Read opened, after the check of the installation state and the
// fence.
//
//nolint:interfacebloat // One adapter contract for one table set: the jobs, the freezes and the state records share a transaction.
type Tx interface {
	// Now returns the time on the clock of the database.
	Now(ctx context.Context) (time.Time, error)

	// Job returns the job id, and false when none exists.
	Job(ctx context.Context, id job.ID) (Row, bool, error)
	// JobByKey returns the job of the namespace with the key, and false
	// when none exists.
	JobByKey(ctx context.Context, ns namespace.Name, key string) (Row, bool, error)
	// InsertJob stores a new row. It sets Seq. It returns ErrHolderIndex or
	// ErrKeyIndex for a row that an index refuses.
	InsertJob(ctx context.Context, r Row) error
	// UpdateJob replaces the row of version prev with r, and reports false
	// when the stored version is not prev. It returns ErrHolderIndex for a
	// row that the index on the unit refuses.
	UpdateJob(ctx context.Context, r Row, prev int64) (bool, error)
	// CountOpen counts the jobs that are not terminal, of the unit unitID
	// of the namespace ns, or of the whole namespace when unitID is empty.
	CountOpen(ctx context.Context, ns namespace.Name, unitID string) (int, error)
	// HolderOf returns the ID of a job other than except that holds the unit
	// of the namespace ns for a change, and false when none does.
	HolderOf(ctx context.Context, ns namespace.Name, unitID string, except job.ID) (job.ID, bool, error)
	// Awaiting returns the jobs of the unit of the namespace ns that wait for
	// a state version, oldest first.
	Awaiting(ctx context.Context, ns namespace.Name, unitID string) ([]Row, error)
	// List returns the jobs that q selects, newest first.
	List(ctx context.Context, q Query) ([]Row, error)
	// Orphans returns at most limit jobs that hold their unit and whose
	// operation does not exist or ended, oldest first.
	Orphans(ctx context.Context, limit int) ([]OrphanRow, error)

	// ActiveFreeze returns the ID of a freeze of the namespace that is active
	// at t, and false when none is.
	ActiveFreeze(ctx context.Context, ns namespace.Name, t time.Time) (string, bool, error)
	// CoveringLocks returns the manual locks that cover the unit unitID of the
	// namespace ns, the lock of the unit first. It finds the unit by its stable
	// ID, so a rename does not hide a lock. It returns none when no lock
	// covers the unit, and ErrUnitNotInInventory when the inventory holds no
	// such unit.
	CoveringLocks(ctx context.Context, ns namespace.Name, unitID string) ([]manuallock.Lock, error)
	// StateVersion returns the current state version of the unit of the
	// namespace ns, or empty.
	StateVersion(ctx context.Context, ns namespace.Name, unitID string) (string, error)
	// FinalVersion returns the last final state version of the unit of the
	// namespace ns, and false when none was recorded.
	FinalVersion(ctx context.Context, ns namespace.Name, unitID string) (FinalVersion, bool, error)

	// InsertFreeze stores a freeze. It returns job.ErrConflict for an ID
	// that exists.
	InsertFreeze(ctx context.Context, f FreezeRow) error
	// Freeze returns the freeze id, and false when none exists.
	Freeze(ctx context.Context, id string) (FreezeRow, bool, error)
	// SetFreezeEnd sets the end and the ender of the freeze id.
	SetFreezeEnd(ctx context.Context, id string, endsMs int64, by string) error
	// Freezes returns the freezes of ns that end after sinceMs or never end,
	// ordered by start and ID.
	Freezes(ctx context.Context, ns namespace.Name, sinceMs int64) ([]FreezeRow, error)

	// StartOperation starts the durable operation of req with the rules of
	// durable.Store.Start, and returns its ID.
	StartOperation(ctx context.Context, req *durable.StartRequest) (string, error)
	// CancelOperation applies the rules of durable.Store.Cancel to the
	// operation id.
	CancelOperation(ctx context.Context, id durable.ID) error

	// CheckClaim returns durable.ErrClaimLost unless the claim c is the live
	// claim of its operation on the clock of the database. It changes nothing.
	CheckClaim(ctx context.Context, c durable.Claim) error
	// EndOperation ends the durable operation of the claim of end with the
	// rules of durable.Store.Complete, Fail or Release for end.Kind, and
	// checks the token of the claim and its expiry on the clock of the
	// database. It returns durable.ErrClaimLost when the claim is not live,
	// and durable.ErrCanceled for EndComplete while a cancel is requested:
	// the operation then stays as it was, and the caller ends it with
	// EndRelease. On an error it changes nothing.
	EndOperation(ctx context.Context, end *job.OperationEnd) error

	// AppendAudit appends the entries to the audit chain of the namespace.
	AppendAudit(ctx context.Context, e []audit.Entry) error

	ArtifactTx

	// ItemKeys returns the item key part of this transaction, under fence, so
	// that the record of the item key of a new plan or log version commits
	// with its row.
	ItemKeys(fence namespace.Fence) secret.ItemKeyTx
}

// CloseTx reads and writes the rows of one transaction, for the close of the
// jobs that wait for a state version. The Tx of every adapter implements it.
// An adapter whose state write opens no job transaction, for example the
// store of a workspace or a stack that publishes a state version, gives the
// close this narrow view of its transaction: the close runs no hand-written
// query, so its queries alone serve it.
type CloseTx interface {
	// Now returns the time on the clock of the database.
	Now(ctx context.Context) (time.Time, error)
	// Awaiting returns the jobs of the unit of the namespace ns that wait for
	// a state version, oldest first.
	Awaiting(ctx context.Context, ns namespace.Name, unitID string) ([]Row, error)
	// UpdateJob replaces the row of version prev with r, and reports false
	// when the stored version is not prev. It returns ErrHolderIndex for a
	// row that the index on the unit refuses.
	UpdateJob(ctx context.Context, r Row, prev int64) (bool, error)
	// StartOperation starts the durable operation of req with the rules of
	// durable.Store.Start, and returns its ID.
	StartOperation(ctx context.Context, req *durable.StartRequest) (string, error)
}

// Runner opens the transactions of an adapter.
type Runner interface {
	// Write runs fn in one write transaction that checks the installation
	// state and fence first. A SQL adapter that does not serialize writes
	// by itself also holds the row of the namespace until the transaction
	// ends, before fn reads any rule. An error of fn rolls the transaction
	// back.
	Write(ctx context.Context, fence namespace.Fence, fn func(Tx) error) error
	// Read runs fn in one read transaction.
	Read(ctx context.Context, fn func(Tx) error) error
	// Logger returns the logger of the adapter, or nil. The store reads it at
	// each use, so the adapter can set its logger after it gives the runner.
	Logger() *slog.Logger
}

// PlanRow is one stored plan: the lookup row of job_plans. Body is the JSON of
// the plan.
type PlanRow struct {
	ID        string
	Job       job.ID
	Body      string
	CreatedMs int64
}

// LogRow is one stored log version: the lookup row of job_logs. It names the
// step record that holds the version.
type LogRow struct {
	ID        string
	Job       job.ID
	Phase     int
	Step      string
	Token     int64
	CreatedMs int64
}

// ArtifactTx reads and writes the rows of plans, log versions and log chunks
// of one transaction. The Tx of every adapter implements it.
type ArtifactTx interface {
	// InsertPlan stores a plan row of the namespace ns.
	InsertPlan(ctx context.Context, ns namespace.Name, r PlanRow) error
	// UpdatePlan replaces the body of the plan row r.ID of the namespace ns.
	UpdatePlan(ctx context.Context, ns namespace.Name, r PlanRow) error
	// Plan returns the plan row id of the namespace ns, and false when none
	// exists.
	Plan(ctx context.Context, ns namespace.Name, id string) (PlanRow, bool, error)
	// InsertLog stores a log version row of the namespace ns.
	InsertLog(ctx context.Context, ns namespace.Name, r LogRow) error
	// Log returns the log version row id of the namespace ns, and false when
	// none exists.
	Log(ctx context.Context, ns namespace.Name, id string) (LogRow, bool, error)
	ChunkTx
}

// ChunkKey is the blob key that the row of the confirmed chunk Seq holds.
type ChunkKey struct {
	Seq     int
	BlobKey string
}

// ChunkTx reads and writes the rows of log chunks of one transaction.
type ChunkTx interface {
	// Chunk returns the row of the chunk seq of the log version, and false
	// when none exists.
	Chunk(ctx context.Context, ns namespace.Name, log string, seq int) (job.ChunkRow, bool, error)
	// InsertChunk stores the row of a chunk that is not confirmed. A row that
	// exists stays as it is.
	InsertChunk(ctx context.Context, ns namespace.Name, log string, seq int, c job.ChunkRow) error
	// ConfirmChunk marks the row of the chunk as confirmed and keeps what the
	// attempt that won stored.
	ConfirmChunk(ctx context.Context, ns namespace.Name, log string, seq int, stored StoredChunk) error
	// ChunkKeys returns the blob key of each confirmed chunk of the log
	// version, in the order of the chunk numbers.
	ChunkKeys(ctx context.Context, ns namespace.Name, log string) ([]ChunkKey, error)
	// JobPlanIDs returns the ID of each plan row of the job of the namespace
	// ns, also of a reserved plan.
	JobPlanIDs(ctx context.Context, ns namespace.Name, id job.ID) ([]string, error)
	// JobChunkKeys returns the blob key of each confirmed chunk of each log
	// version of the job of the namespace ns.
	JobChunkKeys(ctx context.Context, ns namespace.Name, id job.ID) ([]string, error)
}

// StoredChunk is what the attempt that won a chunk stored: the size and the
// MD5 of its sealed bytes in the blob store, and the key of its blob.
type StoredChunk struct {
	Digest  blob.Digest
	BlobKey string
}
