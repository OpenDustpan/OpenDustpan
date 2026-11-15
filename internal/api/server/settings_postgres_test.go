package server_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// newBoundsSettingsEnv builds a handler with the declarations of the bounds
// of the PostgreSQL store over a fresh store, and no console sign-in.
func newBoundsSettingsEnv(t *testing.T) settingsEnv {
	t.Helper()

	registry, err := setting.NewRegistry(postgres.SettingDeclarations())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, settingsConfig(
		t, st, namespaces, allowAll{}, setting.NewService(st.SettingStore(), registry, nil, st, nil),
	))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return settingsEnv{h: handler, st: st}
}

// TestSettingsShowThePostgresBounds proves that the settings resource answers
// each bound of the PostgreSQL store with the value of the code and the
// source "default", and that a change of one is refused as for each setting
// that only the static layer can set.
func TestSettingsShowThePostgresBounds(t *testing.T) {
	t.Parallel()

	e := newBoundsSettingsEnv(t)

	r := e.do(t, getCall(settingsURL))
	wantSettingsStatus(t, r, http.StatusOK)

	got := decodeSettings(t, r)

	tests := []struct {
		key   string
		value string
	}{
		{"database.postgres.statementTimeout", "25s"},
		{"database.postgres.idleInTransactionTimeout", "1m0s"},
		{"database.postgres.clientConnectionCheckInterval", "5s"},
		{"database.postgres.backup.statementTimeout", "1h0m0s"},
		{"database.postgres.backup.idleInTransactionTimeout", "15m0s"},
		{"database.postgres.schemaChange.statementTimeout", "10m0s"},
		{"database.postgres.schemaChange.idleInTransactionTimeout", "1m0s"},
		{"database.postgres.migration.timeout", "15m0s"},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			item, ok := got[tc.key]
			if !ok {
				t.Fatalf("the resource has no setting %s", tc.key)
			}

			if item["value"] != tc.value || item["default"] != tc.value || item["source"] != "default" ||
				!isTrue(item["locked"]) || isTrue(item["databaseLayer"]) {
				t.Fatalf("%s = %v, want the default %s with the source default and no database layer",
					tc.key, item, tc.value)
			}

			refused := e.patch(t, settingsURL, `"0"`, `{"`+tc.key+`":"1s"}`)
			wantSettingsStatus(t, refused, http.StatusConflict)

			if !strings.Contains(refused.body, `"errorCode":"settings.static-only"`) {
				t.Fatalf("PATCH %s = %s, want settings.static-only", tc.key, refused.body)
			}
		})
	}
}
