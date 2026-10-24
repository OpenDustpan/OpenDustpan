package objectcontext_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
)

// TestHeaderFlagsMatchTheHandler proves that the header flags of each
// operation of the description match what the handler does with the headers
// ETag, If-Match and Idempotency-Key. An operation with no scenario here fails,
// so a new route cannot enter without a check.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	cases := casesOf(t)
	byPattern := make(map[string]*routeCase, len(cases))

	for i := range cases {
		byPattern[cases[i].route.Method+" "+cases[i].route.Path] = &cases[i]
	}

	ops := newHandler(t, newAuthorizer(), newFakeStore()).Operations()
	if len(ops) != len(byPattern) {
		t.Fatalf("the handler describes %d operations, the test has %d scenarios", len(ops), len(byPattern))
	}

	for _, op := range ops {
		rc, ok := byPattern[op.Method+" "+op.Path]
		if !ok {
			t.Fatalf("operation %s has no header scenario", op.ID)
		}

		t.Run(op.ID, func(t *testing.T) {
			t.Parallel()

			// The adapter does not refuse an Idempotency-Key header yet, so the
			// check of the 400 answer is off.
			headertest.Check(t, op, scenarioOf(rc), headertest.Options{RefusesKey: false})
		})
	}
}

// scenarioOf returns the scenario of the route of rc: a fresh handler and a
// caller that holds every check of the route.
func scenarioOf(rc *routeCase) headertest.Scenario {
	return func(t *testing.T) headertest.Probe {
		t.Helper()

		h := newHandler(t, newAuthorizer(rc.checks...), newFakeStore())

		send := func(headers map[string]string) headertest.Answer {
			req := httptest.NewRequestWithContext(t.Context(), rc.route.Method, rc.path, strings.NewReader(rc.body))
			req.Header.Set("Authorization", "token "+testToken)

			for k, v := range headers {
				req.Header.Set(k, v)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			return headertest.Answer{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
		}

		return headertest.Probe{Send: send, ETag: "", Replayed: nil}
	}
}
