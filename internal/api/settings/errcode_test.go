package settings_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// errDriver is an error that no case of the adapter knows. Its text must never
// reach the answer.
var errDriver = errors.New("driver: connection reset by peer")

// TestEveryRefusalAnswersACatalogCode sends one request for each refusal of the
// adapter and checks that the answer holds a code of the catalog with the
// arguments that the code declares.
func TestEveryRefusalAnswersACatalogCode(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	tooLarge := `{"pool":"` + strings.Repeat("a", unitsettings.MaxBodyBytes) + `"}`

	cases := []struct {
		name                        string
		method, path, body, version string
		status                      int
		want                        errcodetest.Want
	}{
		{
			"unit not found", http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/nope/settings", "", "",
			http.StatusNotFound,
			errcodetest.Want{Code: errcode.UnitSettingsNotFound, Args: nil, RequestBody: nil},
		},
		{
			"no version", http.MethodPut, settingsPath, "{}", "", http.StatusPreconditionRequired,
			errcodetest.Want{Code: errcode.RequestPreconditionRequired, Args: nil, RequestBody: nil},
		},
		{
			"bad version", http.MethodPut, settingsPath, "{}", "x", http.StatusBadRequest,
			errcodetest.Want{Code: errcode.RequestIfMatchInvalid, Args: nil, RequestBody: nil},
		},
		{
			"stale version", http.MethodPut, settingsPath, "{}", `"5"`, http.StatusPreconditionFailed,
			errcodetest.Want{Code: errcode.RequestPreconditionFailed, Args: nil, RequestBody: nil},
		},
		{
			"body not valid", http.MethodPut, settingsPath, "{", `"0"`, http.StatusBadRequest,
			errcodetest.Want{Code: errcode.RequestBodyInvalid, Args: nil, RequestBody: nil},
		},
		{
			"body too large", http.MethodPut, settingsPath, tooLarge, `"0"`, http.StatusRequestEntityTooLarge,
			errcodetest.Want{
				Code: errcode.RequestBodyTooLarge, Args: map[string]any{"maxBytes": int64(unitsettings.MaxBodyBytes)},
				RequestBody: nil,
			},
		},
		{
			"settings not valid", http.MethodPut, settingsPath, `{"codeLocation":{"ref":"-x"}}`, `"0"`,
			http.StatusBadRequest,
			errcodetest.Want{Code: errcode.UnitSettingsInvalid, Args: nil, RequestBody: nil},
		},
		{
			"pool unknown", http.MethodPut, settingsPath, `{"pool":"rp-nope"}`, `"0"`, http.StatusUnprocessableEntity,
			errcodetest.Want{Code: errcode.UnitSettingsPoolUnknown, Args: nil, RequestBody: nil},
		},
		{
			"no record to reset", http.MethodDelete, settingsPath, "", `"1"`, http.StatusNotFound,
			errcodetest.Want{Code: errcode.UnitSettingsNotFound, Args: nil, RequestBody: nil},
		},
		{
			"bulk size", http.MethodPost, bulkPath, `{"units":[],"settings":{}}`, "", http.StatusBadRequest,
			errcodetest.Want{Code: errcode.UnitSettingsBulkSize, Args: nil, RequestBody: nil},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var header map[string]string
			if tc.version != "" {
				header = ifMatch(tc.version)
			}

			rec := r.call(t, tc.method, tc.path, tc.body, header)
			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rec.Code, rec.Body.Bytes(), tc.want)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}

// TestUnknownErrorAnswers500WithoutDetail proves that an error of the store
// that no class knows answers server.internal and never repeats its text.
func TestUnknownErrorAnswers500WithoutDetail(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	r.store.failWith = errDriver

	rec := r.call(t, http.MethodPut, settingsPath, validBody, ifMatch(`"0"`))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "driver") {
		t.Fatalf("answer = %d %s, want a 500 without the text of the error", rec.Code, rec.Body.String())
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rec.Code, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.ServerInternal, Args: nil, RequestBody: nil})
}
