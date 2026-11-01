package settings_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
)

// probeOf returns the scenario that sends one request to a fresh rig that
// holds a record at version 1, and whose current ETag is etag. An empty etag is
// a route with no version.
func probeOf(method, target, body, etag string) headertest.Scenario {
	return func(t *testing.T) headertest.Probe {
		t.Helper()

		r := newRig(t, fullAccess()...)

		// The record and the defaults exist at version 1, so a write with the
		// current ETag succeeds.
		if rec := r.call(t, http.MethodPut, settingsPath, validBody, ifMatch(`"0"`)); rec.Code != http.StatusOK {
			t.Fatalf("seed the record = %d: %s", rec.Code, rec.Body.String())
		}

		rec := r.call(t, http.MethodPut, defaultsPath, `{"pool":"`+poolJobs+`"}`, ifMatch(`"0"`))
		if rec.Code != http.StatusOK {
			t.Fatalf("seed the defaults = %d: %s", rec.Code, rec.Body.String())
		}

		return headertest.Probe{
			ETag: etag, Replayed: nil,
			Send: func(headers map[string]string) headertest.Answer {
				req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
				req.Header.Set("Authorization", "token "+testToken)

				for k, v := range headers {
					req.Header.Set(k, v)
				}

				rec := httptest.NewRecorder()
				r.h.ServeHTTP(rec, req)

				return headertest.Answer{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
			},
		}
	}
}

// TestHeaderFlagsMatchTheHandler proves that the header flags of each operation
// of the description match what the handler does with the header. Each write
// refuses an Idempotency-Key. An operation with no scenario here fails, so a
// new route cannot enter without a check.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	changed := `{"pool":"` + poolJobs + `","codeLocation":{"repository":"acme/other"}}`

	scenarios := map[string]headertest.Scenario{
		"getUnitSettings":            probeOf(http.MethodGet, settingsPath, "", `"1"`),
		"replaceUnitSettings":        probeOf(http.MethodPut, settingsPath, changed, `"1"`),
		"resetUnitSettings":          probeOf(http.MethodDelete, settingsPath, "", `"1"`),
		"getUnitSettingsSuggestions": probeOf(http.MethodGet, suggestionsPath, "", ""),
		"getUnitDefaults":            probeOf(http.MethodGet, defaultsPath, "", `"1"`),
		"replaceUnitDefaults":        probeOf(http.MethodPut, defaultsPath, `{"pool":""}`, `"1"`),
		"bulkReplaceUnitSettings": probeOf(http.MethodPost, bulkPath,
			`{"units":[{"id":"`+netID+`","version":1}],"settings":`+changed+`}`, ""),
	}

	ops := newRig(t, fullAccess()...).h.Operations()
	if len(ops) != len(scenarios) {
		t.Fatalf("the handler describes %d operations, the test has %d scenarios", len(ops), len(scenarios))
	}

	for _, op := range ops {
		sc, ok := scenarios[op.ID]
		if !ok {
			t.Fatalf("operation %s has no header scenario", op.ID)
		}

		t.Run(op.ID, func(t *testing.T) {
			t.Parallel()

			headertest.Check(t, op, sc, headertest.Options{RefusesKey: true})
		})
	}
}
