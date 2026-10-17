package job

import (
	"context"
	"io"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Durable kinds of the phases. A phase dispatches as one durable operation
// of one of these kinds, so the claim, its expiry and the cancel of a phase
// are the rules of package durable. No other queue holds jobs.
const (
	// KindWriteBack is the kind of a deliver phase. The server instances
	// claim it with a durable.Runner.
	KindWriteBack durable.Kind = "job.write-back"
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

// DispatchKey returns the durable start key of dispatch n of the phase at
// index phase of the job id. Two dispatches of one phase never share a key.
// A retry of one dispatch after a lost answer gives the same key, so it
// gives the same operation.
func DispatchKey(id ID, phase, n int) string {
	return string(id) + "/" + strconv.Itoa(phase) + "/" + strconv.Itoa(n)
}

// Executor is the port that each way of running a job implements. The job
// service calls it at start and when a job leaves the awaiting-state state.
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
	// for write-back, no edit or a forge connection without push access;
	// for a runner, steps that fail ValidateSteps or an unknown pool.
	Phases(ctx context.Context, spec Spec) ([]PhaseKind, error)
	// Abandon undoes what the job left open, after a cancel, a rejection or
	// an expiry of a job that delivered. Write-back closes the review
	// request that it opened when no merge happened. A direct push stays.
	// A runner has nothing to undo and returns nil. It is safe to call more
	// than once.
	Abandon(ctx context.Context, j Job) error
}

// PhaseClaim is the hold of one executor on one dispatch of one phase.
// Claim is the durable claim of the dispatch, with its token and its
// expiry. Every write of the executor carries the phase claim, and the
// store checks its token in the transaction of the write.
type PhaseClaim struct {
	Job   ID
	Phase int
	Claim durable.Claim
}

// ClaimRequest asks for one phase to run. Kinds are the durable kinds that
// the executor runs. Owner names the runner or the instance, for status.
// Lease is the time from the claim to its expiry.
type ClaimRequest struct {
	Owner string
	Kinds []durable.Kind
	Lease time.Duration
}

// Assignment is a claimed phase: the phase claim, the job as it was at the
// claim, and the steps of the phase that an earlier dispatch already
// recorded. It holds no secret. A runner gets the credentials of the
// claim through the runner protocol.
type Assignment struct {
	Claim PhaseClaim
	Job   Job
	// Done are the names of the steps that an earlier dispatch of the
	// phase recorded. The executor does not run them again.
	Done []string
	// Plan is the plan that an apply phase applies, with its hash.
	Plan *PlanRef
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
// ended. Both executors use this port: the write-back executor
// in the server, and the runner protocol for each runner.
type Claimer interface {
	// Claim takes one dispatched phase of req.Kinds, moves its job to
	// running, and returns the assignment. A takeover of a phase whose last
	// executor recorded the start of a step that changes resources, and no
	// end, fails the phase instead and looks for another one. It returns
	// durable.ErrNoWork when no phase can run now, and ErrInvalid for a
	// request with no owner, no kind or a lease that is not positive.
	Claim(ctx context.Context, req ClaimRequest) (Assignment, error)
	// Renew moves the expiry of c to now plus d on the clock of the store,
	// and returns the new claim. It returns ErrCanceled when a cancel
	// arrived: the executor then stops the phase and calls Finish.
	Renew(ctx context.Context, c PhaseClaim, d time.Duration) (PhaseClaim, error)
	// Release gives the phase back before it ends. With no recorded start
	// of a step that changes resources, the phase dispatches again. Else
	// the phase fails. During a cancel, the job ends canceled.
	Release(ctx context.Context, c PhaseClaim) error
	// Finish ends the phase with o, and moves the job by the state machine:
	// to the next phase, to succeeded, to failed, or to canceled during a
	// cancel. Finish of a deliver phase needs a Recorder.Delivered first. It
	// returns ErrCanceled only when the phase did not succeed and a cancel
	// arrived. The job then ends canceled, and the error is for the log.
	Finish(ctx context.Context, c PhaseClaim, o Outcome) error
}

// Recorder is the port through which an executor records the progress of a
// phase. Every method checks the claim in the transaction of its write and
// returns ErrClaimLost when it is not live. During a cancel, BeginStep
// returns ErrCanceled, and the other methods still accept writes, so that
// the executor can record how the phase stopped.
type Recorder interface {
	// BeginStep records that the step called name starts. For a step that
	// changes resources, the executor calls it before it starts the
	// process. It returns ErrCanceled during a cancel, and
	// durable.ErrStepDone when the phase already recorded the end of the
	// step.
	BeginStep(ctx context.Context, c PhaseClaim, name string) error
	// EndStep records the outcome of a step that BeginStep started. It
	// returns ErrInvalid for a step with no begin record.
	EndStep(ctx context.Context, c PhaseClaim, r StepRecord) error
	// AppendLog stores the next chunk of the log of the step called step.
	// Seq starts at 0 and grows by 1. A chunk with a seq that the log has
	// already, and the same SHA-256, changes nothing, so a retry is safe. A
	// chunk with a known seq and another hash returns ErrConflict.
	AppendLog(ctx context.Context, c PhaseClaim, step string, seq int, chunk io.Reader) error
	// PutPlan stores an output of a plan phase and returns its reference.
	// It returns ErrInvalid for an output kind that is not a plan.
	PutPlan(ctx context.Context, c PhaseClaim, kind OutputKind, plan io.Reader) (PlanRef, error)
	// Delivered records the commit of a deliver phase. It returns
	// ErrInvalid when the phase is not a deliver phase.
	Delivered(ctx context.Context, c PhaseClaim, d DeliveryResult) error
}

// StepPlanner gives the default steps of an operation for the units of one
// tool. Each tool package implements it, so that the job core never knows
// a command of a tool. A blueprint or the settings of a unit can replace
// the defaults.
type StepPlanner interface {
	// Tool is the lowercase code of the tool, as in a unit reference.
	Tool() string
	// Steps returns the default steps of op for unit at loc. It returns
	// ErrUnsupported for an operation that the tool has no command for.
	Steps(ctx context.Context, unit identity.Object, op Operation, loc Location) ([]Step, error)
}
