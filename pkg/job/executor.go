package job

import (
	"context"
	"io"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Durable kinds of jobs. A phase dispatches as one durable operation of
// KindWriteBack or a runner kind, so the claim, its expiry and the cancel
// of a phase are the rules of package durable. The other kinds are the
// follow-up work of a job, which the server instances claim with a
// durable.Runner. No other queue holds jobs.
const (
	// KindWriteBack is the kind of a deliver phase. The server instances
	// claim it with a durable.Runner.
	KindWriteBack durable.Kind = "job.write-back"
	// KindAbandon calls Executor.Abandon for a job that ended canceled,
	// rejected or expired after a delivery. The store starts it in the
	// transaction of that change.
	KindAbandon durable.Kind = "job.abandon"
	// KindVerify asks the forge whether the commit of the closing state
	// version contains the delivered commit, and records the
	// Verification. The store starts it in the transaction of the close.
	KindVerify durable.Kind = "job.verify"
	// KindReviewWatch is the polling fallback of the review watcher: it
	// asks the forge for the merge or the close of the review request of a
	// job in awaiting-state, for the case that a forge webhook did not
	// arrive. The store starts it in the transaction of the delivery.
	KindReviewWatch durable.Kind = "job.review-watch"
	// KindRunnerPrefix starts the kind of a runner phase. The kind of a
	// phase for the pool with the ID p is KindRunnerPrefix + p, so a runner
	// claims only the phases of its own pool.
	KindRunnerPrefix = "job.runner."
)

// RunnerKind returns the durable kind of the phases of the runner pool with
// the ID pool.
func RunnerKind(pool string) durable.Kind {
	return durable.Kind(KindRunnerPrefix + pool)
}

// PhaseKey returns the durable start key of the phase at index phase of the
// job id. A phase has one operation, so a retry of the dispatch after a
// lost answer gives the same operation, and a takeover is a new claim on
// it.
func PhaseKey(id ID, phase int) string {
	return string(id) + "/" + strconv.Itoa(phase)
}

// FollowUpKey returns the durable start key of the follow-up operation of
// kind k of the job id: one for each job and kind.
func FollowUpKey(id ID, k durable.Kind) string {
	return string(id) + "/" + string(k)
}

// Executor is the port that each way of running a job implements. The job
// service calls Phases at start. A durable operation of KindAbandon calls
// Abandon after the job ends.
// The work itself goes through durable operations: an executor takes a
// phase with Claimer.Claim, and reports through Claimer and Recorder.
//
// The write-back executor runs in the server. It claims KindWriteBack with
// a durable.Runner. The runner executor is the server side of the runner
// protocol. It claims a runner kind on behalf of the runner that asks.
type Executor interface {
	// Kind names the executor.
	Kind() ExecutorKind
	// Phases checks spec and returns its phases in order. It returns
	// ErrUnsupported for an operation that the executor cannot do, and
	// ErrInvalid, wrapped with the reason, for a spec that it cannot run:
	// for write-back, edits that fail ValidateEdits or a forge connection
	// without push access; for a runner, steps that fail ValidateSteps, an
	// unknown pool, a pool of another namespace than the unit, or no
	// profile.
	Phases(ctx context.Context, spec Spec) ([]PhaseKind, error)
	// Abandon undoes what the job left open, after a cancel, a rejection or
	// an expiry of a job that delivered. Write-back closes the review
	// request that it opened when no merge happened. A direct push stays.
	// A runner has nothing to undo and returns nil. It runs in a durable
	// operation of KindAbandon, so it is safe to call more than once, and
	// an error retries with the backoff of the durable runner.
	Abandon(ctx context.Context, j Job) error
}

// PhaseClaim is the hold of one executor on one phase. Claim is the
// durable claim of the operation of the phase, with its token and its
// expiry. Fence is the namespace fence of the unit, taken at the claim.
// Every write of the executor carries the phase claim, and the store
// checks the token and the fence in the transaction of the write. So an
// executor of a namespace that failed over writes nothing.
type PhaseClaim struct {
	Job   ID
	Phase int
	Claim durable.Claim
	Fence namespace.Fence
}

// ClaimRequest asks for one phase to run. Kinds are the durable kinds that
// the executor runs. Owner names the runner or the instance, for status.
// Lease is the time from the claim to its expiry. MaxVersions is the limit
// of the version of the operation for each kind, as in
// durable.ClaimRequest: a phase above the limit of its kind stays for
// another claimer. A kind with no entry has no limit.
type ClaimRequest struct {
	Owner       string
	Kinds       []durable.Kind
	MaxVersions map[durable.Kind]int
	Lease       time.Duration
}

// Assignment is a claimed phase: the phase claim and the job as it was at
// the claim. The executor runs every step of the phase, from the first, on
// a fresh checkout. It holds no secret. A runner gets the credentials of
// the claim through the runner protocol.
type Assignment struct {
	Claim PhaseClaim
	Job   Job
	// Plan is the plan that an apply phase applies, with the hashes of its
	// parts, or nil.
	Plan *PlanRef
	// Profile names the profile whose credentials the tool steps need, or is
	// empty when they need none.
	Profile string
}

// Outcome is how an executor ends a phase.
type Outcome struct {
	// Succeeded is true when every step that cannot fail succeeded.
	Succeeded bool
	// Message tells why a phase failed, in the words of the executor.
	Message string
}

// Claimer is the port through which an executor takes, keeps and ends a
// phase. The job service implements it on the durable store and the job
// store. Every method that takes a PhaseClaim returns ErrClaimLost when the
// claim is not live: another claim replaced it, it expired, or the phase
// ended. It returns namespace.ErrFenced when the fence of the claim is
// stale. Both executors use this port: the write-back executor in the
// server, and the runner protocol for each runner.
type Claimer interface {
	// Claim takes one phase of req.Kinds that can run, moves its job to
	// running, and returns the assignment with the namespace fence of the
	// unit. The claim of a plan phase records the current state version of
	// the unit in Phase.StateVersion, in the same transaction. A takeover is
	// a new claim on the same operation, with a new token: the phase runs
	// again from its first step. When an earlier claim recorded the start
	// of a step that changes resources, whether or not it recorded its end,
	// the takeover fails the phase instead (AfterLostClaim), so a finished
	// change never runs again, and Claim looks for another phase. A deliver
	// phase has no such step, and its takeover runs the lookup of
	// NeedsCommitLookup. It returns durable.ErrNoWork when no phase can run
	// now, and ErrInvalid for a request with no owner, no kind or a lease
	// that is not positive.
	Claim(ctx context.Context, req ClaimRequest) (Assignment, error)
	// Renew moves the expiry of c to now plus d on the clock of the store,
	// and returns the new claim. It returns ErrCanceled when a cancel
	// arrived: the executor then stops the phase and calls Finish.
	Renew(ctx context.Context, c PhaseClaim, d time.Duration) (PhaseClaim, error)
	// Release gives the phase back before it ends, with the rule of
	// AfterLostClaim. The end of the operation and the event of the job commit
	// in one transaction (Change.End), bound to the claim c: a holder whose
	// claim a takeover replaced changes nothing and gets ErrClaimLost. The
	// call does not stop when the context of the request ends. With no recorded start of a step that changes
	// resources under any claim of the phase, the operation waits for the
	// next claim. Else the phase fails, also when the change ended. During
	// a cancel, the job ends canceled when no change began, and failed when
	// one began: a release after a change never ends canceled.
	Release(ctx context.Context, c PhaseClaim) error
	// Finish ends the phase with o, and moves the job by the state machine:
	// to the next phase, to succeeded or to failed. A phase that fails
	// during a cancel ends the job failed, never canceled. The end of the
	// operation and the event of the job commit in one transaction
	// (Change.End), bound to the claim c, and the call does not stop when the
	// context of the request ends. A successful Finish of a deliver phase
	// needs a Recorder.Delivered first, and returns ErrInvalid before it, with
	// the claim still live: the write-back executor must call Delivered
	// before Finish and must not depend on this error. Finish with a failed
	// outcome and Release still work.
	Finish(ctx context.Context, c PhaseClaim, o Outcome) error
}

// Recorder is the port through which an executor records the progress of a
// phase. Every method checks the claim and its namespace fence in the
// transaction of its write. It returns ErrClaimLost when the claim is not
// live, and namespace.ErrFenced when the fence is stale. During a cancel,
// BeginStep returns ErrCanceled, and the other methods still accept
// writes, so that the executor can record how the phase stopped.
type Recorder interface {
	// BeginStep records that the step called name starts under the claim
	// c. For a step that changes resources, the executor calls it before
	// it starts the process. The write-back executor calls it before its
	// forge commit (WriteBackStepCommit). It returns ErrCanceled during a
	// cancel, and durable.ErrStepDone when the claim already recorded the
	// end of the step.
	BeginStep(ctx context.Context, c PhaseClaim, name string) error
	// EndStep records the outcome of a step that BeginStep started. It uses
	// the Name, the Token and the ExitCode of r: the server sets the times, and
	// the log version is the one that AppendLog made. When the step has a log
	// version, EndStep reads its chunks back, closes the version with their
	// count, the SHA-256 of their plaintext and its size. It returns
	// ErrInvalid for a step with no begin record under c, for a Token that is
	// not the token of c, and for an exit code that is not in -1 to 255. A
	// second EndStep with the same exit code changes nothing, and another exit
	// code returns ErrConflict.
	EndStep(ctx context.Context, c PhaseClaim, r StepRecord) error
	// AppendLog stores the next chunk of the log of the step called step,
	// as the blob LogChunkAttemptKey. Seq starts at 0 and grows by 1. Chunk 0
	// creates the log version of the step under c, with a new item key of
	// the kind secret.ItemKindJobLog and LogRef.ExpiresAt from
	// history.RetentionSettings.JobLog. A
	// chunk with a seq that the log has already, and the same SHA-256,
	// changes nothing, so a retry is safe. A chunk with a known seq and
	// another hash returns ErrConflict. A chunk above MaxLogChunk, or a seq
	// that is not the next one, returns ErrInvalid. EndStep closes the
	// log version.
	AppendLog(ctx context.Context, c PhaseClaim, step string, seq int, chunk io.Reader) error
	// PutPlan stores the plan of a plan phase, with one part for each
	// upload, and returns its reference. It writes the item key (of the
	// kind secret.ItemKindPlan) and the part blobs first, and then the plan
	// in one transaction, with PlanRef.ExpiresAt from
	// history.RetentionSettings.Plan. In that
	// transaction it sets PlanRef.StateVersion to Phase.StateVersion, and
	// checks that it is still the current state version of the unit. When
	// it is not, the transaction fails the phase (EventPhaseFailed, with a
	// reason that names both versions), stores no plan, and PutPlan
	// returns ErrPlanStale. It returns ErrInvalid for a phase that is not a
	// plan phase, a kind that is not a plan output, a kind twice, or a part
	// above MaxPlanPart, and ErrConflict for a phase that has a plan
	// already. A plan does not change after PutPlan.
	PutPlan(ctx context.Context, c PhaseClaim, parts []PlanUpload) (PlanRef, error)
	// Delivered records the commit of a deliver phase, and writes the audit
	// entry of the delivery (unit:write-back) in the same transaction. A second
	// call with the same result changes nothing and writes no second entry,
	// and a call with another result returns ErrConflict. It returns
	// ErrInvalid when the phase is not a deliver phase or d fails
	// DeliveryResult.Validate.
	Delivered(ctx context.Context, c PhaseClaim, d DeliveryResult) error
}

// The steps of a deliver phase, as the write-back executor records them.
// They have no Step in the spec: the executor runs them in the server.
const (
	// WriteBackStepCommit is the commit through the forge. The executor
	// records its begin before the forge call. Before it commits on a claim
	// for which NeedsCommitLookup is true, it looks for a commit with the
	// TrailerJob trailer of the job on the target ref, or for the branch
	// BranchPrefix + the job ID, and records the commit that it finds with
	// no second commit.
	WriteBackStepCommit = "commit"
	// WriteBackStepReview opens the review request of the commit.
	WriteBackStepReview = "review-request"
	// BranchPrefix starts the branch of the review request of a write-back
	// job. The branch is BranchPrefix + the job ID.
	BranchPrefix = "dustpan/"
)

// NeedsCommitLookup reports whether the write-back executor must look for
// the commit of the job before it commits in the deliver phase p: on every
// claim after the first (p.Claims above 1), and whenever a claim recorded
// the begin of WriteBackStepCommit. Each takeover runs the lookup, whatever
// the earlier claim recorded, because the forge can have made the commit
// before or after the begin record.
func NeedsCommitLookup(p *Phase) bool {
	if p.Claims > 1 {
		return true
	}

	for _, r := range p.Steps {
		if r.Name == WriteBackStepCommit {
			return true
		}
	}

	return false
}

// StepPlanner gives the default steps of an operation for the units of one
// tool, so that the job core never knows a command of a tool. The adapter
// of each tool lives in the internal wiring of the server, next to the
// tool package, so a tool package never imports job. The adapter sets
// ChangesResources on its own steps. A blueprint or the settings of a unit
// can replace the defaults, through MarkCustomSteps. A caller of Service
// never gives steps.
type StepPlanner interface {
	// Tool is the lowercase code of the tool, as in a unit reference.
	Tool() string
	// Steps returns the default steps of op for unit at loc. It returns
	// ErrUnsupported for an operation that the tool has no command for.
	Steps(ctx context.Context, unit identity.Object, op Operation, loc Location) ([]Step, error)
	// Editable reports whether a write-back job may edit the file at p,
	// relative to the directory of the code location: a config or variable
	// file of the unit, for example an OpenTofu .tfvars.json file or a
	// Pulumi stack settings file. ValidateEdits uses it.
	Editable(p string) bool
}
