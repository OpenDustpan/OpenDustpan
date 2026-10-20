package jobstore

import "opendustpan.org/opendustpan/pkg/job"

// EncodeJobBody exposes encodeBody to the black-box tests.
func EncodeJobBody(j *job.Job) (string, error) { return encodeBody(j) }

// DecodeJobBody exposes decodeBody to the black-box tests.
func DecodeJobBody(body string, j *job.Job) error { return decodeBody(body, j) }

// RowToJob exposes jobOf to the black-box tests.
func RowToJob(r *Row) (job.Job, error) { return jobOf(r) }
