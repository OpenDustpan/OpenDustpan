package jobs_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// kindAuthenticator accepts the test token and answers a principal of one kind.
type kindAuthenticator struct {
	kind identity.Kind
}

func (a kindAuthenticator) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	p, err := tokenAuthenticator{}.AuthenticateToken(ctx, token)
	if err != nil {
		return identity.Principal{}, err
	}

	p.Kind = a.kind
	p.Credential = identity.CredentialAccessToken

	return p, nil
}

// freshApproveRig returns a rig whose handler holds policy. The job of the rig is a
// destroy that waits for approval, and the caller holds job:read and
// job:approve.
func freshApproveRig(t *testing.T, policy freshstep.Policy, kind identity.Kind) *approveRig {
	t.Helper()

	r := newApproveRig(t, holdsApprove()...)
	j := runningJob(t, testJob)
	j.Spec.Operation = job.OperationDestroy
	j.State = job.StateAwaitingApproval

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces: namespaces, Jobs: newFakeJobs(j), Units: nil, Artifacts: nil, Starter: r.svc,
		Workers: nil, Pools: nil, Authenticator: kindAuthenticator{kind: kind}, Authorizer: r.authz,
		Audit: r.entries, OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil,
		Logger: slog.New(slog.DiscardHandler), Logs: nil, Cursors: nil,
		Approvers:   gate.ApproverPolicy{Production: false, IsDev: nil},
		FreshPolicy: policy,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	r.h = testHandler{Handler: handler, served: apidesctest.CheckStatus(t, handler)}
	r.svc.job = j

	return r
}

// TestDestroyApprovalByTokenFollowsSetting proves that an access token that
// holds the grant approves a destroy with the setting off, and that the
// handler refuses it with the setting on, with no help from the service. It
// also proves that a nil policy refuses, and that an agent principal never
// gets the skip.
func TestDestroyApprovalByTokenFollowsSetting(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	tests := []struct {
		name      string
		policy    freshstep.Policy
		kind      identity.Kind
		status    int
		code      string
		wantCalls int
	}{
		{"setting off", off, identity.KindPerson, http.StatusCreated, "", 1},
		{"setting on", on, identity.KindPerson, http.StatusForbidden, errcode.MFAFreshStepRequired, 0},
		{"no policy", nil, identity.KindPerson, http.StatusForbidden, errcode.MFAFreshStepRequired, 0},
		{"setting off, service principal", off, identity.KindService, http.StatusForbidden, errcode.MFAFreshStepRequired, 0},
		{"setting off, workload principal", off, identity.KindWorkload, http.StatusForbidden, errcode.MFAFreshStepRequired, 0},
		{"setting off, agent principal", off, identity.KindAgent, http.StatusForbidden, errcode.MFAFreshStepRequired, 0},
		{"setting on, agent principal", on, identity.KindAgent, http.StatusForbidden, errcode.MFAFreshStepRequired, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := freshApproveRig(t, tc.policy, tc.kind)

			rec := r.send(t, testJob, goodApprove)
			if rec.Code != tc.status || len(r.svc.called()) != tc.wantCalls {
				t.Fatalf("status = %d, service calls = %d; want %d and %d",
					rec.Code, len(r.svc.called()), tc.status, tc.wantCalls)
			}

			if tc.code == "" {
				return
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rec.Code, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}
