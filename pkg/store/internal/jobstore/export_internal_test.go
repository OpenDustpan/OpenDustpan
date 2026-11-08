package jobstore

import (
	"context"
	"time"

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

// NextJobUpdateAt exposes nextUpdateAt to the black-box tests.
func NextJobUpdateAt(now, stored time.Time) time.Time { return nextUpdateAt(now, stored) }
