//go:build e2e

package e2e

import (
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// jobStoreOptions returns the options that give the in-process server the job
// store of st and the runner stores that a job needs for its pool.
func jobStoreOptions(st serverStore) []dustpan.Option {
	var opts []dustpan.Option

	if jobs, ok := jobsOf(st); ok {
		opts = append(opts, dustpan.WithJobStore(jobs))
	}

	switch s := any(st).(type) {
	case *sqlite.Store:
		opts = append(opts, dustpan.WithRunners(s.Runners()), dustpan.WithTriggerStore(s.Triggers()))
	case *postgres.Store:
		opts = append(opts, dustpan.WithRunners(s.Runners()), dustpan.WithTriggerStore(s.Triggers()))
	default:
	}

	return opts
}

// jobsOf returns the job store of st, and false for a store that keeps none.
//
//nolint:ireturn // The harness takes the port of whichever store the run uses.
func jobsOf(st any) (job.Store, bool) {
	switch s := st.(type) {
	case *sqlite.Store:
		return s.Jobs(), true
	case *postgres.Store:
		return s.Jobs(), true
	default:
		return nil, false
	}
}
