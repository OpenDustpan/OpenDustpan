package server_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

// TestSettingsGetEffectiveSourceLockedRevision checks that a read of each scope
// answers the effective value, the source, the locked flag and the revision,
// and that a namespace that the caller cannot read or that does not exist is
// refused.
func TestSettingsGetEffectiveSourceLockedRevision(t *testing.T) {
	t.Parallel()

	static := settingtest.StaticMap{settingtest.KeyLimit: settingtest.File("3")}
	e := newSettingsEnv(t, static)

	wantSettingsStatus(t, e.patch(t, settingsURL, `"0"`, `{"test.label":"blue"}`), http.StatusOK)
	wantSettingsStatus(t, e.patch(t, settingsNSURL, `"0"`, `{"test.nsLimit":9}`), http.StatusOK)

	server := e.do(t, getCall(settingsURL))
	wantSettingsStatus(t, server, http.StatusOK)

	if server.header.Get("ETag") != `"1"` {
		t.Fatalf("installation ETag = %q, want \"1\"", server.header.Get("ETag"))
	}

	got := decodeSettings(t, server)

	label := got[settingtest.KeyLabel]
	if label["value"] != "blue" || label["source"] != "database" || !isFalse(label["locked"]) {
		t.Fatalf("label = %v, want blue from the database, not locked", label)
	}

	limit := got[settingtest.KeyLimit]
	if limit["value"] != float64(3) || limit["source"] != "file" || !isTrue(limit["locked"]) {
		t.Fatalf("limit = %v, want 3 from the file, locked", limit)
	}

	namespaced := e.do(t, getCall(settingsNSURL))
	wantSettingsStatus(t, namespaced, http.StatusOK)

	if namespaced.header.Get("ETag") != `"1"` {
		t.Fatalf("namespace ETag = %q, want \"1\"", namespaced.header.Get("ETag"))
	}

	nsLimit := decodeSettings(t, namespaced)[settingtest.KeyNSLimit]
	if nsLimit["value"] != float64(9) || nsLimit["source"] != "database" || nsLimit["scope"] != "namespace" {
		t.Fatalf("nsLimit = %v, want 9 from the database in the namespace scope", nsLimit)
	}

	// The refusals of a namespace read.
	wantSettingsStatus(t, e.do(t, getCall("/api/dustpan/v1/namespaces/nowhere/settings")), http.StatusNotFound)

	denied := newSettingsEnv(t, nil, identity.ActionNamespaceReadSettings)
	wantSettingsStatus(t, denied.do(t, getCall(settingsNSURL)), http.StatusForbidden)
	wantSettingsStatus(t, denied.do(t, getCall(settingsURL)), http.StatusOK)
}
