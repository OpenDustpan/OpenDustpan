package postgres_test

import (
	"context"

	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// Defense in depth: the compile-time check below stops a change that removes a
// runner port from the store, so the suite cannot skip the runner round trip.
var _ storetest.RunnerSeeder = (*backupStore)(nil)

// RunnerPorts implements storetest.RunnerSeeder.
func (s *backupStore) RunnerPorts() storetest.RunnerPorts {
	r := s.Runners()

	return storetest.RunnerPorts{Pools: r, Members: r}
}

// SeedRunnerControlRows implements storetest.RunnerSeeder.
func (s *backupStore) SeedRunnerControlRows(ctx context.Context) error {
	return postgres.SeedRunnerControlRows(ctx, s.Store)
}

// CountRunnerControlRows implements storetest.RunnerSeeder.
func (s *backupStore) CountRunnerControlRows(ctx context.Context) (map[string]int64, error) {
	return postgres.CountRunnerControlRows(ctx, s.Store)
}
