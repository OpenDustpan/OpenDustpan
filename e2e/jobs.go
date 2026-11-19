//go:build e2e

package e2e

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// builtinStoreOption returns the option that gives the in-process server every
// store option of the built-in store of st, as the binary gives them. A port
// that the binary does not wire fails the suites. A test adds only what it
// must replace.
func builtinStoreOption(tb testing.TB, st serverStore) dustpan.Option {
	tb.Helper()

	switch s := st.(type) {
	case *sqlite.Store:
		return dustpan.WithOpenSQLiteStore(s)
	case *postgres.Store:
		return dustpan.WithOpenPostgresStore(s)
	default:
		tb.Fatalf("no built-in store option for %T", st)

		return nil
	}
}
