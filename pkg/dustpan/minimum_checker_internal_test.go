package dustpan

import (
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// The two built-in stores meet minimumChecker. So the start check of a
// restore and the readiness check never skip a built-in store because its
// type lacks the method. A change that removes CheckBinaryMinimum from a
// store breaks the build of this test.
var (
	_ minimumChecker = (*sqlite.Store)(nil)
	_ minimumChecker = (*postgres.Store)(nil)
)
