//go:build e2e

package e2e

import (
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// trustRuleStoreOptions returns the option that gives the in-process server
// the trust rule store of st, as the binary gives it.
func trustRuleStoreOptions(st serverStore) []dustpan.Option {
	switch s := st.(type) {
	case *sqlite.Store:
		return []dustpan.Option{dustpan.WithTrustRules(s.TrustRules())}
	case *postgres.Store:
		return []dustpan.Option{dustpan.WithTrustRules(s.TrustRules())}
	default:
		return nil
	}
}
