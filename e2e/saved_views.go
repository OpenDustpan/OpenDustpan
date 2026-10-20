//go:build e2e

package e2e

import (
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// savedViewStoreOptions returns the option that gives the in-process server the
// saved view store of st.
func savedViewStoreOptions(st serverStore) []dustpan.Option {
	switch s := st.(type) {
	case *sqlite.Store:
		return []dustpan.Option{dustpan.WithSavedViewStore(s.SavedViews())}
	case *postgres.Store:
		return []dustpan.Option{dustpan.WithSavedViewStore(s.SavedViews())}
	default:
		return nil
	}
}
