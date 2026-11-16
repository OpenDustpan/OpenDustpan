package server_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestPatchRefusesUnknownKeyWith400(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, nil)

	tests := []struct {
		name, body, code string
	}{
		{"an undeclared key", `{"nope":1}`, "settings.key-unknown"},
		{"a value of the wrong kind", `{"test.enabled":"yes"}`, "settings.value-invalid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := e.patch(t, settingsURL, `"0"`, tc.body)
			wantSettingsStatus(t, r, http.StatusBadRequest)

			if !strings.Contains(r.body, `"errorCode":"`+tc.code+`"`) {
				t.Fatalf("body = %s, want the code %s", r.body, tc.code)
			}
		})
	}
}
