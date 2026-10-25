package postgres_test

import (
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// RunnerPorts implements storetest.RunnerSeeder.
func (s *backupStore) RunnerPorts() storetest.RunnerPorts {
	r := s.Runners()

	return storetest.RunnerPorts{Pools: r, Members: r}
}
