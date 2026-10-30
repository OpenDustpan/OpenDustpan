package storecheck

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
)

// The budget of one check, and the wait between two checks of one target.
// The server fixes these figures. No flag and no setting changes them.
const (
	// MaxObjects is the most objects that one check writes.
	MaxObjects = 1000
	// MaxTotalBytes is the most bytes that one check writes in total: 256 MiB.
	MaxTotalBytes int64 = 256 << 20
	// MaxObjectBytes is the most bytes that one check writes in one object:
	// 64 MiB.
	MaxObjectBytes int64 = 64 << 20
	// Timeout is the time after which a check stops.
	Timeout = 10 * time.Minute
	// Cooldown is the time after the end of a check in which a new check on
	// the same target answers ErrCooldown.
	Cooldown = 15 * time.Minute
)

// The durable operation of a check.
const (
	// OperationKind is the kind of the durable operation of a check.
	OperationKind durable.Kind = "store-check"
	// OperationVersion is the version of the kind.
	OperationVersion = 1
	// StepRun is the step that runs the cases on the throwaway prefix.
	StepRun = "run"
	// StepFinish is the step that stores the report in the row.
	StepFinish = "finish"

	operationKeyPrefix = "store-check/"
)

// OperationKey returns the start key of the operation of check id.
func OperationKey(id ID) string {
	return operationKeyPrefix + id.String()
}

// auditObject is the object of an audit entry that names no check.
const auditObject = "store-check"

// cleanupTimeout bounds the delete of the throwaway prefix at the end of a
// check, which runs after the time limit of the cases.
const cleanupTimeout = 2 * time.Minute

// reapWindow is the age after which the prefix reaper stops to look at the
// prefix of an ended check. A target with object lock refuses the delete for
// good, so the reaper must not try for ever.
const reapWindow = 24 * time.Hour

// AuditAppender appends entries to the audit log of the installation.
type AuditAppender interface {
	// AppendServer writes e in the scope of the installation.
	AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error)
}

// Limits holds the budget of a check. The server uses the defaults. A zero
// field takes its default.
type Limits struct {
	MaxObjects     int
	MaxTotalBytes  int64
	MaxObjectBytes int64
	Timeout        time.Duration
	Cooldown       time.Duration
}

func (l Limits) withDefaults() Limits {
	return Limits{
		MaxObjects:     cmp.Or(l.MaxObjects, MaxObjects),
		MaxTotalBytes:  cmp.Or(l.MaxTotalBytes, MaxTotalBytes),
		MaxObjectBytes: cmp.Or(l.MaxObjectBytes, MaxObjectBytes),
		Timeout:        cmp.Or(l.Timeout, Timeout),
		Cooldown:       cmp.Or(l.Cooldown, Cooldown),
	}
}

// Config holds the dependencies of a Service.
type Config struct {
	// Records keeps the rows of the checks. It starts the operation in the
	// transaction of the row, so Records and Operations must name one
	// database.
	Records Records
	// Operations keeps the durable operation of each check.
	Operations durable.Store
	// Blobs is the live blob store of the installation (target "blobs").
	Blobs blob.Store
	// Backup is the backup target (target "backup"). Nil when the server has
	// none.
	Backup blob.Store
	// Audit receives the entries of the checks.
	Audit AuditAppender
	// Admit refuses a start, and a run, while the installation is in
	// maintenance mode or a restore runs. It returns any error to refuse.
	// Nil admits everything.
	Admit func(ctx context.Context) error
	// Limits is the budget. Tests set it; the server leaves it zero.
	Limits Limits
	Now    func() time.Time
	// Owner names this instance in the claims on check operations, for
	// status only. An empty Owner means "store-check".
	Owner string
	// Lease is the time from a claim or a renewal to its expiry. Zero means
	// the default of the durable runner.
	Lease time.Duration
	// Logger receives the messages of the service. Nil discards them.
	Logger *slog.Logger
}

// Service runs the store checks of an installation.
type Service struct {
	records Records
	// stores holds the store of each target that the server has set.
	stores map[Target]blob.Store
	audit  AuditAppender
	admit  func(ctx context.Context) error
	limits Limits
	now    func() time.Time
	runner *durable.Runner
	lease  time.Duration
	logger *slog.Logger
}

// NewService returns a Service. Records, Operations, Blobs and Audit are
// required.
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.Records == nil:
		return nil, fmt.Errorf("%w: records", ErrMissingDependency)
	case cfg.Operations == nil:
		return nil, fmt.Errorf("%w: durable operations", ErrMissingDependency)
	case cfg.Blobs == nil:
		return nil, fmt.Errorf("%w: blob store", ErrMissingDependency)
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit log", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	stores := map[Target]blob.Store{TargetBlobs: cfg.Blobs}
	if cfg.Backup != nil {
		stores[TargetBackup] = cfg.Backup
	}

	s := &Service{
		records: cfg.Records,
		stores:  stores,
		audit:   cfg.Audit,
		admit:   cfg.Admit,
		limits:  cfg.Limits.withDefaults(),
		now:     now,
		runner:  nil,
		lease:   cmp.Or(cfg.Lease, durable.DefaultLease),
		logger:  cmp.Or(cfg.Logger, slog.New(slog.DiscardHandler)),
	}

	runner, err := newRunner(s, cfg)
	if err != nil {
		return nil, err
	}

	s.runner = runner

	return s, nil
}

// StartRequest asks for one check.
type StartRequest struct {
	// Target names the store to check: "blobs" or "backup". Any other value
	// returns ErrInvalidTarget.
	Target string
	// Actor is the principal that asked.
	Actor Actor
	// Entry holds the request data of the audit entry of the start (the
	// client address, the user agent, the request ID and the channel). The
	// service fills the other fields.
	Entry audit.Entry
}

// Start stores a running check and starts the durable operation that runs it,
// in one transaction, and returns the ID at once. A worker of any instance
// claims the operation. It returns ErrInvalidTarget for a target that is not
// on the fixed list, ErrNoTarget for a target that the server has not set,
// ErrRefused while the installation is in maintenance mode or a restore
// runs, ErrRunning while a check runs, and ErrCooldown while the last check
// on the target ended less than Cooldown ago. The start and each denial
// write an audit entry.
func (s *Service) Start(ctx context.Context, req StartRequest) (ID, error) {
	target, err := ParseTarget(req.Target)
	if err != nil {
		s.auditStart(ctx, req, denial{object: auditObject, why: "the target is not on the list", target: req.Target})

		return "", err
	}

	if _, ok := s.stores[target]; !ok {
		s.auditStart(
			ctx,
			req,
			denial{object: auditObject, why: "the target is not configured", target: string(target)},
		)

		return "", fmt.Errorf("%w: %s", ErrNoTarget, target)
	}

	if admitErr := s.admitNow(ctx); admitErr != nil {
		s.auditStart(
			ctx,
			req,
			denial{object: auditObject, why: "maintenance mode or a restore", target: string(target)},
		)

		return "", admitErr
	}

	id := NewID()
	startedAt := s.now().UTC()

	encoded, err := json.Marshal(input{ID: id, Target: target, Actor: req.Actor, StartedAt: startedAt})
	if err != nil {
		return "", fmt.Errorf("encode input of store check %s: %w", id, err)
	}

	start, err := s.runner.StartRequest(OperationKind, OperationKey(id), encoded)
	if err != nil {
		return "", fmt.Errorf("begin store check %s: %w", id, err)
	}

	row := Record{
		StartedAt: startedAt, EndedAt: time.Time{}, ID: id, Target: target, State: StateRunning,
		RequestedBy: req.Actor.Subject, Error: "", Report: nil,
	}

	if createErr := s.records.StartCheck(ctx, row, start, startedAt.Add(-s.limits.Cooldown)); createErr != nil {
		s.auditRefusedStart(ctx, req, target, createErr)

		return "", fmt.Errorf("create store check %s: %w", id, createErr)
	}

	s.auditStart(ctx, req, denial{object: "store-check " + id.String(), why: "", target: string(target)})

	return id, nil
}

// Get returns the row of check id, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id ID) (Record, error) {
	rec, err := s.records.GetCheck(ctx, id)
	if err != nil {
		return Record{}, fmt.Errorf("read store check: %w", err)
	}

	return rec, nil
}

// List returns every check, newest first.
func (s *Service) List(ctx context.Context) ([]Record, error) {
	recs, err := s.records.ListChecks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list store checks: %w", err)
	}

	return recs, nil
}

// DecodeReport reads the report of a row. It returns false when the row has no
// report.
func DecodeReport(rec Record) (Report, bool, error) {
	var report Report

	if len(rec.Report) == 0 {
		return report, false, nil
	}

	if err := json.Unmarshal(rec.Report, &report); err != nil {
		return Report{}, false, fmt.Errorf("decode report of store check %s: %w", rec.ID, err)
	}

	return report, true, nil
}

// Work claims and runs check operations, and runs the prefix reaper, until
// ctx ends. A worker process calls it.
func (s *Service) Work(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Go(func() { s.runner.Run(ctx) })
	wg.Go(func() { s.maintain(ctx) })

	wg.Wait()
}

// MaintainOnce does one round of the maintenance: it marks failed the rows of
// checks whose operation ended without a finish write, and reaps the prefixes
// of ended checks. A failure is not final: the next round tries again.
func (s *Service) MaintainOnce(ctx context.Context) {
	n, err := s.records.FailEndedChecks(ctx, s.now().UTC())
	if err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "store check rows of ended operations were not marked failed",
			slog.String("error", errorClass(err)))
	} else if n > 0 {
		s.logger.WarnContext(ctx, "store checks ended without a finish write were marked failed",
			slog.Int("checks", n))
	}

	s.reapEnded(ctx)
}

// Scope returns the view of the throwaway prefix of check id on the store of
// target. It is the first two layers of the containment: it refuses a prefix
// that is not exactly the prefix of id with ErrPrefixRefused and writes a
// denial entry, and the view it returns refuses a key outside the prefix with
// blob.ErrOutOfScope.
func (s *Service) Scope(ctx context.Context, target Target, id ID, prefix string) (*blob.Scoped, error) {
	store, ok := s.stores[target]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoTarget, target)
	}

	return s.viewOf(ctx, store, id, prefix)
}

// RunOnce claims one check operation and runs it to its end. It returns
// durable.ErrNoWork when no operation can run now. A test, or a command that
// waits for its own check, calls it; a worker process calls Work.
func (s *Service) RunOnce(ctx context.Context) error {
	if err := s.runner.RunOnce(ctx); err != nil {
		return fmt.Errorf("run store check: %w", err)
	}

	return nil
}

// newRunner builds the runner that claims and runs the check operations.
func newRunner(s *Service, cfg Config) (*durable.Runner, error) {
	runner, err := durable.NewRunner(durable.RunnerConfig{
		Store: cfg.Operations,
		Owner: cmp.Or(cfg.Owner, "store-check"),
		Kinds: []durable.KindSpec{{
			Kind:       OperationKind,
			Version:    OperationVersion,
			MinVersion: OperationVersion,
			Steps: []durable.Step{
				{Name: StepRun, Run: s.stepRun},
				{Name: StepFinish, Run: s.stepFinish},
			},
		}},
		Lease:        cfg.Lease,
		RenewEvery:   0,
		PollEvery:    0,
		MaxAttempts:  0,
		ExpiryLimit:  0,
		Backoff:      durable.Backoff{Base: 0, Max: 0},
		ClaimBackoff: durable.Backoff{Base: 0, Max: 0},
		WriteTimeout: 0,
		// A run that meets maintenance mode or a restore waits: the attempt
		// does not count as a failure.
		Deferred:           func(err error) bool { return errors.Is(err, ErrRefused) },
		StopOnRenewalError: false,
		Logger:             cfg.Logger,
		Rand:               nil,
	})
	if err != nil {
		return nil, fmt.Errorf("store check runner: %w", err)
	}

	return runner, nil
}

// denial describes an audit entry of a start: the object, the reason of a
// refusal (empty for a start that worked) and the target text.
type denial struct {
	object string
	why    string
	target string
}

// auditStart writes the start entry, or the denial entry of a start.
func (s *Service) auditStart(ctx context.Context, req StartRequest, d denial) {
	entry := s.fill(req.Entry, req.Actor, d.object)
	entry.Reason = d.why

	if d.why != "" {
		entry.Outcome = audit.OutcomeDenied
	}

	entry.Changes = []audit.Change{{Key: "target", Old: nil, New: sanitize(d.target), Layer: "", TakesEffect: ""}}

	s.appendAudit(ctx, entry)
}

// auditRefusedStart writes the denial entry for the refusals of the row
// store: a running check and a target in its cooldown.
func (s *Service) auditRefusedStart(ctx context.Context, req StartRequest, target Target, err error) {
	switch {
	case errors.Is(err, ErrRunning):
		s.auditStart(ctx, req, denial{object: auditObject, why: "a check runs", target: string(target)})
	case errors.Is(err, ErrCooldown):
		s.auditStart(ctx, req, denial{object: auditObject, why: "the target is cooling down", target: string(target)})
	default:
	}
}

// sanitize keeps a target from the request in an entry only when it is on the
// fixed list. Any other value is untrusted text and never reaches the log.
func sanitize(target string) string {
	if t, err := ParseTarget(target); err == nil {
		return string(t)
	}

	return "invalid"
}

// admitNow asks the admit hook. Any error from it is a refusal.
func (s *Service) admitNow(ctx context.Context) error {
	if s.admit == nil {
		return nil
	}

	if err := s.admit(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}

	return nil
}

// maintain runs MaintainOnce once in each lease period until ctx ends.
func (s *Service) maintain(ctx context.Context) {
	ticker := time.NewTicker(s.lease)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.MaintainOnce(ctx)
		}
	}
}

// fill sets the fields of an audit entry that every entry of a check shares.
func (s *Service) fill(e audit.Entry, actor Actor, object string) audit.Entry {
	e.At = s.now().UTC()
	e.Namespace = audit.ServerScope
	e.Subject = actor.Subject
	e.TokenID = actor.TokenID
	e.PrincipalKind = actor.PrincipalKind
	e.Action = string(identity.ActionInstallationStoreCheck)
	e.Object = object
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess

	if e.Channel == "" {
		e.Channel = audit.ChannelAPI
	}

	return e
}

// entry returns an entry for the actor with no request data.
func (s *Service) entry(actor Actor, object string) audit.Entry {
	return s.fill(audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: "",
		Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}, actor, object)
}
