package triggers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
)

// TestHeaderFlagsMatchTheHandler proves that the header flags of each operation
// of the description match what the handler does with the header. No route of
// this API has a version or takes a key, and each write refuses a key. An
// operation with no scenario here fails, so a new route cannot enter without a
// check.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	probe := func(method, target, body string) headertest.Scenario {
		return func(t *testing.T) headertest.Probe {
			t.Helper()

			r := fullRig(t)
			seed(r)

			return headertest.Probe{
				ETag: "", Replayed: nil,
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

	seeded := basePath + "/trg-seed"
	scenarios := map[string]headertest.Scenario{
		"listUnitTriggers":  probe(http.MethodGet, basePath, ""),
		"createUnitTrigger": probe(http.MethodPost, basePath, scheduleDoc),
		"getTrigger":        probe(http.MethodGet, seeded, ""),
		"replaceTrigger":    probe(http.MethodPut, seeded, scheduleDoc),
		"deleteTrigger":     probe(http.MethodDelete, seeded, ""),
		"fireTrigger":       probe(http.MethodPost, seeded+"/fire", ""),
	}

	ops := fullRig(t).h.Operations()
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
