package jobs_test

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestForbiddenCodes proves that each cause of a 403 keeps its message and
// status and adds its machine code, on the route that starts a job. A read of
// a job answers 404 for a refusal, so the start route is the 403 route.
func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	for _, cause := range errcodetest.Causes(t) {
		t.Run(cause.Name, func(t *testing.T) {
			t.Parallel()

			starter := newFakeStarter(t)
			starter.err = cause.Err
			h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

			rec := post(t, h, testToken, goodBody)

			errcodetest.Check(t, rec.Code, rec.Body.Bytes(), cause)
		})
	}
}

// TestForbiddenAccessLimit proves that a start that an access limit refuses
// answers its own code, not access.denied. The limited authorizer wraps the
// gate refusal in identity.ErrDenied.
func TestForbiddenAccessLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sentinel error
		code     string
	}{
		{"limit forbids", gate.ErrLimited, errcode.GateLimited},
		{"limit failed to evaluate", gate.ErrRuleFailed, errcode.GateRuleFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			refusal := &gate.RefusalError{Err: tc.sentinel, Record: "", Messages: nil}
			cause := errcodetest.Cause{
				Name: tc.name, Err: fmt.Errorf("%w: %w", identity.ErrDenied, refusal), Code: tc.code,
				Message: "forbidden", Fixed: true,
			}

			starter := newFakeStarter(t)
			starter.err = cause.Err
			h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

			rec := post(t, h, testToken, goodBody)

			errcodetest.Check(t, rec.Code, rec.Body.Bytes(), cause)
		})
	}
}
