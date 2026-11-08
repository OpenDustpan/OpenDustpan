package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// stepUpHeader is the header of a step-up credential, and stepUpValue is a
// value of the form of a step-up credential. The server has no step-up yet, so
// no value of this form is valid.
const (
	stepUpHeader = "Dustpan-Step-Up"
	stepUpValue  = "stp-0123456789abcdefghijklmnopqrstuvwxyzABCDEF"
)

// TestStepUpCredentialAloneGivesNoFreshAuthentication sends a change that
// needs a fresh authentication, with a step-up credential in the request,
// from a job token and from access tokens. The credential alone must give no
// fresh authentication: each request answers 403 with
// mfa.fresh-step-required, as with no credential.
func TestStepUpCredentialAloneGivesNoFreshAuthentication(t *testing.T) {
	t.Parallel()

	callers := prefsCallers{
		"t-job": withJob(caller("tester", identity.KindWorkload, identity.CredentialJobToken, false), "job-1"),
		// A personal access token, as the CLI sign-in or the token exchange
		// issues it.
		"t-person": caller("tester", identity.KindPerson, identity.CredentialAccessToken, false),
		"t-trust":  caller("ci-deploy", identity.KindService, identity.CredentialTrustToken, false),
	}

	h, _ := newServiceBackedHandlerOf(t, callers, allowAll{}, failedSeed())

	for _, tc := range []struct {
		name, token string
		values      []string
	}{
		{"a job token with a step-up credential", "t-job", []string{stepUpValue}},
		{"a job token with two values in one header line", "t-job", []string{stepUpValue + ", " + stepUpValue}},
		{"a job token with two header lines", "t-job", []string{stepUpValue, stepUpValue}},
		{"a personal access token with a step-up credential", "t-person", []string{stepUpValue}},
		{"a personal access token with two header lines", "t-person", []string{stepUpValue, stepUpValue}},
		{"a trust token with a step-up credential", "t-trust", []string{stepUpValue}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(testContext(t), http.MethodPost,
				"/api/server/principals/prn-ci-deploy/tokens", strings.NewReader(`{"name":"ci"}`))
			req.Header.Set("Authorization", "Bearer "+tc.token)
			for _, v := range tc.values {
				req.Header.Add(stepUpHeader, v)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := decodeErrorBody(t, rec)
			if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode {
				t.Fatalf("token change = %d %s, want 403 with errorCode %s", rec.Code, rec.Body.String(),
					freshErrorCode)
			}
		})
	}
}

// withJob returns p as the principal of the job token of job.
func withJob(p identity.Principal, job string) identity.Principal {
	p.JobID = job

	return p
}
