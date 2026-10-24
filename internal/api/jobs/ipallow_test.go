package jobs_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

// addressRefused refuses every check as the IP allowlist gate does for a
// client address outside the allowlist of the namespace.
type addressRefused struct{}

func (addressRefused) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return errors.Join(identity.ErrDenied, ipallow.ErrAddressNotAllowed)
}

func (addressRefused) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// TestAddressOutsideTheAllowlistAnswers403 proves that the read and the start
// of a job answer 403 with access.ip-not-allowed for a client address outside
// the IP allowlist, as the other routes of the namespace do, and not the 404
// of a job that the caller cannot read. No job starts.
func TestAddressOutsideTheAllowlistAnswers403(t *testing.T) {
	t.Parallel()

	want := func(name string) errcodetest.Cause {
		return errcodetest.Cause{
			Name: name, Err: nil, Code: errcode.AccessIPNotAllowed, Message: "forbidden", Fixed: true,
		}
	}

	// A job that exists, a job that does not exist, a job in a namespace that
	// does not exist and a malformed job ID all answer the same 403: the
	// refusal does not tell whether the object exists.
	read := newHandler(t, addressRefused{}, newFakeJobs(runningJob(t, testJob)))

	for _, path := range []string{
		jobPath("acme", testJob),
		jobPath("acme", "job-unknownunknownunknow"),
		jobPath("absent", testJob),
		jobPath("absent", "job-unknownunknownunknow"),
	} {
		rec := serve(t, read, request{method: http.MethodGet, path: path, token: testToken})
		errcodetest.Check(t, rec.Code, rec.Body.Bytes(), want("read "+path))
	}

	starter := newFakeStarter(t)
	start := newStartHandler(t, addressRefused{}, newFakeJobs(), starter, nil)
	rec := post(t, start, testToken, goodBody)
	errcodetest.Check(t, rec.Code, rec.Body.Bytes(), want("start a job"))

	if starter.calls() != 0 {
		t.Fatalf("the job service got %d starts, want none", starter.calls())
	}
}
