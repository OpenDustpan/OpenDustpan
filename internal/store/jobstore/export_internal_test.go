package jobstore

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// EncodeJobBody exposes encodeBody to the black-box tests.
func EncodeJobBody(j *job.Job) (string, error) { return encodeBody(j) }

// DecodeJobBody exposes decodeBody to the black-box tests.
func DecodeJobBody(body string, j *job.Job) error { return decodeBody(body, j) }

// RowToJob exposes jobOf to the black-box tests.
func RowToJob(r *Row) (job.Job, error) { return jobOf(r) }

// CheckReservedPlan exposes checkReserved to the black-box tests.
func CheckReservedPlan(
	ctx context.Context, tx Tx, j *job.Job, pc *job.PhaseClaim, p *job.PlanRef,
) (*job.PlanRef, error) {
	return checkReserved(ctx, tx, j, pc, p)
}

// CheckDestroyProofStepOn exposes checkDestroyProof with the setting
// freshAuthEnabled on to the black-box tests. approver is the principal of the
// approval subject, and found tells whether that principal row exists.
func CheckDestroyProofStepOn(j *job.Job, ap *job.Approval, now time.Time, approver identity.Kind, found bool) error {
	rules := writeRules{now: now, freshRequired: true, approverFound: found, approverKind: approver}

	return checkDestroyProof(j, ap, rules)
}

// CheckDestroyProofStepOff exposes checkDestroyProof with the setting
// freshAuthEnabled off to the black-box tests. approver is the principal of the
// approval subject, and found tells whether that principal row exists.
func CheckDestroyProofStepOff(j *job.Job, ap *job.Approval, now time.Time, approver identity.Kind, found bool) error {
	rules := writeRules{now: now, freshRequired: false, approverFound: found, approverKind: approver}

	return checkDestroyProof(j, ap, rules)
}

// ReadFreshRequired exposes freshRequired of s, read in the transaction tx, to
// the black-box tests.
func ReadFreshRequired(ctx context.Context, s *Store, tx Tx) (bool, error) {
	return freshRequired(ctx, s.fresh, tx)
}

// NextJobUpdateAt exposes nextUpdateAt to the black-box tests.
func NextJobUpdateAt(now, stored time.Time) time.Time { return nextUpdateAt(now, stored) }

// BuildKeyExpiredEntries exposes keyExpiredEntries to the black-box tests.
func BuildKeyExpiredEntries(e []audit.Entry, old *job.Job, now time.Time) ([]audit.Entry, error) {
	return keyExpiredEntries(e, old, now)
}
