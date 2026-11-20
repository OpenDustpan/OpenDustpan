package server_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

const restoreAccessBody = `{"restore.replace-access-tables":true}`

func restoreAccessEnv(t *testing.T, deny ...identity.Action) keyedSettingsEnv {
	t.Helper()

	reg, err := setting.NewRegistry(append(settingtest.Declarations(), backup.RestoreAccessSettingDeclarations()...))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	denied := map[identity.Action]bool{}
	for _, action := range deny {
		denied[action] = true
	}

	return newKeyedSettingsEnvAs(t, reg, selectiveAuthorizer{deny: denied})
}

// TestSettingsRestoreAccessWriterRule is the table of the settings layer: the
// default reads off, and a change needs server:manage-settings, then
// server:restore-access, then a fresh session step. Each refusal keeps the
// value and writes no change entry.
func TestSettingsRestoreAccessWriterRule(t *testing.T) {
	t.Parallel()

	e := restoreAccessEnv(t, identity.ActionServerRestoreAccess)

	read := e.read(t)
	if !strings.Contains(read.body, `"key":"restore.replace-access-tables"`) ||
		!strings.Contains(read.body, `"default":false`) || !strings.Contains(read.body, `"value":false`) {
		t.Fatalf("read = %s, want the key with the value and the default false", read.body)
	}

	// A holder of server:manage-settings without server:restore-access, even
	// with a fresh session.
	r := e.patch(t, sessionPatch(`"0"`, restoreAccessBody, 0))
	wantSettingsStatus(t, r, http.StatusForbidden)

	if e.changeEntries(t) != 0 {
		t.Fatal("a refused change wrote a change entry")
	}

	if got := e.read(t); got.header.Get("ETag") != `"0"` {
		t.Fatalf("a refused change moved the settings: ETag %q", got.header.Get("ETag"))
	}
}

// TestSettingsRestoreAccessFreshStepAndChange proves the allowed change: an
// access token is refused with the fresh-step code, a fresh session saves
// the value, and the change entry holds the old and the new value.
func TestSettingsRestoreAccessFreshStepAndChange(t *testing.T) {
	t.Parallel()

	e := restoreAccessEnv(t)

	r := e.patch(t, tokenPatch(settingsURL, `"0"`, "key-restore-token", restoreAccessBody))
	wantSettingsStatus(t, r, http.StatusForbidden)

	if !strings.Contains(r.body, settingsFreshCode) {
		t.Fatalf("body = %s, want the code mfa.fresh-step-required", r.body)
	}

	wantSettingsStatus(t, e.patch(t, sessionPatch(`"0"`, `{"restore.replace-access-tables":"maybe"}`, 0)),
		http.StatusBadRequest)

	wantSettingsStatus(t, e.patch(t, sessionPatch(`"0"`, restoreAccessBody, 0)), http.StatusOK)

	saved := 0

	for _, entry := range serverEntries(t, e.st) {
		if entry.Action == string(identity.ActionServerManageSettings) && entry.Outcome == audit.OutcomeSuccess {
			saved++

			c := entry.Changes[0]
			if c.Key != backup.SettingRestoreAccessTables || c.Old == nil || *c.Old != "false" || c.New != "true" {
				t.Fatalf("entry change = %+v, want the old value false and the new value true", c)
			}
		}
	}

	if saved != 1 {
		t.Fatalf("saved change entries = %d, want 1", saved)
	}

	if !strings.Contains(e.read(t).body, `"value":true`) {
		t.Fatal("the value did not save")
	}

	// Turning the setting off is stronger and needs no fresh step.
	off := e.patch(t, tokenPatch(settingsURL, `"1"`, "key-restore-off", `{"restore.replace-access-tables":false}`))
	wantSettingsStatus(t, off, http.StatusOK)
}

// TestSettingsRestoreAccessOtherKeyNeedsNoRestoreAccess proves that the extra
// action guards only this key.
func TestSettingsRestoreAccessOtherKeyNeedsNoRestoreAccess(t *testing.T) {
	t.Parallel()

	e := restoreAccessEnv(t, identity.ActionServerRestoreAccess)

	r := e.patch(t, tokenPatch(settingsURL, `"0"`, "key-other", `{"test.label":"blue"}`))
	wantSettingsStatus(t, r, http.StatusOK)
}
