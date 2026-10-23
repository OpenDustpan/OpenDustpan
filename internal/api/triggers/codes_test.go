package triggers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// lockedFire is the refusal of a manual lock that the job service wraps in
// job.ErrLocked. The reason of the lock must not reach the arguments.
func lockedFire() error {
	return fmt.Errorf("%w: %w", job.ErrLocked, &manuallock.LockedError{
		Lock: manuallock.Lock{
			Since:  time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC),
			Target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "net"}),
			Holder: "user-1", Reason: "secret reason",
		},
		Noun: "workspace", Name: "acme/net",
	})
}

// TestFireErrorsCarryTheirCode proves that each error that the fire path can
// return answers with the code and the arguments of the catalog.
func TestFireErrorsCarryTheirCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		want   errcodetest.Want
	}{
		{"a rate limit", identity.ErrRateLimited, http.StatusTooManyRequests, errcodetest.Want{Code: errcode.AuthRateLimited}},
		{
			"no credential", identity.ErrUnauthenticated, http.StatusUnauthorized,
			errcodetest.Want{Code: errcode.AuthUnauthenticated},
		},
		{"a namespace that is gone", namespace.ErrNotFound, http.StatusNotFound, errcodetest.Want{Code: errcode.TriggerNotFound}},
		{"a trigger that is gone", trigger.ErrNotFound, http.StatusNotFound, errcodetest.Want{Code: errcode.TriggerNotFound}},
		{"a job that is gone", job.ErrNotFound, http.StatusNotFound, errcodetest.Want{Code: errcode.TriggerNotFound}},
		{
			"another home region", namespace.ErrNotHome, http.StatusMisdirectedRequest,
			errcodetest.Want{Code: errcode.NamespaceNotHome},
		},
		{
			"maintenance mode", maintenance.ErrInMaintenance, http.StatusServiceUnavailable,
			errcodetest.Want{Code: errcode.MaintenanceInMaintenance},
		},
		{"a fenced write", namespace.ErrFenced, http.StatusConflict, errcodetest.Want{Code: errcode.NamespaceFenced}},
		{"a job that is not valid", job.ErrInvalid, http.StatusBadRequest, errcodetest.Want{Code: errcode.JobInvalid}},
		{"a rule of the agent", job.ErrAgentDenied, http.StatusForbidden, errcodetest.Want{Code: errcode.JobAgentDenied}},
		{
			"a manual lock", lockedFire(), http.StatusConflict,
			errcodetest.Want{Code: errcode.LockLocked, Args: map[string]any{
				"holder": "user-1", "since": "2026-10-23T09:30:00Z", "target": "opentofu:acme/net",
				"targetKind": "unit", "unit": "acme/net",
			}},
		},
		{"an error that no rule maps", errDatabaseDown, http.StatusInternalServerError, errcodetest.Want{Code: errcode.ServerInternal}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := fullRig(t)
			seed(r)
			r.disp.err = tt.err

			code, body, _ := do(t, r, http.MethodPost, basePath+"/trg-seed/fire", "")
			if code != tt.status {
				t.Fatalf("status = %d %s, want %d", code, body, tt.status)
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tt.status, []byte(body), tt.want)
		})
	}
}

// TestRouteRefusalsCarryTheirCode proves that the refusals of the route wrapper
// carry a code of the catalog: a token that is not valid, a token that the rate
// limit stops, an object that is not there, a key on a route that takes none and
// an Idempotency-Key that is not valid.
func TestRouteRefusalsCarryTheirCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		req    request
		status int
		code   string
	}{
		{
			"a token that is not valid", request{method: http.MethodGet, path: basePath, token: "wrong"},
			http.StatusUnauthorized, errcode.AuthUnauthenticated,
		},
		{
			"a token that the rate limit stops", request{method: http.MethodGet, path: basePath, token: limitedToken},
			http.StatusTooManyRequests, errcode.AuthRateLimited,
		},
		{
			"a namespace that is not there",
			request{
				method: http.MethodGet, token: testToken,
				path: "/api/dustpan/v1/namespaces/absent/units/" + netID + "/triggers",
			},
			http.StatusNotFound, errcode.TriggerNotFound,
		},
		{
			"a key on a route that takes none",
			request{
				method: http.MethodPost, path: basePath, token: testToken, body: scheduleDoc,
				header: map[string]string{"Idempotency-Key": "k-1"},
			},
			http.StatusBadRequest, errcode.RequestIdempotencyKeyUnsupported,
		},
		{
			"a key that is not valid",
			request{
				method: http.MethodPost, path: basePath + "/trg-seed/fire", token: testToken,
				header: map[string]string{"Idempotency-Key": "drift-1"},
			},
			http.StatusBadRequest, errcode.RequestIdempotencyKeyInvalid,
		},
		{
			"a body that is not valid",
			request{method: http.MethodPost, path: basePath, token: testToken, body: "[]"},
			http.StatusBadRequest, errcode.RequestBodyInvalid,
		},
		{
			"a field that is not valid",
			request{method: http.MethodPost, path: basePath, token: testToken, body: `{"kind":"manual"}`},
			http.StatusBadRequest, errcode.TriggerInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := fullRig(t)
			seed(r)

			rec := serve(t, r.h, tt.req)
			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tt.status, rec.Body.Bytes(), errcodetest.Want{
				Code: tt.code, Args: nil, RequestBody: []byte(tt.req.body),
			})
		})
	}
}

// readsThenRefuses lets the read actions pass and refuses every other action
// with the error of refuse. The route wrapper answers 404 for a refused read,
// so a test that wants a 403 lets the reads pass first.
type readsThenRefuses struct{ refuse errcodetest.Authorizer }

func (a readsThenRefuses) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionNamespaceRead || action == identity.ActionUnitRead {
		return nil
	}

	return a.refuse.Authorize(ctx, p, action, obj)
}

func (a readsThenRefuses) ListObjects(
	ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	return a.refuse.ListObjects(ctx, p, action)
}

// TestForbiddenAnswersCarryTheCodeOfTheirCause proves that each cause of a 403
// answers its own code, for a route that checks an action.
func TestForbiddenAnswersCarryTheCodeOfTheirCause(t *testing.T) {
	t.Parallel()

	for _, c := range errcodetest.Causes(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, readsThenRefuses{refuse: errcodetest.Authorizer{Err: c.Err}})
			seed(r)

			rec := serve(t, r.h, request{
				method: http.MethodPost, path: basePath + "/trg-seed/fire", token: testToken, body: "", header: nil,
			})

			errcodetest.Check(t, rec.Code, rec.Body.Bytes(), c)
		})
	}
}

// TestSaverDeniedKeepsItsCode proves that the refusal of a saver without access
// answers trigger.saver-denied and not the code of a missing permission, though
// the error wraps identity.ErrDenied.
func TestSaverDeniedKeepsItsCode(t *testing.T) {
	t.Parallel()

	r := fullRig(t)
	seed(r)
	r.disp.err = trigger.ErrSaverDenied

	code, body, _ := do(t, r, http.MethodPost, basePath+"/trg-seed/fire", "")
	if code != http.StatusForbidden {
		t.Fatalf("status = %d %s, want 403", code, body)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, code, []byte(body), errcodetest.Want{
		Code: errcode.TriggerSaverDenied,
	})
}
