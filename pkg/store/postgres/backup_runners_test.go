package postgres_test

import (
	"context"

	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// RunnerPorts implements storetest.RunnerSeeder.
func (s *backupStore) RunnerPorts() storetest.RunnerPorts {
	r := s.Runners()

	return storetest.RunnerPorts{Pools: r, Members: r, Sessions: r}
}

// SeedRunnerControlRows implements storetest.RunnerSeeder.
func (s *backupStore) SeedRunnerControlRows(ctx context.Context, runnerID string) error {
	return postgres.SeedRunnerControlRows(ctx, s.Store, runnerID)
}

// CountRunnerControlRows implements storetest.RunnerSeeder.
func (s *backupStore) CountRunnerControlRows(ctx context.Context) (map[string]int64, error) {
	return postgres.CountRunnerControlRows(ctx, s.Store)
}
