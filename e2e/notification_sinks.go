//go:build e2e

package e2e

import (
	"slices"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// sinkStoreOptions returns the option that gives the in-process server the
// notification sink store of st.
func sinkStoreOptions(st serverStore) []dustpan.Option {
	switch s := st.(type) {
	case *sqlite.Store:
		return []dustpan.Option{dustpan.WithSinkStore(s.NotificationSinks())}
	case *postgres.Store:
		return []dustpan.Option{dustpan.WithSinkStore(s.NotificationSinks())}
	default:
		return nil
	}
}

// storeOptions returns the options that give the in-process server the feature
// stores of st: profiles, jobs, saved views and notification sinks.
func storeOptions(st serverStore, wrapper secret.Wrapper) []dustpan.Option {
	return slices.Concat(
		profileStoreOptions(st, wrapper), jobStoreOptions(st), savedViewStoreOptions(st), sinkStoreOptions(st),
	)
}
