package inventory_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
)

// TestViewHeaderFlagsMatchTheHandler proves that the header flags of the
// operations of the saved views match what the handler does with the header.
// An operation with a flag and no scenario here fails, and so does a write
// operation with no scenario, because the check of the "no flag" direction
// needs one. The reads with no scenario set no flag.
func TestViewHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)

	var names atomic.Int64

	// body returns a view body with a name that no earlier call used.
	body := func() string {
		return fmt.Sprintf(`{"name":"View %d","query":{"labels":[],"references":[]}}`, names.Add(1))
	}

	probe := func(t *testing.T, method, target, body string) headertest.Probe {
		t.Helper()

		return headertest.Probe{
			ETag: "", Replayed: nil,
			Send: func(headers map[string]string) headertest.Answer {
				call := &viewCall{method: method, target: target, token: tokenAna, body: body}

				got := f.send(t, call, headers["Idempotency-Key"])

				return headertest.Answer{Status: got.status, Header: got.header, Body: got.body}
			},
		}
	}

	scenarios := map[string]headertest.Scenario{
		"listViews": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(t, http.MethodGet, viewsPath, "")
		},
		"createView": func(t *testing.T) headertest.Probe {
			t.Helper()

			p := probe(t, http.MethodPost, viewsPath, body())
			p.Replayed = func(first, again headertest.Answer) bool {
				return again.Status == http.StatusConflict &&
					strings.Contains(again.Body, "request.idempotency-key-replayed-secret") &&
					first.Status == http.StatusCreated
			}

			return p
		},
		"getView": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(t, http.MethodGet, viewsPath+"/"+f.createView(t, tokenAna, body()), "")
		},
		"deleteView": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(t, http.MethodDelete, viewsPath+"/"+f.createView(t, tokenAna, body()), "")
		},
		"listViewUnits": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(t, http.MethodGet, viewsPath+"/"+f.createView(t, tokenAna, body())+"/units", "")
		},
	}

	for _, op := range f.handler.Operations() {
		t.Run(op.ID, func(t *testing.T) {
			t.Parallel()

			scenario, ok := scenarios[op.ID]
			if !ok {
				if headertest.Flagged(op) || headertest.Writes(op) {
					t.Fatalf("%s sets a header flag or writes, and has no scenario", op.ID)
				}

				t.Skip("the route sets no header flag")
			}

			// The inventory adapter ignores a key on a route that does not
			// take it, so the test does not check for a refusal.
			headertest.Check(t, op, scenario, headertest.Options{RefusesKey: false})
		})
	}
}
