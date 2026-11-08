package postgres_test

import "opendustpan.org/opendustpan/pkg/store/storetest"

// BreakGlassPorts implements storetest.BreakGlassSeeder.
func (s *backupStore) BreakGlassPorts() storetest.BreakGlassPorts {
	return storetest.BreakGlassPorts{Store: s.BreakGlass()}
}
