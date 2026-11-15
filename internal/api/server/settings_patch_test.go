package server_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

// TestSettingsPatchAtomic checks that a patch with one valid member and one
// refused member changes nothing: no value, no revision and no success entry.
// The failure entry names no value of the request.
func TestSettingsPatchAtomic(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, settingtest.StaticMap{settingtest.KeyLimit: settingtest.File("3")})

	refused := map[string]string{
		"a locked key":      `{"test.label":"secret-value-1","test.limit":5}`,
		"an undeclared key": `{"test.label":"secret-value-2","nope":1}`,
		"a wrong kind":      `{"test.label":"secret-value-3","test.enabled":"yes"}`,
	}

	for name, body := range refused {
		r := e.patch(t, settingsURL, `"0"`, body)
		if r.status < http.StatusBadRequest || r.status >= http.StatusInternalServerError {
			t.Fatalf("%s: status = %d %s, want a refusal", name, r.status, r.body)
		}
	}

	got := e.do(t, getCall(settingsURL))
	if got.header.Get("ETag") != `"0"` {
		t.Fatalf("ETag = %q, want \"0\": a refused patch moved the revision", got.header.Get("ETag"))
	}

	if label := decodeSettings(t, got)[settingtest.KeyLabel]; label["source"] != "default" {
		t.Fatalf("label = %v, want the default: the valid member of a refused patch was applied", label)
	}

	for _, entry := range serverEntries(t, e.st) {
		if entry.Outcome == audit.OutcomeSuccess {
			t.Fatalf("entry = %+v, want only failure entries", entry)
		}

		if strings.Contains(entry.Object+entry.Action+entry.Reason, "secret-value") {
			t.Fatalf("entry = %+v, want no value of the request", entry)
		}
	}

	// The same patch without the refused member works.
	wantSettingsStatus(t, e.patch(t, settingsURL, `"0"`, `{"test.label":"blue"}`), http.StatusOK)
}
