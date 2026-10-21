package catalog

import (
	"encoding/json"
	"fmt"
	"slices"
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
	// QuotaKeys are the keys of KindUnits that the unit of the request
	// counts in: the keys of the reservation of the create, with the scope
	// of the requester and of each group of the requester at the submit.
	// The reconcile schedule counts the settled units of each key from
	// these records, so a unit counts in the keys of its create, also after
	// its requester leaves a group.
	QuotaKeys []quota.Key
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
	// Reservation is the reservation of a create, or empty. A revision from
	// git that supersedes a create takes over its reservation, so the ID can
	// be the key of the superseded revision.
	Reservation quota.ReservationID
	// Deadline is the time at which the expire schedule ends a revision with
	// no job or with a job that ended (ExpiryOf, RevisionDeadline). It moves
	// the deadline of a revision whose job has not ended from ExtendLead
	// before it. The reservation of a create ends at the same time.
	Deadline time.Time
	// Document is the document of the revision. For a request in git it is
	// a record of what the revision delivered, never a second home.
	Document Document
	// Decision is the combined policy decision at submit, which gives the
	// gates of the job.
	Decision  policy.Combined
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
// start of the revision in the same transaction, for a revision of
// OriginAPI.
//
// Quota, Provision and Supersedes add writes to the same transaction, so
// that a failed submit keeps none of them.
type Submission struct {
	// Quota is the quota write of the submit, or nil when the blueprint
	// version has no rule that counts.
	Quota *QuotaWrite
	// Provision is set for a create from git: the store makes the empty
	// unit in the transaction of the submit and sets Request.Unit. A create
	// through the API makes its unit in the start operation instead.
	Provision *Provision
	Request   Request
	Revision  Revision
	Audit     []audit.Entry
	// Supersedes is the number of the revision in flight that a revision
	// from git replaces, or 0. The store moves that revision to
	// StateSuperseded with CheckSupersede. When both revisions are creates,
	// the new revision takes over the unit and the reservation of the old
	// one, and the store sets the end of the reservation to the new
	// deadline. The unit stays, and nothing is reserved twice.
	Supersedes int
}

// QuotaWrite is the quota write of a submit. The store writes it through
// the quota.Tx of its own transaction, after its other checks and in this
// order: Reserve of Reservation with the checks Units, then Record of the
// submit at Reservation.At, or at the time of the submit when Reservation
// is nil, with the checks Requests. A submit that a units limit refuses
// never counts in the window of a requests limit.
type QuotaWrite struct {
	// Reservation is the reservation of a create, with the ID of the
	// revision key and EndsAt equal to the deadline of the revision, or nil.
	Reservation *quota.Reservation
	Units       []quota.Check
	Requests    []quota.Check
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

// DeadlineChange moves the deadline of the revision Number of the request
// Ref, made with the version of the request that the caller read.
type DeadlineChange struct {
	Ref RequestRef
	// Deadline is the new deadline, after the old one: Expiry.Deadline.
	Deadline time.Time
	Number   int
	Version  int64
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
// did not apply, with the start key of QuotaKey. It starts only when the
// revision is terminal, and a revision with a job is terminal only after
// its job (ExpiryOf), so it never runs under a job that has not ended. A create that a revision
// from git superseded starts none: its unit and its reservation belong to
// the new revision.
const KindQuota durable.Kind = "catalog.quota"

// KindExpire is the schedule that applies ExpiryOf to each revision in
// flight at or after ExtendLead before its deadline. It runs at least once
// each hour. It ends a revision only when the revision has
// no job or its job ended, with Store.Change, which starts the operation of
// KindQuota of a create. For a job that has not ended, it cancels the job
// when the job model allows it, moves the deadline and the end time of the
// reservation by quota.ReservationGrace with Store.Extend, and checks the
// revision again on the next run. So it never releases a reservation or
// removes a unit under a job that has not ended.
const KindExpire durable.Kind = "catalog.expire"

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
// the rendered snapshot of the revision, which the Catalog renders once at
// the start and the job spec names by its SHA-256, so each phase runs the
// same bytes. It returns ErrInvalid for an operation with no job.
func StartRequestFor(req *Request, rev *Revision, v *Version, reason string) (job.StartRequest, error) {
	out := job.StartRequest{
		Unit:           req.Unit,
		Operation:      job.OperationApply,
		Purpose:        "",
		Executor:       job.ExecutorRunner,
		Location:       job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
		Pool:           v.Blueprint.Platform.Pool,
		Edits:          nil,
		Delivery:       "",
		Source:         job.SourceCatalog,
		IdempotencyKey: rev.Key(),
		Reason:         reason,
		BreakGlass:     rev.BreakGlass,
		Channel:        audit.ChannelAPI,
		SourceIP:       "",
		UserAgent:      "",
		RequestID:      "",
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

// GateFor returns the approval gates that the combined Catalog decision d
// gives to the first phase that changes: one gate for each requirement of
// d, and none for a decision that is not approve. The job model needs each
// gate of a phase, so a change needs each requirement. Each gate needs the
// approvals of its requirement by members of its groups, and never counts
// the requester: AllowSelf is false, so a requester cannot approve its own
// request, also when it is in a group of the gate. bindPlan binds the
// approvals to the plan of the phase before, when there is one.
func GateFor(d *policy.Combined, bindPlan bool) []job.Gate {
	if d.Effect != policy.EffectApprove {
		return nil
	}

	out := make([]job.Gate, 0, len(d.Approvals))

	for _, a := range d.Approvals {
		out = append(out, job.Gate{
			Type: job.GateApproval, Approvals: a.Count, Groups: slices.Clone(a.Groups),
			AllowSelf: false, Timeout: 0, BindPlan: bindPlan,
		})
	}

	return out
}

// EditableRequestFile returns the predicate that the Catalog gives to
// job.ValidateEdits for the write-back job of ref, whose code location has
// the directory dir: it allows exactly the request file of ref, at
// GitPath(ref), and no other file. The path must be RequestFile as it is,
// and dir must be GitDir(ref), so no other spelling of the path passes.
func EditableRequestFile(ref RequestRef, dir string) func(p string) bool {
	return func(p string) bool {
		return dir == GitDir(ref) && p == RequestFile
	}
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
