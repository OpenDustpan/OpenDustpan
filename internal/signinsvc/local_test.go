package signinsvc_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/session"
)

// localRequest returns the request of a good password sign-in of the local
// user usr-1 of the principal prn-1.
func localRequest() *signinsvc.LocalRequest {
	return &signinsvc.LocalRequest{
		Entry: callerEntry(), PrincipalID: "prn-1", UserID: "usr-1",
		Assertion: session.Assertion{
			AuthTime: testNow(), ProviderID: session.LocalProviderID, PrincipalID: "prn-1", Subject: "usr-1",
			UserName: "alice", Email: "", Method: session.MethodLocalPassword, Names: nil, Groups: nil,
			Factors: []session.Factor{session.FactorPassword}, EmailVerified: false, GroupsPresent: false,
			AuthTimeKnown: true,
		},
		Generation: 7, ChangeRequired: true,
	}
}

// TestCompleteLocalStartsTheSession checks that the session carries the
// generation of the record, the time of the check, the method kind and the
// factors, and that the flag to change the password reaches the entry and
// the result.
func TestCompleteLocalStartsTheSession(t *testing.T) {
	t.Parallel()

	w := newWorld()

	res, err := w.service(t).CompleteLocal(t.Context(), localRequest())
	if err != nil {
		t.Fatalf("CompleteLocal() = %v", err)
	}

	if len(w.starts) != 1 {
		t.Fatalf("sessions = %d, want 1", len(w.starts))
	}

	st := w.starts[0]
	if st.Generation != 7 || st.Provider != session.LocalProviderID || st.PrincipalID != "prn-1" ||
		!st.AuthTime.Equal(testNow()) || st.Stored {
		t.Errorf("start = %+v, want the local session of prn-1 with generation 7", st)
	}

	if !res.ChangeRequired || res.Method != session.MethodLocalPassword {
		t.Errorf("result = %+v, want the change flag and the method", res)
	}

	if len(w.signInInputs) != 0 {
		t.Fatalf("the local branch searched for a principal: %+v", w.signInInputs)
	}

	checkLocalEntry(t, w)
}

// checkLocalEntry fails unless the entry of the session names the local
// subject, the method, the factors and the flag to change the password.
func checkLocalEntry(t *testing.T, w *world) {
	t.Helper()

	e := &w.startEntries[0]
	if e.Subject != "local:usr-1" || changeOf(e, "method") != "local-password" ||
		changeOf(e, "factors") != "password" ||
		changeOf(e, "changeRequired") != "true" {
		t.Errorf("session entry = %+v, want the method, the factors and the change flag", e)
	}
}

// TestCompleteLocalRefusesAHostileAssertion sends a hostile Assertion
// straight to the local branch, as if the service of the local users had made
// it. Each case must refuse with an audit entry and start no session.
func TestCompleteLocalRefusesAHostileAssertion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		change func(r *signinsvc.LocalRequest)
		name   string
		reason string
	}{
		{func(r *signinsvc.LocalRequest) { r.Assertion.Subject = "" }, "empty subject", signinsvc.ReasonAssertion},
		{
			func(r *signinsvc.LocalRequest) { r.Assertion.AuthTime = testNow().AddDate(0, 0, 1) },
			"future authentication time", signinsvc.ReasonAssertion,
		},
		{
			func(r *signinsvc.LocalRequest) { r.Assertion.PrincipalID = "prn-other" },
			"other principal", signinsvc.ReasonAssertion,
		},
		{
			func(r *signinsvc.LocalRequest) { r.PrincipalID = "" },
			"record names no principal",
			signinsvc.ReasonAssertion,
		},
		{func(r *signinsvc.LocalRequest) { r.UserID = "usr-other" }, "other local user", signinsvc.ReasonAssertion},
		{
			func(r *signinsvc.LocalRequest) { r.Assertion.ProviderID = "corp" },
			"provider of a method port", signinsvc.ReasonAssertion,
		},
		{func(r *signinsvc.LocalRequest) {
			r.Assertion.Method, r.Assertion.Factors = session.MethodPasskey, []session.Factor{session.FactorPasskey}
		}, "passkey kind in the password branch", signinsvc.ReasonMethodMismatch},
		{
			func(r *signinsvc.LocalRequest) { r.Assertion.Method = session.MethodOIDC },
			"method port kind", signinsvc.ReasonAssertion,
		},
		{func(r *signinsvc.LocalRequest) { r.Assertion.Method = "" }, "empty kind", signinsvc.ReasonAssertion},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			req := localRequest()
			tc.change(req)

			_, err := w.service(t).CompleteLocal(t.Context(), req)
			if r := refusalOf(t, err); r.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", r.Reason, tc.reason)
			}

			if len(w.starts) != 0 || len(w.entries) != 1 || reasonOf(&w.entries[0]) != tc.reason {
				t.Fatalf("sessions %d, entries %+v, want a refusal entry with %q", len(w.starts), w.entries, tc.reason)
			}

			if got := changeOf(&w.entries[0], "provider"); got != session.LocalProviderID {
				t.Errorf("audit provider = %q, want %q", got, session.LocalProviderID)
			}
		})
	}
}

// TestCompleteLocalStartRefusals checks the refusal for a generation that
// changed after the check of the password, and for a principal that went
// off.
func TestCompleteLocalStartRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err    error
		name   string
		reason string
	}{
		{session.ErrGenerationChanged, "generation changed", signinsvc.ReasonGenerationChanged},
		{session.ErrPrincipalDisabled, "principal disabled", signinsvc.ReasonDisabled},
		{session.ErrPrincipalNotFound, "principal gone", signinsvc.ReasonNotTarget},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			w.startErr = tc.err

			_, err := w.service(t).CompleteLocal(t.Context(), localRequest())
			if r := refusalOf(t, err); r.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", r.Reason, tc.reason)
			}

			if len(w.entries) != 1 || reasonOf(&w.entries[0]) != tc.reason {
				t.Fatalf("entries = %+v, want one refusal with %q", w.entries, tc.reason)
			}
		})
	}
}
