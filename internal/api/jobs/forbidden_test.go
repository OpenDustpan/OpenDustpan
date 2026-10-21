package jobs_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
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
