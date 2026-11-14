package drift_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func startRequest(body string) request {
	return request{method: http.MethodPost, path: checksURL, token: testToken, body: body, headers: nil}
}

// checkDoc is the answer to a start.
type checkDoc struct {
	JobID string       `json:"jobId"` //nolint:tagliatelle // The description of the API demands camelCase: jobId.
	Unit  driftUnitDoc `json:"unit"`
}

func TestStartCheck(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(holdsAll()...))

	rec := r.serve(t, request{
		method: http.MethodPost, path: checksURL, token: testToken, body: "",
		headers: map[string]string{"User-Agent": "drift-test", "Dustpan-Channel": "cli"},
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST drift-checks = %d %s, want 202", rec.Code, rec.Body.String())
	}

	var doc checkDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}

	if doc.JobID != "job-fake1" || doc.Unit != (driftUnitDoc{ID: testUnitID, Tool: "OpenTofu", Name: "net"}) {
		t.Errorf("answer = %s, want job-fake1 for the unit ws-1", rec.Body.String())
	}

	if got, want := rec.Header().Get("Location"), "/api/dustpan/v1/namespaces/acme/jobs/job-fake1"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	checkStartedRequest(t, r)
}

// checkStartedRequest checks the request that the handler gave the checker
// for a start with no body.
func checkStartedRequest(t *testing.T, r *rig) {
	t.Helper()

	if len(r.checker.reqs) != 1 {
		t.Fatalf("checks = %d, want 1", len(r.checker.reqs))
	}

	req, actor := r.checker.reqs[0], r.checker.actors[0]

	if req.Unit.String() != "opentofu:acme/net" || req.Unit.UnitID() != testUnitID {
		t.Errorf("unit = %s with ID %q, want opentofu:acme/net with the ID of the path", req.Unit, req.Unit.UnitID())
	}

	if !req.Location.IsZero() || req.Pool != "" || req.Reason != "" {
		t.Errorf("request = %+v, want the defaults of the job service", req)
	}

	if req.UserAgent != "drift-test" || actor.Principal.Subject != "ada" || actor.Agent != "" {
		t.Errorf("request = %+v by %+v, want the user agent and the person ada", req, actor)
	}
}

func TestStartCheckBody(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(holdsAll()...))

	body := `{"pool":"ci","reason":"after the incident","location":{"connection":"gh","repository":"acme/infra",` +
		`"ref":"main","directory":"net","revision":"abc"}}`

	if rec := r.serve(t, startRequest(body)); rec.Code != http.StatusAccepted {
		t.Fatalf("POST = %d %s, want 202", rec.Code, rec.Body.String())
	}

	req := r.checker.reqs[0]

	want := job.Location{Connection: "gh", Repository: "acme/infra", Ref: "main", Directory: "net", Revision: "abc"}
	if req.Pool != "ci" || req.Reason != "after the incident" || req.Location != want {
		t.Errorf("request = %+v, want the pool, the reason and the location of the body", req)
	}
}

// TestStartCheckRefusesALineSeparator proves that a reason with a line
// separator, a paragraph separator or a format character never reaches the
// checker, and that the route answers the coded 400 of an invalid drift value.
func TestStartCheckRefusesALineSeparator(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, body string }{
		{"line separator", `{"reason":"a\u2028b"}`},
		{"paragraph separator", `{"reason":"a\u2029b"}`},
		{"format character", `{"reason":"a\u200bb"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, newAuthorizer(holdsAll()...))

			rec := r.serve(t, startRequest(tt.body))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "job.invalid") {
				t.Fatalf("POST = %d %s, want 400 job.invalid", rec.Code, rec.Body.String())
			}

			if r.checker.calls() != 0 {
				t.Errorf("checker calls = %d, want none", r.checker.calls())
			}
		})
	}
}

// TestStartCheckRefusesBadBodies proves that a body that is not valid never
// reaches the checker, and that a field that the server fixes is not accepted.
func TestStartCheckRefusesBadBodies(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"not JSON":                       `pool=ci`,
		"an array":                       `[]`,
		"a number":                       `1`,
		"an unknown field":               `{"unknown":1}`,
		"the operation":                  `{"operation":"apply"}`,
		"the purpose":                    `{"purpose":"drift-check"}`,
		"the source":                     `{"source":"schedule"}`,
		"the steps":                      `{"steps":[{"name":"x","args":["sh"]}]}`,
		"the executor":                   `{"executor":"write-back"}`,
		"the environment":                `{"env":{"TF_CLI_ARGS":"-lock=false"}}`,
		"a pool that is a number":        `{"pool":1}`,
		"a location that is a string":    `{"location":"main"}`,
		"a location with an extra field": `{"location":{"ref":"main","depth":1}}`,
		"two documents":                  `{} {}`,
		"text after the document":        `{} x`,
		"a body over the limit":          `{"reason":"` + strings.Repeat("a", 64<<10) + `"}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, newAuthorizer(holdsAll()...))

			rec := r.serve(t, startRequest(body))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "request.body-invalid") {
				t.Fatalf("POST = %d %s, want 400 request.body-invalid", rec.Code, rec.Body.String())
			}

			if r.checker.calls() != 0 {
				t.Errorf("checker calls = %d, want none for a body that is not valid", r.checker.calls())
			}
		})
	}
}

// TestStartCheckAcceptsAnEmptyBody proves that the body is optional.
func TestStartCheckAcceptsAnEmptyBody(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"", "  \n", "{}"} {
		r := newRig(t, newAuthorizer(holdsAll()...))

		if rec := r.serve(t, startRequest(body)); rec.Code != http.StatusAccepted {
			t.Errorf("POST with body %q = %d %s, want 202", body, rec.Code, rec.Body.String())
		}
	}
}

// TestStartCheckRefusesAKey proves that the route never ignores an
// Idempotency-Key header: it answers 400 and starts nothing.
func TestStartCheckRefusesAKey(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(holdsAll()...))

	rec := r.serve(t, request{
		method: http.MethodPost, path: checksURL, token: testToken, body: "",
		headers: map[string]string{"Idempotency-Key": "abc"},
	})
	if rec.Code != http.StatusBadRequest || r.checker.calls() != 0 {
		t.Fatalf("POST with a key = %d %s with %d checks, want 400 and none", rec.Code, rec.Body.String(),
			r.checker.calls())
	}
}

// TestStartCheckErrors maps each refusal of the checker to a status and a code,
// and never shows the text of an error that is not a refusal.
func TestStartCheckErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"denied", fmt.Errorf("%w: unit:run-plan", identity.ErrDenied), http.StatusForbidden, ""},
		{"policy denied", fmt.Errorf("%w: no", job.ErrPolicyDenied), http.StatusForbidden, "job.policy-denied"},
		{"agent denied", job.ErrAgentDenied, http.StatusForbidden, "job.agent-denied"},
		{"enforced gate", job.ErrGateEnforced, http.StatusForbidden, "job.gate-enforced"},
		{
			"enforced gate with names",
			&job.GateError{Gates: []job.EnforcedGate{{Scope: "ns:acme", ID: "g1", Point: "change.propose"}}},
			http.StatusForbidden,
			"job.gate-enforced",
		},
		{"invalid", fmt.Errorf("%w: reason", job.ErrInvalid), http.StatusBadRequest, "job.invalid"},
		{"invalid drift value", fmt.Errorf("%w: unit", drift.ErrInvalid), http.StatusBadRequest, "job.invalid"},
		{
			"unsupported",
			fmt.Errorf("%w: no refresh-only plan", job.ErrUnsupported),
			http.StatusUnprocessableEntity,
			"job.unsupported",
		},
		{"frozen", job.ErrFrozen, http.StatusConflict, "job.frozen"},
		{"same minute, other request", job.ErrKeyConflict, http.StatusConflict, "drift.start-conflict"},
		{"fenced", namespace.ErrFenced, http.StatusConflict, "namespace.fenced"},
		{"limit", job.ErrLimit, http.StatusTooManyRequests, "job.limit"},
		{
			"policy denied that also wraps denied",
			fmt.Errorf("%w: %w", identity.ErrDenied, job.ErrPolicyDenied),
			http.StatusForbidden,
			errcode.AccessDenied,
		},
		{
			"frozen that also wraps denied",
			fmt.Errorf("%w: %w", identity.ErrDenied, job.ErrFrozen),
			http.StatusForbidden,
			errcode.AccessDenied,
		},
		{"job not found", job.ErrNotFound, http.StatusNotFound, ""},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, ""},
		{
			"other",
			errDriver,
			http.StatusInternalServerError,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, newAuthorizer(holdsAll()...))
			r.checker.err = tt.err

			rec := r.serve(t, startRequest(""))
			if rec.Code != tt.status || (tt.code != "" && !strings.Contains(rec.Body.String(), tt.code)) {
				t.Fatalf("POST = %d %s, want %d %s", rec.Code, rec.Body.String(), tt.status, tt.code)
			}

			if strings.Contains(rec.Body.String(), "hunter2") {
				t.Errorf("answer = %s: it shows the text of an error that is no refusal", rec.Body.String())
			}
		})
	}
}

// TestHeaderFlagsMatchTheHandler proves that the header flags of each operation
// of the description match what the handler does with the header: ETag and
// Idempotency-Key. An operation with no scenario here fails, so a new route
// cannot enter without a check.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	probe := func(r *rig, method, target string) headertest.Probe {
		return headertest.Probe{
			ETag: "", Replayed: nil,
			Send: func(headers map[string]string) headertest.Answer {
				rec := r.serve(t, request{method: method, path: target, token: testToken, body: "", headers: headers})

				return headertest.Answer{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
			},
		}
	}

	scenarios := map[string]headertest.Scenario{
		"getUnitDrift": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(newRig(t, newAuthorizer(holdsAll()...)), http.MethodGet, driftURL)
		},
		"startDriftCheck": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(newRig(t, newAuthorizer(holdsAll()...)), http.MethodPost, checksURL)
		},
	}

	h := newRig(t, newAuthorizer()).handler

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

// recordingAudit keeps the entries that the handler appends.
type recordingAudit struct {
	audit.Store

	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordingAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.entries = append(a.entries, e)

	return e, nil
}

// AppendServer keeps an entry of the installation scope, so that a test counts
// the refusals of a namespace that does not exist.
func (a *recordingAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.entries = append(a.entries, e)

	return e, nil
}

// TestStartWritesADenialForEachRefusal proves that every refusal of the start
// writes an audit entry with a fixed reason, and never the text of the error.
func TestStartWritesADenialForEachRefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"denied by the service", fmt.Errorf("%w: hunter2", identity.ErrDenied), "policy"},
		{"policy", fmt.Errorf("%w: hunter2", job.ErrPolicyDenied), "policy denied the job"},
		{"agent mode", fmt.Errorf("%w: hunter2", job.ErrAgentDenied), "an agent cannot do this"},
		{"freeze", job.ErrFrozen, "a change freeze is active"},
		{"limit", job.ErrLimit, "too many open jobs"},
		{"gate", &job.GateError{Gates: []job.EnforcedGate{{Scope: "s", ID: "no-plans", Point: "p"}}}, "no-plans"},
		{"unsupported", fmt.Errorf("%w: hunter2", job.ErrUnsupported), "the server cannot start this job"},
		{
			"policy that also wraps denied",
			fmt.Errorf("%w: %w: hunter2", identity.ErrDenied, job.ErrPolicyDenied),
			refusal.ReasonPolicy,
		},
		{
			"gate that also wraps denied",
			fmt.Errorf("%w: %w", identity.ErrDenied,
				&job.GateError{Gates: []job.EnforcedGate{{Scope: "s", ID: "no-plans", Point: "p"}}}),
			refusal.ReasonPolicy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
			r := newRig(t, newAuthorizer(holdsAll()...), func(cfg *driftapi.Config) { cfg.Audit = entries })
			r.checker.err = tt.err

			if rec := r.serve(t, startRequest("")); rec.Code < 400 {
				t.Fatalf("POST = %d, want a refusal", rec.Code)
			}

			if len(entries.entries) != 1 {
				t.Fatalf("audit entries = %d, want 1", len(entries.entries))
			}

			e := entries.entries[0]
			if e.Subject != "ada" || e.Action != string(identity.ActionUnitRunPlan) ||
				e.Object != "opentofu:acme/net" ||
				e.Outcome != audit.OutcomeDenied ||
				!strings.Contains(e.Reason, tt.wantReason) {
				t.Errorf("entry = %+v, want a refusal of unit:run-plan for ada with the reason %q", e, tt.wantReason)
			}

			if strings.Contains(e.Reason, "hunter2") {
				t.Errorf("reason %q holds the text of the error", e.Reason)
			}
		})
	}
}

// TestStartWritesNoDenialForOtherErrors proves that a request that is not valid
// and a failure of the server write no denial entry.
func TestStartWritesNoDenialForOtherErrors(t *testing.T) {
	t.Parallel()

	for _, err := range []error{fmt.Errorf("%w: no pool", job.ErrInvalid), namespace.ErrFenced, errDriver} {
		entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
		r := newRig(t, newAuthorizer(holdsAll()...), func(cfg *driftapi.Config) { cfg.Audit = entries })
		r.checker.err = err

		r.serve(t, startRequest(""))

		if len(entries.entries) != 0 {
			t.Errorf("error %v wrote %d audit entries, want none", err, len(entries.entries))
		}
	}
}
