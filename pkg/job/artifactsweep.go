package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The name, the kind and the interval of the schedule of the artifact sweep.
// The name and the kind match, as for the other schedules of the server.
const (
	// ArtifactSweepName is the name of the schedule of the artifact sweep.
	ArtifactSweepName = "job.artifact-sweep"
	// KindArtifactSweep is the durable kind of the operation that one tick of
	// the schedule starts.
	KindArtifactSweep durable.Kind = "job.artifact-sweep"
	// ArtifactSweepVersion is the version of KindArtifactSweep.
	ArtifactSweepVersion = 1
	// ArtifactSweepInterval is the time between two ticks of the schedule.
	ArtifactSweepInterval = time.Hour
	// ArtifactSweepAuditAction is the action of the audit entry that each run
	// of the sweep writes.
	ArtifactSweepAuditAction = "job:artifact-sweep"
	// ArtifactSweepReason is the reason of each delete of the sweep, as its
	// audit entry gives it.
	ArtifactSweepReason = "unnamed job artifact older than the grace period"
	// MaxSweptKeys is the largest number of deleted keys that the result of
	// one run lists. The result sets Truncated when the run deleted more.
	MaxSweptKeys = 1000
	// ArtifactSweepWindow is how long after its last change of state an ended
	// job is still swept. Only a job that runs writes artifact blobs, so the
	// last blob of an ended job is older than the grace period after
	// blob.UnnamedGracePeriod. The window gives each ended job about 24 runs.
	ArtifactSweepWindow = 24*time.Hour + blob.UnnamedGracePeriod
)

// Limits of one run of the artifact sweep.
const (
	// artifactSweepBatch is the number of jobs that one query of the sweep
	// reads.
	artifactSweepBatch = 200
	// maxArtifactSweepPages bounds the pages of jobs that one run reads in
	// each pass, so a very long list of jobs cannot hold a run for longer than
	// a tick. The next run starts from the newest jobs again.
	maxArtifactSweepPages = 10
)

// ArtifactNames is what the rows of one job name in the blob store.
type ArtifactNames struct {
	// Plans holds the ID of each plan row of the job: a stored plan, a plan
	// that a replan cleared and a reserved plan with no part yet.
	Plans []string
	// Parts holds, for each stored plan of Plans, the kinds of the parts that
	// its manifest names. A plan that is not in Parts, or that has no kind in
	// it, is a reserved plan: no row names a blob of it.
	Parts map[string][]OutputKind
	// ChunkKeys holds the blob key of each confirmed chunk of each log version
	// of the job, as ChunkBlobKey reads it.
	ChunkKeys []string
}

// ArtifactKeyStore reads what the rows of a job name in the blob store, for
// the sweep of its artifact blobs. Each SQL store adapter implements it and
// runs the conformance suite in package jobtest.
type ArtifactKeyStore interface {
	// ArtifactNames returns the names of the job id of the namespace ns. It
	// returns ErrNotFound for an unknown job and for a job of another
	// namespace, so the sweep never reads an empty set for a job that it
	// cannot find.
	ArtifactNames(ctx context.Context, ns namespace.Name, id ID) (ArtifactNames, error)
}

// PhaseOperations is the part of durable.Store that the sweep reads: the
// operation of a phase, with its claim.
type PhaseOperations interface {
	// Get returns the operation id, or durable.ErrNotFound.
	Get(ctx context.Context, id durable.ID) (durable.Operation, error)
}

// ArtifactSweeperConfig configures an ArtifactSweeper. Every field but Logger
// and Now is required.
type ArtifactSweeperConfig struct {
	// Jobs lists the jobs to sweep.
	Jobs Store
	// Names reads the blob keys that the rows of a job name.
	Names ArtifactKeyStore
	// Blobs lists and deletes the artifact blobs.
	Blobs *ArtifactBlobs
	// Namespaces gives the ID of the namespace of a job.
	Namespaces NamespaceReader
	// Guard tells whether a backup or a restore runs. The sweep deletes
	// nothing while one runs.
	Guard blobguard.Watch
	// Clock reads the clock of the database. The sweep takes now from it.
	Clock blob.DatabaseClock
	// Operations reads the durable operation of a plan phase, to tell whether
	// the claim that uploads the parts of a plan is still live.
	Operations PhaseOperations
	// Audit takes the audit entry of each run, in the scope of the server.
	Audit audit.ServerStore
	// Logger is the logger of the sweep. The default discards.
	Logger *slog.Logger
	// Now is the clock of the instance, which the sweep compares with the
	// clock of the database. The default is time.Now.
	Now func() time.Time
}

// ArtifactSweepResult is what one run of the sweep did. The durable step of
// the schedule stores it, in JSON, as its output.
type ArtifactSweepResult struct {
	// Skipped is the reason when the run deleted nothing because it could
	// not trust its clock, or because a backup or a restore runs. It is empty
	// for a run that swept.
	Skipped string `json:"skipped,omitempty"`
	// DeletedKeys holds the full key of each deleted blob, in the order of
	// the deletes, at most MaxSweptKeys of them.
	DeletedKeys []string `json:"deletedKeys"`
	// Jobs is the number of jobs that the run swept.
	Jobs int `json:"jobs"`
	// Deleted is the number of blobs that the run deleted.
	Deleted int `json:"deleted"`
	// Unknown is the number of old blobs that the run kept because their key
	// is not an artifact blob key, or because the store gives no modification
	// time for them.
	Unknown int `json:"unknown"`
	// Failed is the number of jobs whose sweep failed.
	Failed int `json:"failed"`
	// Held is true when a running backup or restore stopped the run.
	Held bool `json:"held"`
	// Truncated is true when the run deleted more than MaxSweptKeys blobs, so
	// DeletedKeys does not list all of them.
	Truncated bool `json:"truncated"`
	// Capped is true when a pass of the run read maxArtifactSweepPages pages
	// of jobs and more jobs were left. The next run starts again from the
	// newest jobs, so the jobs after the cap wait for a later run.
	Capped bool `json:"capped"`
}

// add counts one more deleted key, and lists it while the list has room.
func (r *ArtifactSweepResult) add(key string) {
	r.Deleted++

	if len(r.DeletedKeys) < MaxSweptKeys {
		r.DeletedKeys = append(r.DeletedKeys, key)
	} else {
		r.Truncated = true
	}
}

// merge adds the result of the sweep of one job.
func (r *ArtifactSweepResult) merge(one *ArtifactSweepResult) {
	r.Unknown += one.Unknown

	for _, key := range one.DeletedKeys {
		r.add(key)
	}

	// The deletes of one that its list does not hold.
	if extra := one.Deleted - len(one.DeletedKeys); extra > 0 {
		r.Deleted += extra
		r.Truncated = true
	}
}

// ArtifactSweeper deletes the plan and log blobs of jobs that no row names and
// that are older than blob.UnnamedGracePeriod: the blob of a chunk attempt
// that lost to another attempt or whose confirm never committed, and the parts
// of a plan whose row is gone. The commit of each attempt is refused after
// blob.AttemptDeadline, so a blob older than the grace period can never get a
// row. The sweep reads the backups registry after the listing and after the
// read of the names, and again before each delete: a running backup or
// restore, or a failed read, keeps every blob for the next run.
type ArtifactSweeper struct {
	jobs       Store
	names      ArtifactKeyStore
	blobs      *ArtifactBlobs
	namespaces NamespaceReader
	guard      blobguard.Watch
	clock      blob.DatabaseClock
	operations PhaseOperations
	audit      audit.ServerStore
	logger     *slog.Logger
	now        func() time.Time
}

// NewArtifactSweeper returns the sweep of the artifact blobs.
func NewArtifactSweeper(cfg ArtifactSweeperConfig) (*ArtifactSweeper, error) {
	switch {
	case cfg.Jobs == nil:
		return nil, fmt.Errorf("%w: job store", ErrInvalid)
	case cfg.Names == nil:
		return nil, fmt.Errorf("%w: artifact key store", ErrInvalid)
	case cfg.Blobs == nil:
		return nil, fmt.Errorf("%w: artifact blobs", ErrInvalid)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace reader", ErrInvalid)
	case cfg.Guard == nil:
		return nil, fmt.Errorf("%w: blob delete guard", ErrInvalid)
	case cfg.Clock == nil:
		return nil, fmt.Errorf("%w: database clock", ErrInvalid)
	case cfg.Operations == nil:
		return nil, fmt.Errorf("%w: operation reader", ErrInvalid)
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrInvalid)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &ArtifactSweeper{
		jobs: cfg.Jobs, names: cfg.Names, blobs: cfg.Blobs, namespaces: cfg.Namespaces, guard: cfg.Guard,
		clock: cfg.Clock, operations: cfg.Operations, audit: cfg.Audit, logger: logger, now: now,
	}, nil
}

// errHeld stops a run when a backup or a restore runs, or when the guard
// cannot tell.
var errHeld = errors.New("blob deletes are held")

// heldReason is the reason of a run that a backup, a restore or a failed
// read of the guard stopped.
const heldReason = "a backup or a restore runs, or the read of the backups registry failed"

// Sweep runs the sweep once: first over each job that has not ended, then
// over each job whose state changed in the last ArtifactSweepWindow. A job
// in both passes is swept twice, which deletes nothing more. A failure of one
// job is logged and counted, and the run goes on. The run takes now from the
// clock of the database. When the clock of the instance differs from it by
// more than blob.AttemptSkew, or when it cannot be read, the run deletes
// nothing and Skipped gives the reason. Each run, also one that deletes
// nothing, appends one audit entry with its counts and its reason. It returns
// an error when a list of jobs or the audit append fails.
func (s *ArtifactSweeper) Sweep(ctx context.Context) (ArtifactSweepResult, error) {
	res := ArtifactSweepResult{
		Skipped: "", DeletedKeys: []string{}, Jobs: 0, Deleted: 0, Unknown: 0, Failed: 0, Held: false,
		Truncated: false, Capped: false,
	}

	at := s.now()

	now, err := blob.SweepNow(ctx, s.clock, at)

	var runErr error

	if err != nil {
		res.Skipped = err.Error()
	} else {
		at = now
		runErr = s.run(ctx, now, &res)
	}

	if auditErr := s.record(ctx, at, &res); auditErr != nil {
		return res, errors.Join(runErr, auditErr)
	}

	return res, runErr
}

// SweepJob deletes the artifact blobs of j that no row names and that are
// older than the grace period, at now from the clock of the database. It
// returns blob.ErrClockSkew, wrapped, when the clocks do not agree, errHeld,
// wrapped, when a backup or a restore runs or the guard cannot tell, and the
// error of a read. No log line and no error names a blob key. It writes no
// audit entry: Sweep does, for its run.
func (s *ArtifactSweeper) SweepJob(ctx context.Context, j *Job) (ArtifactSweepResult, error) {
	var res ArtifactSweepResult

	now, err := blob.SweepNow(ctx, s.clock, s.now())
	if err != nil {
		return res, fmt.Errorf("sweep the artifact blobs of job %s: %w", j.ID, err)
	}

	err = s.sweepOne(ctx, j, now, &res)

	return res, err
}

// run sweeps the two passes at now.
func (s *ArtifactSweeper) run(ctx context.Context, now time.Time, res *ArtifactSweepResult) error {
	passes := []Filter{
		{
			Namespace: "", UnitID: "", States: liveStates(), Limit: artifactSweepBatch, After: "",
			StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
		},
		{
			Namespace: "", UnitID: "", States: nil, Limit: artifactSweepBatch, After: "",
			StateChangedBefore: time.Time{}, StateChangedAfter: now.Add(-ArtifactSweepWindow),
		},
	}

	for i := range passes {
		if err := s.sweepPass(ctx, now, passes[i], res); err != nil {
			if errors.Is(err, errHeld) {
				res.Held = true
				res.Skipped = heldReason

				return nil
			}

			return err
		}
	}

	return nil
}

// sweepOne sweeps the blobs of j at now into res.
func (s *ArtifactSweeper) sweepOne(ctx context.Context, j *Job, now time.Time, res *ArtifactSweepResult) error {
	ns := j.Spec.Unit.Namespace()

	nsID, err := namespaceIDOf(ctx, s.namespaces, ns)
	if err != nil {
		return err
	}

	at := sweepAt{ns: ns, nsID: nsID, job: j.ID, now: now, current: j}

	err = s.blobs.WalkJob(ctx, nsID, j.ID, func(page []ArtifactBlob) error {
		return s.sweepPage(ctx, at, page, res)
	})
	if err != nil {
		return fmt.Errorf("sweep the artifact blobs of job %s: %w", j.ID, err)
	}

	return nil
}

// sweepPass sweeps each job that f selects, at most maxArtifactSweepPages
// pages of them. A damaged row holds no readable job, so there is nothing to
// sweep: the pass reports it and goes on.
func (s *ArtifactSweeper) sweepPass(ctx context.Context, now time.Time, f Filter, res *ArtifactSweepResult) error {
	for range maxArtifactSweepPages {
		page, err := s.jobs.List(ctx, f)
		if err != nil {
			return fmt.Errorf("list jobs: %w", err)
		}

		for i := range page.Damaged {
			s.logger.WarnContext(ctx, "sweep job artifact blobs skipped a damaged job row",
				slog.String("job", string(page.Damaged[i].ID)), slog.String("reason", page.Damaged[i].Reason))
		}

		for i := range page.Jobs {
			var one ArtifactSweepResult

			sweepErr := s.sweepOne(ctx, &page.Jobs[i], now, &one)
			res.Jobs++
			res.merge(&one)

			switch {
			case errors.Is(sweepErr, errHeld):
				return sweepErr
			case sweepErr != nil:
				res.Failed++

				s.logger.WarnContext(ctx, "sweep job artifact blobs", slog.String("job", string(page.Jobs[i].ID)),
					slog.String("error", sweepErr.Error()))
			default:
			}
		}

		// An empty cursor means that the page reached the end of the list.
		if page.After == "" {
			return nil
		}

		f.After = page.After
	}

	// The pass stopped at the page cap and a row is left after the last page,
	// so the result reports that jobs are left.
	res.Capped = true

	return nil
}

// liveStates returns the states of a job that has not ended.
func liveStates() []State {
	var out []State

	for _, st := range States() {
		if !st.Terminal() {
			out = append(out, st)
		}
	}

	return out
}

// sweepAt names the job of a sweep, its namespace by name and by ID, and the
// time of the run on the clock of the database.
type sweepAt struct {
	now  time.Time
	ns   namespace.Name
	nsID namespace.ID
	job  ID
	// current is the job as the sweep read it, for the claim of its phase.
	current *Job
}

// sweepPage deletes the blobs of one page that are older than the grace
// period and that no row names. It reads the names after the listing, and only
// when old blobs remain.
func (s *ArtifactSweeper) sweepPage(
	ctx context.Context,
	at sweepAt,
	page []ArtifactBlob,
	res *ArtifactSweepResult,
) error {
	old := make([]ArtifactBlob, 0, len(page))

	for _, b := range page {
		switch {
		case b.ModifiedAt.IsZero():
			// The store did not say when it wrote the blob, so the blob is
			// never old: the sweep keeps it and counts it.
			res.Unknown++
		case blob.OldEnough(b.ModifiedAt, at.now):
			old = append(old, b)
		default:
		}
	}

	if len(old) == 0 {
		return nil
	}

	named, live, err := s.readNames(ctx, &at)
	if err != nil {
		return err
	}

	for _, b := range old {
		key, parseErr := ParseArtifactKey(at.job, b.Rel)
		if parseErr != nil {
			// The sweep deletes only the keys that it knows.
			res.Unknown++

			continue
		}

		if named.has(key, b.Rel, live) {
			continue
		}

		// The guard comes after the listing and after the read of the names.
		// A blob that is unnamed here lost its row, or never had one, before
		// that read, so a backup that the guard does not see cannot name it.
		// The guard is read again before each delete, so a restore that starts
		// during the loop keeps the blobs that are left.
		if holdErr := blobguard.Hold(ctx, s.guard); holdErr != nil {
			return fmt.Errorf("%w: %w", errHeld, holdErr)
		}

		if delErr := s.blobs.DeleteJobBlob(ctx, at.nsID, at.job, b.Rel); delErr != nil {
			return fmt.Errorf("delete an artifact blob: %w", delErr)
		}

		res.add("namespaces/" + at.nsID.String() + "/" + b.Rel)
	}

	return nil
}

// readNames reads what the rows of the job name, and the plan of its live claim.
// The live plan comes before the names. A claim that is not live now never
// stores a plan later, so the names that follow show its final state. A claim
// that is live now keeps its plan, also if it commits after the read of the
// names. A failed read deletes nothing, also for a job that is gone.
func (s *ArtifactSweeper) readNames(ctx context.Context, at *sweepAt) (artifactNameSet, string, error) {
	live, err := s.liveClaimPlan(ctx, at)
	if err != nil {
		return artifactNameSet{}, "", err
	}

	names, err := s.names.ArtifactNames(ctx, at.ns, at.job)
	if err != nil {
		return artifactNameSet{}, "", fmt.Errorf("read the names of the artifact blobs: %w", err)
	}

	return namedSet(names), live, nil
}

// liveClaimPlan returns the ID of the plan that the live claim of the job
// stores, or an empty ID when no claim stores one. A claim stores a plan only in
// a plan phase of a job that has not ended, while its operation runs and its
// lease has not passed at the time of the run. The ID comes from the claim, as
// PlanIDOf makes it. A failed read of the operation is an error, so the sweep
// does not take a claim for lost when it cannot tell.
func (s *ArtifactSweeper) liveClaimPlan(ctx context.Context, at *sweepAt) (string, error) {
	j := at.current
	if j == nil || j.State.Terminal() || j.Current < 0 || j.Current >= len(j.Phases) {
		return "", nil
	}

	phase := &j.Phases[j.Current]
	if phase.Kind != PhasePlan || phase.Operation == "" {
		return "", nil
	}

	op, err := s.operations.Get(ctx, durable.ID(phase.Operation))
	if errors.Is(err, durable.ErrNotFound) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("read the operation of the plan phase of job %s: %w", j.ID, err)
	}

	if op.State != durable.StateRunning || !at.now.Before(op.Claim.ExpiresAt) {
		return "", nil
	}

	claim := PhaseClaim{
		Job: j.ID, Phase: j.Current, Claim: op.Claim, Fence: namespace.Fence{Namespace: "", FailoverVersion: 0},
	}

	return PlanIDOf(claim), nil
}

// artifactNameSet is the set of the plans, the parts of the stored plans and the
// chunk keys that the rows of a job name.
type artifactNameSet struct {
	plans  map[string]struct{}
	parts  map[string]map[OutputKind]struct{}
	chunks map[string]struct{}
}

// namedSet returns the set of names.
func namedSet(names ArtifactNames) artifactNameSet {
	set := artifactNameSet{
		plans:  make(map[string]struct{}, len(names.Plans)),
		parts:  make(map[string]map[OutputKind]struct{}, len(names.Parts)),
		chunks: make(map[string]struct{}, len(names.ChunkKeys)),
	}

	for _, p := range names.Plans {
		set.plans[p] = struct{}{}
	}

	for plan, kinds := range names.Parts {
		set.parts[plan] = make(map[OutputKind]struct{}, len(kinds))

		for _, k := range kinds {
			set.parts[plan][k] = struct{}{}
		}
	}

	for _, k := range names.ChunkKeys {
		set.chunks[k] = struct{}{}
	}

	return set
}

// has reports whether the sweep keeps the blob rel, which key parsed. A row
// names a log chunk by its blob key. A stored plan names the parts of its
// manifest and no other blob. A reserved plan names none: the blobs of the plan
// of the live claim stay, as the claim can still store the plan, and the blobs
// of any other reserved plan go, because their claim ended and none stores
// them.
func (n artifactNameSet) has(key ArtifactKey, rel, live string) bool {
	if key.Plan == "" {
		_, ok := n.chunks[rel]

		return ok
	}

	if _, ok := n.plans[key.Plan]; !ok {
		return false
	}

	if kinds := n.parts[key.Plan]; len(kinds) > 0 {
		_, ok := kinds[key.Kind]

		return ok
	}

	return key.Plan == live
}

// record appends the audit entry of one run at the time at: the sweep, its
// counts and the reason of its deletes, or the reason that it deleted nothing.
// The entry names no blob key: the output of the step lists them.
func (s *ArtifactSweeper) record(ctx context.Context, at time.Time, res *ArtifactSweepResult) error {
	reason := fmt.Sprintf("%s: %d jobs swept, %d blobs deleted, %d kept unknown, %d jobs failed",
		ArtifactSweepReason, res.Jobs, res.Deleted, res.Unknown, res.Failed)
	if res.Skipped != "" {
		reason = "deleted nothing: " + res.Skipped
	}

	if res.Skipped == "" && res.Capped {
		reason += ", capped at the page limit"
	}

	if len([]rune(reason)) > audit.MaxReasonLen {
		reason = string([]rune(reason)[:audit.MaxReasonLen])
	}

	entry := audit.Entry{
		At: at.UTC(), Namespace: audit.ServerScope, Subject: history.ServerSubject, IDPSubject: "", TokenID: "",
		Action: ArtifactSweepAuditAction, Object: ArtifactSweepName, Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelFrom(ctx),
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: audit.PrincipalKindService, JobID: "",
		Reason: reason,
	}

	if _, err := s.audit.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append the audit entry of the artifact sweep: %w", err)
	}

	return nil
}
