package jobstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/job"
)

// errSettingsDown is the error of a settings store that cannot be read.
var errSettingsDown = errors.New("settings store down")

// destroyApproval returns a job of a destroy and an approval of a person at
// at, with no proof of a fresh step.
func destroyApproval(t *testing.T, at time.Time) (*job.Job, *job.Approval) {
	t.Helper()

	j := fullJob(t)
	j.Spec.Operation = job.OperationDestroy

	ap := j.Phases[0].Approvals[0]
	ap.Agent, ap.Verdict, ap.At, ap.Auth, ap.AuthAt = "", job.VerdictApprove, at, "", time.Time{}

	return &j, &ap
}

// TestDestroyProofFollowsTheSetting is the table of the store layer. With
// the setting off, an approval of a destroy that records the authentication
// none passes with no proof. With it on, the same approval is refused. An
// agent and an unknown verdict stay refused with the setting off.
func TestDestroyProofFollowsTheSetting(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		required bool
		edit     func(*job.Approval)
		wantErr  error
	}{
		{"off, auth none passes", false, func(ap *job.Approval) { ap.Auth = job.ConfirmAuthNone }, nil},
		{"on, auth none refused", true, func(ap *job.Approval) { ap.Auth = job.ConfirmAuthNone }, job.ErrAuthStale},
		{"on, no proof refused", true, func(*job.Approval) {}, job.ErrAuthStale},
		{"off, no auth refused", false, func(*job.Approval) {}, job.ErrAuthStale},
		{"off, auth none with a time refused", false, func(ap *job.Approval) {
			ap.Auth, ap.AuthAt = job.ConfirmAuthNone, ap.At
		}, job.ErrAuthStale},
		{"off, agent refused", false, func(ap *job.Approval) {
			ap.Auth, ap.Agent = job.ConfirmAuthNone, "agent"
		}, job.ErrAuthStale},
		{"off, unknown verdict refused", false, func(ap *job.Approval) {
			ap.Auth, ap.Verdict = job.ConfirmAuthNone, "unknown"
		}, job.ErrAuthStale},
		{"off, fresh MFA proof passes", false, func(ap *job.Approval) {
			ap.Auth, ap.AuthAt = job.ConfirmAuthMFA, ap.At
		}, nil},
		{"on, fresh MFA proof passes", true, func(ap *job.Approval) {
			ap.Auth, ap.AuthAt = job.ConfirmAuthMFA, ap.At
		}, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			j, ap := destroyApproval(t, now)
			tc.edit(ap)

			check := jobstore.CheckDestroyProofStepOff
			if tc.required {
				check = jobstore.CheckDestroyProofStepOn
			}

			err := check(j, ap, now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckDestroyProof = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestStoreWithoutPolicyRequiresTheStep proves that the store fails closed:
// a store with no policy, or a policy that cannot read the setting, requires
// the step.
func TestStoreWithoutPolicyRequiresTheStep(t *testing.T) {
	t.Parallel()

	failing := freshstep.NewPolicy(func(context.Context) (bool, error) {
		return false, errSettingsDown
	})
	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	tests := []struct {
		name   string
		policy freshstep.Policy
		want   bool
	}{
		{"no policy", nil, true},
		{"unreadable setting", failing, true},
		{"setting off", off, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jobstore.New(nil)
			if tc.policy != nil {
				s.SetFreshPolicy(tc.policy)
			}

			if got := s.FreshStepRequired(t.Context()); got != tc.want {
				t.Fatalf("FreshStepRequired = %v, want %v", got, tc.want)
			}
		})
	}
}
