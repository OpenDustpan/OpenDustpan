package postgres_test

import (
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// The job store of the PostgreSQL adapter must give the final version reader.
// The assertion fails to compile when the JobStore interface stops embedding it.
var _ job.FinalVersionReader = postgres.JobStore(nil)
