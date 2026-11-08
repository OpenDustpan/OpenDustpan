package jobs_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestHeaderFlagsMatchTheHandler proves that the header flags of each
// operation of the description match what the handler does with the header:
// ETag and Idempotency-Key. An operation with no scenario here fails, so a new
// route cannot enter without a check.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	probe := func(h http.Handler, method, target, body string) headertest.Probe {
		return headertest.Probe{
			ETag: "", Replayed: nil,
			Send: func(headers map[string]string) headertest.Answer {
				req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
				req.Header.Set("Authorization", "token "+testToken)

				for k, v := range headers {
					req.Header.Set(k, v)
				}

				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				return headertest.Answer{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
			},
		}
	}

	scenarios := map[string]headertest.Scenario{
		"getJob": func(t *testing.T) headertest.Probe {
			t.Helper()

			h := newHandler(t, newAuthorizer(holdsBoth()...), newFakeJobs(runningJob(t, testJob)))

			return probe(h, http.MethodGet, jobPath("acme", testJob), "")
		},
		"listJobs": func(t *testing.T) headertest.Probe {
			t.Helper()

			jobs := newFakeJobs()
			jobs.listed = func(job.Filter) job.Page { return job.Page{Jobs: nil, Damaged: nil, After: ""} }
			h := newHandler(t, newAuthorizer(readsNamespace()...), jobs)

			return probe(h, http.MethodGet, jobsURL, "")
		},
		"startJob": func(t *testing.T) headertest.Probe {
			t.Helper()

			starter := newFakeStarter(t)
			starter.keys = map[string]bool{}
			h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

			return probe(h, http.MethodPost, jobsURL, goodBody)
		},
	}

	h := newStartHandler(t, newAuthorizer(), newFakeJobs(), newFakeStarter(t), nil)

	ops := h.Operations()
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
