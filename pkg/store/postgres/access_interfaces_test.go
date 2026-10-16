package postgres_test

import (
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// The access suite runs an optional case only when the store implements the
// interface of that case, and skips it otherwise. These assignments fail the
// build when a method is missing, so a skip cannot hide the drift.
var (
	_ access.Store             = (*postgres.Store)(nil)
	_ access.Callers           = (*postgres.Store)(nil)
	_ access.Units             = (*postgres.Store)(nil)
	_ access.ReviewStore       = (*postgres.Store)(nil)
	_ access.ServicePrincipals = (*postgres.Store)(nil)
	_ namespace.Store          = (*postgres.Store)(nil)
	_ principal.Store          = (*postgres.Store)(nil)
	_ audit.Store              = (*postgres.Store)(nil)
	_ collection.Store         = (*postgres.Store)(nil)
	_ accesstest.UnitStore     = (*postgres.Store)(nil)
)
