package jobstore_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/identity"
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

			err := check(j, ap, now, identity.KindPerson, true)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckDestroyProof = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestFreshRequiredFailsClosed proves that the store requires the step when it
// has no resolver, when the resolver cannot read the setting, and when the
// transaction cannot read the row. A resolver that answers no step lets the
// approval through, and it gets the stored value that the transaction read.
func TestFreshRequiredFailsClosed(t *testing.T) {
	t.Parallel()

	off := func(*string) (bool, error) { return false, nil }
	on := func(*string) (bool, error) { return true, nil }
	failing := func(*string) (bool, error) { return false, errSettingsDown }
	tests := []struct {
		name     string
		resolver jobstore.FreshResolver
		settings map[string]string
		readErr  error
		want     bool
		wantErr  bool
	}{
		{"no resolver", nil, nil, nil, true, false},
		{"no resolver, setting off", nil, map[string]string{"freshAuthEnabled": "false"}, nil, true, false},
		{"setting on", on, nil, nil, true, false},
		{"setting off", off, map[string]string{"freshAuthEnabled": "false"}, nil, false, false},
		{"unreadable setting", failing, nil, nil, true, false},
		{"unreadable row", off, nil, errSettingsDown, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jobstore.New(nil)
			if tc.resolver != nil {
				s.SetFreshResolver(tc.resolver)
			}

			tx := settingTx{fakeTx: newFakeTx(time.Time{}), settings: tc.settings, err: tc.readErr}

			got, err := jobstore.ReadFreshRequired(t.Context(), s, tx)
			if (err != nil) != tc.wantErr {
				t.Fatalf("FreshRequired error = %v, want error %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Fatalf("FreshRequired = %v, want %v", got, tc.want)
			}
		})
	}
}
