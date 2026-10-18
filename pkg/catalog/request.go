package catalog

import (
	"encoding/json"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/policy"
	"opendustpan.org/opendustpan/pkg/quota"
)

// Request is the record of a Catalog request. It is namespace data: the
// store checks the fence and appends the audit entries in the transaction
// of each write, and the backup of the namespace holds it.
type Request struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	// Unit is the unit of the request, with its stable unit ID set, or the
	// zero value before the Catalog made it.
	Unit identity.Object
	ID   RequestID
	Ref  RequestRef
	Home Home
	// Ensure is what the last applied revision asked for, or empty when no
	// revision was applied.
	Ensure Ensure
	// UnitName is the tool's own name of the unit, as the blueprint
	// computed it at the first submit. It never changes.
	UnitName string
	// Last is the number of the newest revision, and Applied the number of
	// the last applied revision, or 0.
	Last    int
	Applied int
	// Version grows on each write. A write names the version that it read,
	// so a write on a stale read fails with ErrConflict.
	Version int64
}

// EngineDecision is the decision of one engine at one recheck point, as the
// revision records it. InputDigest is the SHA-256 of the input document, in
// hex.
type EngineDecision struct {
	Point       RecheckPoint
	Decision    policy.Decision
	InputDigest string
}

// Revision is one submit of a request document, or one change of the
// request file in git that the team's CI rendered.
type Revision struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	// BreakGlass is set when the revision started its job through an active
	// freeze.
	BreakGlass *job.BreakGlass
	Request    RequestID
	Origin     Origin
	State      RevisionState
	Operation  Operation
	// Job is the job of the revision, or empty before its start and for a
	// revision from git.
	Job job.ID
	// JobVersion is the version of the job that gave State.
	JobVersion int64
	// Commit is the commit of a revision from git.
	Commit string
	// Reason is why the revision ended, for a terminal state other than
	// applied.
	Reason      string
	SubmittedBy string
	Agent       string
	// Reservation is the reservation of a create, or empty.
	Reservation quota.ReservationID
	// Document is the document of the revision. For a request in git it is
	// a record of what the revision delivered, never a second home.
	Document Document
	// Decision is the combined policy decision at submit, which gives the
	// gates of the job.
	Decision  policy.Decision
	Decisions []EngineDecision
	// Failures are the failures of the recheck that rejected the revision.
	Failures []Failure
	// Usage is the quota usage that each recheck point saw.
	Usage            []quota.Usage
	Number           int
	BlueprintVersion int
}

// Key returns the revision key of r: <request ID>/<number>.
func (r *Revision) Key() string {
	return RevisionKey(r.Request, r.Number)
}

// Submission is the first write of a revision: the request, created or
// updated with the new Last, the revision in StateAccepted, and the audit
// entries of the submit. The store starts the durable operation of the
// start of the revision in the same transaction.
type Submission struct {
	Request  Request
	Revision Revision
	Audit    []audit.Entry
}

// RevisionChange is one change of the state of a revision, made with the
// version of the request that the caller read.
type RevisionChange struct {
	Ref RequestRef
	// To is the new state. CheckTransition holds the rules.
	To RevisionState
	// Job is set when the change records the job of the revision.
	Job        job.ID
	JobVersion int64
	Reason     string
	Failures   []Failure
	// Unit is set when the change records the unit that the Catalog made.
	Unit    identity.Object
	Number  int
	Version int64
}

// ListFilter selects requests for ListRequests. A zero field matches every request.
// Limit 0 takes the default page size of the adapter.
type ListFilter struct {
	Namespace namespace.Name
	Blueprint string
	// UnitID selects the request of one unit.
	UnitID string
	States []RevisionState
	// After is the ID of the last request of the previous page.
	After RequestID
	Limit int
}

// KindStart is the durable operation that makes the unit of a revision and
// starts its job, with the start key of Revision.Key.
const KindStart durable.Kind = "catalog.start"

// KindQuota is the durable operation that settles or releases the
// reservation of a revision, and deletes the empty unit of a create that
// did not apply, with the start key of QuotaKey.
const KindQuota durable.Kind = "catalog.quota"

// QuotaKey returns the start key of the operation of KindQuota of the
// revision key revision: <request ID>/<revision>/quota.
func QuotaKey(revision string) string {
	return revision + "/quota"
}

// StartRequestFor returns the job start of revision rev of request req,
// from blueprint version v. The idempotency key is the revision key, so a
// retry of the start gives one job. A request in git gives a write-back job
// that edits the request file, and a request in OpenDustpan gives a runner
// job on the pool of the blueprint. The code location of a runner job is
// the rendered code of the revision, which the job model reads through its
// Catalog origin. It returns ErrInvalid for an operation with no job.
func StartRequestFor(req *Request, rev *Revision, v *Version, reason string) (job.StartRequest, error) {
	out := job.StartRequest{
		Unit:           req.Unit,
		Operation:      job.OperationApply,
		Executor:       job.ExecutorRunner,
		Location:       job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
		Pool:           v.Blueprint.Platform.Pool,
		Edits:          nil,
		Delivery:       "",
		IdempotencyKey: rev.Key(),
		Reason:         reason,
		BreakGlass:     rev.BreakGlass,
		Channel:        audit.ChannelAPI,
	}

	switch rev.Operation {
	case OperationCreate, OperationUpdate:
	case OperationDestroy:
		out.Operation = job.OperationDestroy
	case OperationNone:
		return job.StartRequest{}, fmt.Errorf("%w: a revision with no change starts no job", ErrInvalid)
	default:
		return job.StartRequest{}, fmt.Errorf("%w: the operation %q starts no job", ErrInvalid, rev.Operation)
	}

	if req.Home.Kind != HomeGit {
		return out, nil
	}

	edit, err := requestFileEdit(&rev.Document)
	if err != nil {
		return job.StartRequest{}, err
	}

	ref := rev.Document.Ref()
	out.Operation = job.OperationApply
	out.Executor = job.ExecutorWriteBack
	out.Pool = ""
	out.Location = job.Location{
		Connection: req.Home.Connection, Repository: req.Home.Repository, Ref: req.Home.Ref,
		Directory: GitDir(ref), Revision: "",
	}
	out.Edits = []job.FileEdit{edit}
	out.Delivery = req.Home.Delivery

	if out.Delivery == "" {
		out.Delivery = job.DeliveryReviewRequest
	}

	return out, nil
}

// requestFileEdit returns the edit of the request file that writes d: one
// key for each top-level field of the file. The write-back executor changes
// only these keys, and refuses the edit when a key changed after the base.
func requestFileEdit(d *Document) (job.FileEdit, error) {
	data, err := d.Encode()
	if err != nil {
		return job.FileEdit{}, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return job.FileEdit{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	keys := make(map[string][]byte, len(fields))
	for k, v := range fields {
		keys[k] = v
	}

	return job.FileEdit{Path: RequestFile, Keys: keys}, nil
}
