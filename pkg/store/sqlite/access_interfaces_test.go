package sqlite_test

import (
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// The access suite runs an optional case only when the store implements the
// interface of that case, and skips it otherwise. These assignments fail the
// build when a method is missing, so a skip cannot hide the drift.
var (
	_ access.Store         = (*sqlite.Store)(nil)
	_ access.Callers       = (*sqlite.Store)(nil)
	_ access.Units         = (*sqlite.Store)(nil)
	_ access.ReviewStore   = (*sqlite.Store)(nil)
	_ namespace.Store      = (*sqlite.Store)(nil)
	_ principal.Store      = (*sqlite.Store)(nil)
	_ audit.Store          = (*sqlite.Store)(nil)
	_ collection.Store     = (*sqlite.Store)(nil)
	_ accesstest.UnitStore = (*sqlite.Store)(nil)
)
