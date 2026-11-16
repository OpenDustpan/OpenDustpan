package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestSettingsPatchKeyReuseHostile sends an Idempotency-Key that an earlier
// change used, with another body or another If-Match, straight to the handler.
// Each request is refused and changes nothing.
func TestSettingsPatchKeyReuseHostile(t *testing.T) {
	t.Parallel()

	e := newKeyedSettingsEnv(t)

	first := tokenPatch(settingsURL, `"0"`, "key-one", `{"test.label":"blue"}`)
	wantSettingsStatus(t, e.patch(t, first), http.StatusOK)

	hostile := map[string]keyedPatch{
		"another body":       tokenPatch(settingsURL, `"0"`, "key-one", `{"test.label":"red"}`),
		"another If-Match":   tokenPatch(settingsURL, `"1"`, "key-one", `{"test.label":"blue"}`),
		"another value kind": tokenPatch(settingsURL, `"0"`, "key-one", `{"test.limit":42}`),
	}

	for name, p := range hostile {
		r := e.patch(t, p)
		wantSettingsStatus(t, r, http.StatusUnprocessableEntity)

		if !strings.Contains(r.body, `"errorCode":"request.idempotency-key-reused"`) {
			t.Fatalf("%s: body = %s, want the code request.idempotency-key-reused", name, r.body)
		}
	}

	got := e.read(t)
	if got.header.Get("ETag") != `"1"` || decodeSettings(t, got)["test.label"]["value"] != "blue" {
		t.Fatalf("a refused request changed the settings: ETag %q, body %s", got.header.Get("ETag"), got.body)
	}

	if n := e.changeEntries(t); n != 1 {
		t.Fatalf("change entries = %d, want 1", n)
	}
}
