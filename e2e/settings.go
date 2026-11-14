//go:build e2e

package e2e

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// settingsCoveringTest names the test that proves the routes of the settings
// resource instead of a CLI: no CLI reads or changes the settings of the
// installation or of a namespace.
const settingsCoveringTest = "TestSettings"

// settingsAllowEntries returns the routes of the settings resource of the
// installation and of a namespace. The steps of settingsCoveringTest drive
// them through the server of each store with the token of the administrator.
func settingsAllowEntries() []AllowEntry {
	reason := "Console route: no CLI reads or changes the settings resource. " +
		"The harness steps call it with the administrator token and check the answer."

	paths := []string{
		"/api/dustpan/v1/settings",
		"/api/dustpan/v1/namespaces/{namespace}/settings",
	}

	methods := []string{http.MethodGet, http.MethodPatch}
	entries := make([]AllowEntry, 0, len(paths)*len(methods))

	for _, path := range paths {
		for _, method := range methods {
			entries = append(entries, AllowEntry{
				Method: method, Path: path, CoveringTest: settingsCoveringTest, Reason: reason,
			})
		}
	}

	return entries
}

// AdminToken returns the token of the bootstrap administrator of the
// environment, for a step that sends a request with its own headers.
func (e *Env) AdminToken() string { return e.token }

// settingStoreOptions returns the option that gives the in-process server the
// settings resource of st, with the declarations of the binary. The in-process
// server has no flags, so its static layer sets nothing.
func settingStoreOptions(st serverStore) []dustpan.Option {
	switch s := st.(type) {
	case *sqlite.Store:
		return []dustpan.Option{dustpan.WithSettings(s.SettingStore(), cli.SettingDeclarations(), nil)}
	case *postgres.Store:
		declarations := append(cli.SettingDeclarations(), postgres.SettingDeclarations()...)

		return []dustpan.Option{dustpan.WithSettings(s.SettingStore(), declarations, nil)}
	default:
		return nil
	}
}
