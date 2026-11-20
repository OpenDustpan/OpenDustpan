package postgres_test

import (
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/job"
)

// The job store of the PostgreSQL adapter must give the final version reader.
// The assertion fails to compile when the JobStore interface stops embedding it.
var _ job.FinalVersionReader = postgres.JobStore(nil)
