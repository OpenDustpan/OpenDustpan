package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
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
	// that a replan cleared and a reserved plan with no part yet. A plan row
	// names every part of its plan.
	Plans []string
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
	// Logger is the logger of the sweep. The default discards.
	Logger *slog.Logger
	// Now is the time source. The default is time.Now.
	Now func() time.Time
}

// ArtifactSweepResult counts what one run of the sweep did.
type ArtifactSweepResult struct {
	// Jobs is the number of jobs that the run swept.
	Jobs int
	// Deleted is the number of blobs that the run deleted.
	Deleted int
	// Unknown is the number of old blobs that no row names, with a key that
	// is not an artifact blob key. The run keeps them.
	Unknown int
	// Failed is the number of jobs whose sweep failed.
	Failed int
	// Held is true when a running backup or restore stopped the run.
	Held bool
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
		logger: logger, now: now,
	}, nil
}

// errHeld stops a run when a backup or a restore runs, or when the guard
// cannot tell.
var errHeld = errors.New("blob deletes are held")

// Sweep runs the sweep once: first over each job that has not ended, then
// over each job whose state changed in the last ArtifactSweepWindow. A job
// in both passes is swept twice, which deletes nothing more. A failure of one
// job is logged and counted, and the run goes on. It returns an error only
// when a list of jobs fails.
func (s *ArtifactSweeper) Sweep(ctx context.Context) (ArtifactSweepResult, error) {
	var res ArtifactSweepResult

	passes := []Filter{
		{
			Namespace: "", UnitID: "", States: liveStates(), Limit: artifactSweepBatch, After: "",
			StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
		},
		{
			Namespace: "", UnitID: "", States: nil, Limit: artifactSweepBatch, After: "",
			StateChangedBefore: time.Time{}, StateChangedAfter: s.now().Add(-ArtifactSweepWindow),
		},
	}

	for i := range passes {
		if err := s.sweepPass(ctx, passes[i], &res); err != nil {
			if errors.Is(err, errHeld) {
				res.Held = true

				return res, nil
			}

			return res, err
		}
	}

	return res, nil
}

// SweepJob deletes the artifact blobs of j that no row names and that are
// older than the grace period. It returns errHeld, wrapped, when a backup or
// a restore runs or the guard cannot tell, and the error of a read. No log
// line and no error names a blob key.
func (s *ArtifactSweeper) SweepJob(ctx context.Context, j *Job) (ArtifactSweepResult, error) {
	var res ArtifactSweepResult

	ns := j.Spec.Unit.Namespace()

	nsID, err := namespaceIDOf(ctx, s.namespaces, ns)
	if err != nil {
		return res, err
	}

	err = s.blobs.WalkJob(ctx, nsID, j.ID, func(page []ArtifactBlob) error {
		return s.sweepPage(ctx, sweepAt{ns: ns, nsID: nsID, job: j.ID}, page, &res)
	})
	if err != nil {
		return res, fmt.Errorf("sweep the artifact blobs of job %s: %w", j.ID, err)
	}

	return res, nil
}

// sweepPass sweeps each job that f selects, at most maxArtifactSweepPages
// pages of them.
func (s *ArtifactSweeper) sweepPass(ctx context.Context, f Filter, res *ArtifactSweepResult) error {
	for range maxArtifactSweepPages {
		jobs, err := s.jobs.List(ctx, f)
		if err != nil {
			return fmt.Errorf("list jobs: %w", err)
		}

		for i := range jobs {
			one, sweepErr := s.SweepJob(ctx, &jobs[i])
			res.Jobs++
			res.Deleted += one.Deleted
			res.Unknown += one.Unknown

			switch {
			case errors.Is(sweepErr, errHeld):
				return sweepErr
			case sweepErr != nil:
				res.Failed++

				s.logger.WarnContext(ctx, "sweep job artifact blobs", slog.String("job", string(jobs[i].ID)),
					slog.String("error", sweepErr.Error()))
			default:
			}
		}

		if len(jobs) < f.Limit {
			return nil
		}

		f.After = jobs[len(jobs)-1].ID
	}

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

// sweepAt names the job of a sweep: its namespace by name and by ID.
type sweepAt struct {
	ns   namespace.Name
	nsID namespace.ID
	job  ID
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
	now := s.now()

	old := make([]ArtifactBlob, 0, len(page))

	for _, b := range page {
		if now.Sub(b.ModifiedAt) >= blob.UnnamedGracePeriod {
			old = append(old, b)
		}
	}

	if len(old) == 0 {
		return nil
	}

	names, err := s.names.ArtifactNames(ctx, at.ns, at.job)
	if err != nil {
		// A failed read deletes nothing, also for a job that is gone.
		return fmt.Errorf("read the names of the artifact blobs: %w", err)
	}

	named := namedSet(names)

	for _, b := range old {
		key, parseErr := ParseArtifactKey(at.job, b.Rel)
		if parseErr != nil {
			// The sweep deletes only the keys that it knows.
			res.Unknown++

			continue
		}

		if named.has(key, b.Rel) {
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

		res.Deleted++
	}

	return nil
}

// artifactNameSet is the set of the plans and chunk keys that the rows of a
// job name.
type artifactNameSet struct {
	plans  map[string]struct{}
	chunks map[string]struct{}
}

// namedSet returns the set of names.
func namedSet(names ArtifactNames) artifactNameSet {
	set := artifactNameSet{
		plans: make(map[string]struct{}, len(names.Plans)), chunks: make(map[string]struct{}, len(names.ChunkKeys)),
	}

	for _, p := range names.Plans {
		set.plans[p] = struct{}{}
	}

	for _, k := range names.ChunkKeys {
		set.chunks[k] = struct{}{}
	}

	return set
}

// has reports whether a row names the blob rel, which key parsed.
func (n artifactNameSet) has(key ArtifactKey, rel string) bool {
	if key.Plan != "" {
		_, ok := n.plans[key.Plan]

		return ok
	}

	_, ok := n.chunks[rel]

	return ok
}
