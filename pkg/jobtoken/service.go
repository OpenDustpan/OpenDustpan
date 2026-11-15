package jobtoken

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// Service issues job tokens and authenticates them. It reads the live claim
// and the job at each request, so a token stops without a revoke.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService returns a Service. A nil now uses time.Now.
func NewService(store Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}

	return &Service{store: store, now: now}
}

// IssueRequest asks for a job token.
type IssueRequest struct {
	// StarterID is the ID of the principal that started the job.
	StarterID string
	// StarterSubject is the subject of the principal that started the job.
	StarterSubject string
	// Job is the job of the token. Its unit must hold a stable unit ID.
	Job job.Job
	// Claim is the live claim of the current operation of the job.
	Claim durable.Claim
	// ExpiresAt is the hard expiry of the token: the deadline that
	// runner.LimitsFor gives for the steps of the job.
	ExpiresAt time.Time
	// Fence is the fence of the namespace of the unit.
	Fence namespace.Fence
	// Entry is the audit entry of the issue. Issue copies the entry and sets
	// its At to the time of the service clock, so the record, the audit entry
	// and the store check use one time.
	Entry audit.Entry
}

// Issue returns a new secret for a job token of the request. It refuses a
// request with no steps, an expiry that is not after the time of the service
// clock, or an expiry after the deadline that runner.LimitsFor gives for the
// steps, with ErrInvalid, and it writes nothing. It reads the service clock
// once: the creation time of the record and the At of the audit entry are
// that time. A store error comes back wrapped, so the caller never gets a
// secret for a failed issue.
func (s *Service) Issue(ctx context.Context, req *IssueRequest) (Secret, error) {
	if req == nil {
		return Secret{}, fmt.Errorf("%w: missing request", ErrInvalid)
	}

	now := s.now().UTC()
	if len(req.Job.Spec.Steps) == 0 {
		return Secret{}, fmt.Errorf("%w: the job has no steps", ErrInvalid)
	}

	if !req.ExpiresAt.After(now) {
		return Secret{}, fmt.Errorf("%w: the expiry is not after the issue time", ErrInvalid)
	}

	if req.ExpiresAt.After(now.Add(runner.LimitsFor(req.Job.Spec.Steps).Deadline)) {
		return Secret{}, fmt.Errorf("%w: the expiry is above the deadline of the phase", ErrInvalid)
	}

	secret, err := NewSecret()
	if err != nil {
		return Secret{}, err
	}

	var t Token

	t.Digest = DigestOf(secret)
	t.StarterID = req.StarterID
	t.JobID = req.Job.ID
	t.Namespace = req.Job.Spec.Unit.Namespace()
	t.Unit = req.Job.Spec.Unit
	t.ClaimOperation = req.Claim.Operation
	t.ClaimToken = req.Claim.Token
	t.ExpiresAt = req.ExpiresAt
	t.CreatedAt = now

	if err := t.Validate(); err != nil {
		return Secret{}, fmt.Errorf("issue job token: %w", err)
	}

	// Defense in depth: Store.Issue refuses a starter that is not the
	// principal that started the job. This check stays so that a caller
	// cannot ask for a token under another principal or with no starter
	// subject.
	if req.StarterSubject == "" || req.StarterSubject != req.Job.StartedBy {
		return Secret{}, fmt.Errorf("%w: the starter is not the job starter", ErrInvalid)
	}

	// Defense in depth: Store.Issue refuses a claim of another operation
	// than the current operation of the job. This check stays so that a
	// caller cannot ask for a token of an operation that the job left.
	if req.Claim.Operation != currentOperation(&req.Job) {
		return Secret{}, fmt.Errorf("%w: the claim is not the current operation of the job", ErrInvalid)
	}

	entry := req.Entry
	entry.At = now

	if err := s.store.Issue(ctx, t, req.Fence, entry); err != nil {
		return Secret{}, fmt.Errorf("issue job token: %w", err)
	}

	return secret, nil
}

// currentOperation returns the current operation of j, or "" when j has no
// operation.
func currentOperation(j *job.Job) durable.ID {
	if j.Current < 0 || j.Current >= len(j.Phases) {
		return ""
	}

	return durable.ID(j.Phases[j.Current].Operation)
}

// Authenticate returns the starter principal of the job token secret, with
// the job ID set, and the unit of the job of the token. The caller must limit
// every action of the principal to that unit. It returns an error that wraps
// identity.ErrUnauthenticated for every refusal: a value that is not a
// secret, an unknown record, a record whose digest differs, a claim or job
// that is not live, or a starter that is deleted or disabled. On every
// refusal and every error it returns the zero principal and the zero object.
// The error holds no token text, digest, job ID, unit name or namespace name.
// A store error other than ErrNotFound comes back wrapped, so the caller
// never gets a principal with an error.
func (s *Service) Authenticate(
	ctx context.Context, secret string,
) (identity.Principal, identity.Object, error) {
	if !LooksLikeSecret(secret) {
		return identity.Principal{}, identity.Object{}, fmt.Errorf(
			"%w: job token format", identity.ErrUnauthenticated,
		)
	}

	d := digestOfRaw(secret)

	m, err := s.store.GetByDigest(ctx, d)
	if errors.Is(err, ErrNotFound) {
		return identity.Principal{}, identity.Object{}, fmt.Errorf(
			"%w: job token not found", identity.ErrUnauthenticated,
		)
	}

	if err != nil {
		return identity.Principal{}, identity.Object{}, fmt.Errorf("authenticate job token: %w", err)
	}

	// Defense in depth: the store keeps only the digest and answers a lookup
	// by it. This check stays so that an adapter that returns another record
	// cannot authenticate a secret against the wrong row.
	if !m.Token.Digest.Equal(d) {
		return identity.Principal{}, identity.Object{}, fmt.Errorf(
			"%w: job token digest differs", identity.ErrUnauthenticated,
		)
	}

	// Defense in depth: Store.Issue validates the token record before it
	// writes it. This check stays so that a restored or damaged row with
	// empty fields, or with a far expiry, cannot authenticate. Service.Issue
	// refuses such an expiry at the issue.
	if err := m.Token.Validate(); err != nil {
		return identity.Principal{}, identity.Object{}, fmt.Errorf(
			"%w: the job token record is not valid: %w", identity.ErrUnauthenticated, err,
		)
	}

	if err := live(&m, s.now()); err != nil {
		return identity.Principal{}, identity.Object{}, err
	}

	// Defense in depth: Store.GetByDigest reads the starter principal by the
	// ID of the row and refuses a deleted principal. This check stays so that
	// an adapter that returns a starter record with no subject cannot
	// authenticate a principal with no identity.
	if m.Claim.Starter.Subject == "" {
		return identity.Principal{}, identity.Object{}, fmt.Errorf(
			"%w: the starter has no subject", identity.ErrUnauthenticated,
		)
	}

	p := m.Claim.Starter
	p.JobID = string(m.Token.JobID)
	p.Credential = identity.CredentialJobToken
	p.TokenExpiresAt = m.Token.ExpiresAt

	// A job token never carries the token ID of another credential, an
	// administrator flag or a temporary elevation.
	p.TokenID = ""
	p.Admin = false
	p.TemporaryAdministrator = false
	p.TemporaryExpiresAt = time.Time{}

	return p, m.Token.Unit, nil
}

// live returns an error that wraps identity.ErrUnauthenticated when the claim
// or the job of m is not live at the time now.
func live(m *Match, now time.Time) error {
	// Check 1: the durable operation of the claim runs. Defense in depth:
	// Store.Issue checks it at the issue; this check stays so that an
	// operation that stops later cannot authenticate.
	if m.Claim.OperationState != durable.StateRunning {
		return fmt.Errorf("%w: the operation does not run", identity.ErrUnauthenticated)
	}

	// Check 2: the claim token of the operation is the claim token of the
	// record. Another claim replaced the runner, so the token is a takeover.
	// Defense in depth: Store.Issue checks it at the issue; this check stays
	// so that a takeover after the issue cannot authenticate.
	if m.Claim.OperationClaimToken != m.Token.ClaimToken {
		return fmt.Errorf("%w: the claim was taken over", identity.ErrUnauthenticated)
	}

	// Check 3: the lease of the claim ends after now. A lease that ends at
	// now no longer holds the operation. Defense in depth: Store.Issue checks
	// it at the issue; this check stays so that a lease that ends later cannot
	// authenticate.
	if !now.Before(m.Claim.LeaseEndsAt) {
		return fmt.Errorf("%w: the claim lease ended", identity.ErrUnauthenticated)
	}

	// Check 4: the job is in a state that allows a live token. Defense in
	// depth: Store.Issue checks it at the issue; this check stays so that a
	// job that changes state later cannot authenticate.
	if !JobStateLive(m.Claim.JobState) {
		return fmt.Errorf("%w: the job is not live", identity.ErrUnauthenticated)
	}

	// Check 5: the current operation of the job is the claim operation of
	// the record. A job that moved on runs another operation. Defense in
	// depth: Store.Issue checks it at the issue; this check stays so that a
	// job that moves later cannot authenticate.
	if m.Claim.JobOperation != m.Token.ClaimOperation {
		return fmt.Errorf("%w: the job runs another operation", identity.ErrUnauthenticated)
	}

	// Check 6: the unit ID of the job is the unit ID of the record. The
	// stable ID, not the name, so a rename cannot redirect the token. Defense
	// in depth: Store.Issue checks it at the issue; this check stays so that
	// a record of another unit cannot authenticate.
	if m.Claim.JobUnitID != m.Token.Unit.UnitID() {
		return fmt.Errorf("%w: the unit differs from the job unit", identity.ErrUnauthenticated)
	}

	// Check 7: the hard expiry of the record is after now. Defense in
	// depth: Store.Issue checks it at the issue; this check stays so that
	// the time of this request decides.
	if !now.Before(m.Token.ExpiresAt) {
		return fmt.Errorf("%w: the job token expired", identity.ErrUnauthenticated)
	}

	// Check 8: the starter principal is enabled. Defense in depth: the store
	// returns ErrNotFound for a deleted starter and reads the enabled flag in
	// the same lookup. The flag is true only when the store read the starter
	// and the starter is not disabled, so a false value refuses. This check
	// stays so that a record of another version, a new caller that skips the
	// store check, or an adapter that does not read the flag cannot
	// authenticate.
	if !m.Claim.StarterEnabled {
		return fmt.Errorf("%w: the starter is not enabled", identity.ErrUnauthenticated)
	}

	// Check 9: the starter of the record is the principal that started the
	// job. Defense in depth: Store.Issue checks it too. This check stays so
	// that a restored row or a record of another version cannot authenticate
	// with the identity of another principal.
	if m.Token.StarterID != m.Claim.JobStarterID {
		return fmt.Errorf("%w: the starter is not the job starter", identity.ErrUnauthenticated)
	}

	// Check 10: the starter record that the store read is the record of the
	// starter of the token. Defense in depth: the store reads the starter by
	// the ID of the row; this check stays so that an adapter that joins
	// another principal record cannot authenticate as that principal.
	if m.Claim.StarterRecordID != m.Token.StarterID {
		return fmt.Errorf("%w: the starter record differs", identity.ErrUnauthenticated)
	}

	// Check 11: the job that the store read is the job of the record. Defense
	// in depth: the store reads the job by the job ID of the row; this check
	// stays so that an adapter that joins another job cannot authenticate.
	if m.Claim.JobID != m.Token.JobID {
		return fmt.Errorf("%w: the job record differs", identity.ErrUnauthenticated)
	}

	return nil
}
