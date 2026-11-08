package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ jobtoken.Store = (*JobTokens)(nil)

// JobTokens is the store of job tokens on the database of a Store. It
// implements jobtoken.Store.
type JobTokens struct {
	store *Store
}

// JobTokens returns the store of job tokens on s.
func (s *Store) JobTokens() *JobTokens {
	return &JobTokens{store: s}
}

// Issue implements jobtoken.Store.
func (t *JobTokens) Issue(
	ctx context.Context, tok jobtoken.Token, fence namespace.Fence, e audit.Entry,
) error {
	// Defense in depth: Service.Issue validates the token too; this check
	// stays so that a caller that skips the service cannot write an invalid
	// record.
	if err := tok.Validate(); err != nil {
		return err
	}

	return t.store.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		return t.issueRecord(ctx, q, tok, fence, e)
	})
}

// GetByDigest implements jobtoken.Store. One statement reads the record and
// the live claim.
func (t *JobTokens) GetByDigest(ctx context.Context, d jobtoken.Digest) (jobtoken.Match, error) {
	row, err := t.store.queries.GetJobToken(ctx, d.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return jobtoken.Match{}, jobtoken.ErrNotFound
	}

	if err != nil {
		return jobtoken.Match{}, fmt.Errorf("select job token: %w", err)
	}

	// Defense in depth: the service checks the unit ID of the job too; this
	// check stays so that a renamed namespace cannot redirect a token.
	if row.Namespace != row.UnitNamespace {
		return jobtoken.Match{}, jobtoken.ErrNotFound
	}

	if row.Namespace != row.JobNamespace {
		return jobtoken.Match{}, jobtoken.ErrNotFound
	}

	unit, err := unitObject(row.UnitTool, row.UnitNamespace, row.UnitName)
	if err != nil {
		return jobtoken.Match{}, err
	}

	var starter identity.Principal

	starter.Subject = row.StarterSubject
	starter.DisplayName = row.StarterDisplayName
	starter.Kind = identity.Kind(row.StarterKind)

	var m jobtoken.Match

	m.Token = jobtoken.Token{
		Digest:         d,
		StarterID:      row.StarterID,
		JobID:          job.ID(row.JobID),
		Namespace:      namespace.Name(row.UnitNamespace),
		Unit:           unit.WithUnitID(row.UnitID),
		ClaimOperation: durable.ID(row.ClaimOperation),
		ClaimToken:     row.ClaimToken,
		ExpiresAt:      codec.FromMillis(row.ExpiresAtMs),
		CreatedAt:      codec.FromMillis(row.CreatedAtMs),
	}
	m.Claim = jobtoken.Claim{
		OperationState:      durable.State(row.OperationState),
		LeaseEndsAt:         leaseEnd(row.OperationClaimExpiresAtMs),
		JobID:               job.ID(row.JobRecordID),
		JobState:            job.State(row.JobState),
		JobOperation:        durable.ID(row.JobOperation),
		JobUnitID:           row.JobUnitID,
		JobStarterID:        row.JobStarterID,
		Starter:             starter,
		StarterRecordID:     row.StarterRecordID,
		OperationClaimToken: row.OperationClaimToken,
		StarterEnabled:      !row.StarterDisabledAtMs.Valid,
	}

	return m, nil
}

// issueRecord writes tok and its entry in the transaction of q.
func (t *JobTokens) issueRecord(
	ctx context.Context, q *db.Queries, tok jobtoken.Token, fence namespace.Fence, e audit.Entry,
) error {
	if fence.Namespace != tok.Namespace {
		return namespace.ErrFenced
	}

	// Defense in depth: Service.Issue checks the expiry against its clock
	// too; this check stays so that the store refuses a token that expired
	// at the time of its entry.
	if !tok.ExpiresAt.After(e.At) {
		return fmt.Errorf("%w: the expiry is not after the issue time", jobtoken.ErrInvalid)
	}

	// The insert runs before the reads so that a duplicate digest answers
	// ErrAlreadyExists before a missing job. An error after it rolls the
	// transaction back, so it writes nothing.
	if err := insertJobToken(ctx, q, tok); err != nil {
		return err
	}

	world, err := loadIssueWorld(ctx, q, tok)
	if err != nil {
		return err
	}

	return checkIssueLive(tok, &world, e.At)
}

// issueWorld is the record that an issue checks against the job and the
// claim.
type issueWorld struct {
	job          db.GetJobRow
	claim        db.DurableOperation
	starter      db.GetPrincipalRow
	jobStarterID string
}

// loadIssueWorld reads the job, the claim and the starters of an issue. It
// returns the first error of the port order.
func loadIssueWorld(ctx context.Context, q *db.Queries, tok jobtoken.Token) (issueWorld, error) {
	var w issueWorld

	jobRow, err := q.GetJob(ctx, string(tok.JobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return w, jobtoken.ErrNotFound
	}

	if err != nil {
		return w, fmt.Errorf("select job: %w", err)
	}

	w.job = jobRow

	startedBy, err := jobStartedBy(jobRow.Body)
	if err != nil {
		return w, fmt.Errorf("decode job %s: %w", tok.JobID, err)
	}

	claim, err := q.GetDurableOperation(ctx, string(tok.ClaimOperation))
	if errors.Is(err, pgx.ErrNoRows) {
		return w, jobtoken.ErrNotFound
	}

	if err != nil {
		return w, fmt.Errorf("select durable operation: %w", err)
	}

	w.claim = claim

	starter, err := q.GetPrincipal(ctx, tok.StarterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, jobtoken.ErrNotLive
	}

	if err != nil {
		return w, fmt.Errorf("select starter principal: %w", err)
	}

	if starter.DisabledAtMs.Valid {
		return w, jobtoken.ErrNotLive
	}

	w.starter = starter

	jobStarter, err := q.GetPrincipalBySubject(ctx, startedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		// Defense in depth: Service.Issue checks the starter against the job
		// too; this check stays so that a caller that skips the service
		// cannot issue a token under another principal.
		return w, fmt.Errorf("%w: the starter is not the job starter", jobtoken.ErrInvalid)
	}

	if err != nil {
		return w, fmt.Errorf("select job starter principal: %w", err)
	}

	w.jobStarterID = jobStarter.ID

	return w, nil
}

// checkIssueLive refuses an issue whose starter is not the job starter or
// whose claim or job is not live.
func checkIssueLive(tok jobtoken.Token, w *issueWorld, at time.Time) error {
	// Defense in depth: Service.Issue checks the starter against the job too;
	// this check stays so that a caller that skips the service cannot issue a
	// token under another principal.
	if w.jobStarterID != tok.StarterID {
		return fmt.Errorf("%w: the starter is not the job starter", jobtoken.ErrInvalid)
	}

	// Defense in depth: Service.Issue checks the claim against the current
	// operation of the job too; this check stays so that a caller that skips
	// the service cannot issue a token for an operation that the job left.
	if !claimLive(tok, &w.claim, at) || !jobtoken.JobStateLive(job.State(w.job.State)) ||
		w.job.CurrentOperation != string(tok.ClaimOperation) || w.job.UnitID != tok.Unit.UnitID() {
		return jobtoken.ErrNotLive
	}

	return nil
}

// claimLive reports whether claim is the live claim of tok.
func claimLive(tok jobtoken.Token, claim *db.DurableOperation, at time.Time) bool {
	return claim.State == string(durable.StateRunning) && claim.ClaimToken == tok.ClaimToken &&
		claim.ClaimExpiresAtMs.Valid && claim.ClaimExpiresAtMs.Int64 > at.UnixMilli()
}

// insertJobToken writes the record of tok.
func insertJobToken(ctx context.Context, q *db.Queries, tok jobtoken.Token) error {
	err := q.InsertJobToken(ctx, db.InsertJobTokenParams{
		Digest:         tok.Digest.String(),
		Namespace:      tok.Namespace.String(),
		UnitNamespace:  tok.Namespace.String(),
		StarterID:      tok.StarterID,
		JobID:          string(tok.JobID),
		UnitTool:       tok.Unit.Tool(),
		UnitID:         tok.Unit.UnitID(),
		UnitName:       tok.Unit.UnitName(),
		ClaimOperation: string(tok.ClaimOperation),
		ClaimToken:     tok.ClaimToken,
		ExpiresAtMs:    codec.Millis(tok.ExpiresAt),
		CreatedAtMs:    codec.Millis(tok.CreatedAt),
	})
	if isUniqueViolation(err) {
		return jobtoken.ErrAlreadyExists
	}

	if err != nil {
		return fmt.Errorf("insert job token: %w", err)
	}

	return nil
}

// jobStartedBy returns the subject of the principal that started the job
// whose body is body.
func jobStartedBy(body string) (string, error) {
	var b struct {
		StartedBy string `json:"startedBy"`
	}

	if err := json.Unmarshal([]byte(body), &b); err != nil {
		return "", fmt.Errorf("decode job body: %w", err)
	}

	return b.StartedBy, nil
}

// leaseEnd returns the end of a claim lease, or the zero time for no lease.
func leaseEnd(ms pgtype.Int8) time.Time {
	if !ms.Valid {
		return time.Time{}
	}

	return codec.FromMillis(ms.Int64)
}
