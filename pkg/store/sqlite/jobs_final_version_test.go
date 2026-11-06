package sqlite_test

import (
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// The job store of the SQLite adapter must give the final version reader. The
// assertion fails to compile when the JobStore interface stops embedding it.
var _ job.FinalVersionReader = sqlite.JobStore(nil)
