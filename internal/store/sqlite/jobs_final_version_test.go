package sqlite_test

import (
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/job"
)

// The job store of the SQLite adapter must give the final version reader. The
// assertion fails to compile when the JobStore interface stops embedding it.
var _ job.FinalVersionReader = sqlite.JobStore(nil)
