package signinsvc_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/session"
)

// stateWorld answers the state of a person for the passkey sign-in.
type stateWorld struct {
	err   error
	owner session.Owner
	gen   uint64
}

func (s *stateWorld) PrincipalState(_ context.Context, _ string) (session.PrincipalState, error) {
	var (
		policy   session.Policy
		accounts session.AccountPolicy
	)

	return session.PrincipalState{Owner: s.owner, Policy: policy, Accounts: accounts, Generation: s.gen}, s.err
}

// stateOf returns the reader of the state of a person who is owner, at the
// session generation gen.
func stateOf(owner session.Owner, gen uint64) *stateWorld {
	return &stateWorld{err: nil, owner: owner, gen: gen}
}

// localOwner is a local user that can sign in.
func localOwner() session.Owner {
	return session.Owner{
		ActiveAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-1", Subject: "local:usr-1", DisplayName: "Alice",
		Kind: "person", BreakGlass: false,
	}
}

// passkeyService returns a Service over w that reads st for the passkey
// sign-in.
func passkeyService(t *testing.T, w *world, st *stateWorld) *signinsvc.Service {
	t.Helper()

	svc, err := signinsvc.New(signinsvc.Config{
		Principals:     w,
		Disabler:       w,
		Generations:    w,
		Sessions:       w,
		Audit:          w,
		TempAdmins:     w,
		Groups:         nil,
		Removals:       nil,
		States:         st,
		Locks:          nil,
		NameKeys:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		OnAuditFailure: func() { w.auditFailed++ },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return svc
}

// passkeyRequest returns the request of a good passkey sign-in of prn-1.
func passkeyRequest() *signinsvc.PasskeyRequest {
	return &signinsvc.PasskeyRequest{
		Entry: callerEntry(), PrincipalID: "prn-1", Reason: "", UserVerified: true,
		Assertion: session.Assertion{
			AuthTime: testNow(), ProviderID: session.LocalProviderID, PrincipalID: "prn-1", Subject: "prn-1",
			UserName: "", Email: "", Method: session.MethodPasskey, Names: nil, Groups: nil,
			Factors: []session.Factor{session.FactorPasskey}, EmailVerified: false, GroupsPresent: false,
			AuthTimeKnown: true,
		},
	}
}

// TestCompletePasskeyStartsTheSession checks that the session carries the
// generation of the read of the state, that the entry names the method, the
// factor and the two factors that user verification gives, and that a local
// user raises no alert.
func TestCompletePasskeyStartsTheSession(t *testing.T) {
	t.Parallel()

	w := newWorld()
	st := stateOf(localOwner(), 9)

	res, err := passkeyService(t, w, st).CompletePasskey(t.Context(), passkeyRequest())
	if err != nil {
		t.Fatalf("CompletePasskey() = %v", err)
	}

	if len(w.starts) != 1 {
		t.Fatalf("sessions = %d, want 1", len(w.starts))
	}

	checkLocalPasskeyStart(t, &w.starts[0])

	e := &w.startEntries[0]
	if res.Method != session.MethodPasskey || e.Subject != "local:usr-1" || changeOf(e, "method") != "passkey" ||
		changeOf(e, "factors") != "passkey" || changeOf(e, "factorCount") != "2" {
		t.Errorf("result = %+v, entry = %+v, want a passkey sign-in with two factors", res, e)
	}

	if len(w.signInInputs) != 0 {
		t.Errorf("the passkey branch searched for a principal: %+v", w.signInInputs)
	}
}

// checkLocalPasskeyStart fails unless got is the session of prn-1 with the
// generation 9 and no alert.
func checkLocalPasskeyStart(t *testing.T, got *session.Start) {
	t.Helper()

	if got.Generation != 9 || got.PrincipalID != "prn-1" || got.Provider != session.LocalProviderID ||
		got.Alert != nil || got.Stored {
		t.Errorf("start = %+v, want the local session of prn-1 with generation 9 and no alert", *got)
	}
}

// TestCompletePasskeyRefuses sends hostile input straight to the passkey
// branch, as if the layers before it had failed. Each case must refuse with an
// audit entry that holds the real reason, and must start no session.
func TestCompletePasskeyRefuses(t *testing.T) {
	t.Parallel()

	disabled := localOwner()
	disabled.DisabledAt = testNow().Add(-time.Hour)

	namedOther := localOwner()
	namedOther.ID = "prn-2"

	oidcPerson := localOwner()
	oidcPerson.Subject = "corp:sub-1"

	notPerson := localOwner()
	notPerson.Kind = "service"

	cases := []struct {
		name   string
		change func(r *signinsvc.PasskeyRequest)
		owner  session.Owner
		stErr  error
		reason string
	}{
		{
			"no user verification", func(r *signinsvc.PasskeyRequest) { r.UserVerified = false }, localOwner(), nil,
			signinsvc.ReasonPasskeyNotVerified,
		},
		{
			"assertion of another person", func(r *signinsvc.PasskeyRequest) { r.Assertion.PrincipalID = "prn-2" },
			localOwner(), nil, signinsvc.ReasonPasskeyPerson,
		},
		{
			"subject of another person", func(r *signinsvc.PasskeyRequest) { r.Assertion.Subject = "prn-2" },
			localOwner(), nil, signinsvc.ReasonPasskeyPerson,
		},
		{
			"no person", func(r *signinsvc.PasskeyRequest) { r.PrincipalID = "" }, localOwner(), nil,
			signinsvc.ReasonPasskeyPerson,
		},
		{"method of the password", func(r *signinsvc.PasskeyRequest) {
			r.Assertion.Method = session.MethodLocalPassword
			r.Assertion.Factors = []session.Factor{session.FactorPassword}
		}, localOwner(), nil, signinsvc.ReasonMethodMismatch},
		{
			"provider of an identity provider", func(r *signinsvc.PasskeyRequest) { r.Assertion.ProviderID = "corp" },
			localOwner(), nil, signinsvc.ReasonAssertion,
		},
		{
			"no passkey factor", func(r *signinsvc.PasskeyRequest) { r.Assertion.Factors = nil }, localOwner(), nil,
			signinsvc.ReasonAssertion,
		},
		{"disabled person", func(*signinsvc.PasskeyRequest) {}, disabled, nil, signinsvc.ReasonDisabled},
		{
			"state of another person", func(*signinsvc.PasskeyRequest) {}, namedOther, nil,
			signinsvc.ReasonPasskeyNotTarget,
		},
		{
			"person of an identity provider", func(*signinsvc.PasskeyRequest) {}, oidcPerson, nil,
			signinsvc.ReasonPasskeyNotTarget,
		},
		{
			"principal that is no person", func(*signinsvc.PasskeyRequest) {}, notPerson, nil,
			signinsvc.ReasonPasskeyNotTarget,
		},
		{
			"principal gone", func(*signinsvc.PasskeyRequest) {}, localOwner(), session.ErrPrincipalNotFound,
			signinsvc.ReasonLocalPrincipalGone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			st := &stateWorld{owner: tc.owner, gen: 1, err: tc.stErr}

			req := passkeyRequest()
			tc.change(req)

			_, err := passkeyService(t, w, st).CompletePasskey(t.Context(), req)
			if !signinsvc.IsRefusal(err) {
				t.Fatalf("CompletePasskey() = %v, want a refusal", err)
			}

			if len(w.starts) != 0 {
				t.Errorf("sessions = %d, want 0", len(w.starts))
			}

			if len(w.entries) != 1 || reasonOf(&w.entries[0]) != tc.reason {
				t.Errorf("entries = %+v, want one entry with the reason %q", w.entries, tc.reason)
			}
		})
	}
}

// TestCompletePasskeyFailsClosedWithoutStates checks that a service with no
// reader of the state signs nobody in.
func TestCompletePasskeyFailsClosedWithoutStates(t *testing.T) {
	t.Parallel()

	w := newWorld()
	svc := w.service(t)

	if _, err := svc.CompletePasskey(t.Context(), passkeyRequest()); !signinsvc.IsRefusal(err) {
		t.Fatalf("CompletePasskey() = %v, want a refusal", err)
	}

	if len(w.starts) != 0 || len(w.entries) != 1 || reasonOf(&w.entries[0]) != signinsvc.ReasonPasskeyNoState {
		t.Errorf("sessions = %d, entries = %+v, want no session and the reason of the missing reader",
			len(w.starts), w.entries)
	}
}

// TestCompletePasskeyChangedGeneration checks that a generation that changed
// after the read of the state refuses the sign-in.
func TestCompletePasskeyChangedGeneration(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.startErr = session.ErrGenerationChanged

	_, err := passkeyService(t, w, stateOf(localOwner(), 3)).CompletePasskey(
		t.Context(), passkeyRequest(),
	)
	if !signinsvc.IsRefusal(err) {
		t.Fatalf("CompletePasskey() = %v, want a refusal", err)
	}

	if len(w.entries) != 1 || reasonOf(&w.entries[0]) != signinsvc.ReasonGenerationChanged {
		t.Errorf("entries = %+v, want the reason of the changed generation", w.entries)
	}
}

// breakGlassOwner is a break-glass account.
func breakGlassOwner() session.Owner {
	o := localOwner()
	o.Subject = "break-glass:ana"
	o.BreakGlass = true

	return o
}

// TestCompletePasskeyBreakGlassRaisesTheAlert checks that the sign-in of a
// break-glass account carries the alert of its use to the administrators in
// the start of the session, with the reason that the person typed.
func TestCompletePasskeyBreakGlassRaisesTheAlert(t *testing.T) {
	t.Parallel()

	w := newWorld()
	req := passkeyRequest()
	req.Reason = "  the identity provider is down  "

	res, err := passkeyService(t, w, stateOf(breakGlassOwner(), 4)).CompletePasskey(t.Context(), req)
	if err != nil {
		t.Fatalf("CompletePasskey() = %v", err)
	}

	if !res.BreakGlass {
		t.Error("the result of a break-glass sign-in does not say so")
	}

	if len(w.starts) != 1 || w.starts[0].Alert == nil {
		t.Fatalf("starts = %+v, want one session with the alert of a break-glass use", w.starts)
	}

	checkBreakGlassStart(t, w.starts[0].Alert, &w.startEntries[0])
}

// checkBreakGlassStart fails unless n is the critical alert of a break-glass
// use for the administrators, and e is the entry of the session with the
// trimmed reason and the break-glass mark.
func checkBreakGlassStart(t *testing.T, n *notify.Notification, e *audit.Entry) {
	t.Helper()

	if n.Type != notify.BreakGlassUsed || n.Scope != audit.ServerScope || n.Severity != notify.SeverityCritical ||
		len(n.Recipients) != 1 || !n.Recipients[0].Admins {
		t.Errorf("alert = %+v, want critical break-glass.used for the administrators in the server scope", n)
	}

	if e.Reason != "the identity provider is down" || changeOf(e, "breakGlass") != "true" ||
		e.Subject != "break-glass:ana" {
		t.Errorf("entry = %+v, want the trimmed reason and the break-glass mark", e)
	}
}

// reasonWith returns "down", then the rune r, then "now". It builds the
// hidden characters from their code points, so that no source line holds one.
func reasonWith(r rune) string {
	return "down" + string(r) + "now"
}

// TestCompletePasskeyBreakGlassNeedsAReason checks that a break-glass sign-in
// with no reason, or with a reason that is not valid text, starts no session
// and raises no alert. The invalid text holds a control character, a character
// that the audit log hides (format, line separator and paragraph separator
// characters), or more than the most bytes.
func TestCompletePasskeyBreakGlassNeedsAReason(t *testing.T) {
	t.Parallel()

	long := make([]byte, signinsvc.MaxBreakGlassReason+1)
	for i := range long {
		long[i] = 'a'
	}

	cases := []struct {
		name   string
		reason string
		want   string
	}{
		{"empty", "", signinsvc.ReasonPasskeyNoReason},
		{"spaces", "   ", signinsvc.ReasonPasskeyNoReason},
		{"control character", "down\x00now", signinsvc.ReasonPasskeyReasonText},
		{"newline", "down\nnow", signinsvc.ReasonPasskeyReasonText},
		{"too long", string(long), signinsvc.ReasonPasskeyReasonText},
		{"right-to-left override", reasonWith(0x202e), signinsvc.ReasonPasskeyReasonText},
		{"zero-width space", reasonWith(0x200b), signinsvc.ReasonPasskeyReasonText},
		{"left-to-right isolate", reasonWith(0x2066), signinsvc.ReasonPasskeyReasonText},
		{"pop directional isolate", reasonWith(0x2069), signinsvc.ReasonPasskeyReasonText},
		{"line separator", reasonWith(0x2028), signinsvc.ReasonPasskeyReasonText},
		{"paragraph separator", reasonWith(0x2029), signinsvc.ReasonPasskeyReasonText},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			req := passkeyRequest()
			req.Reason = tc.reason

			_, err := passkeyService(t, w, stateOf(breakGlassOwner(), 1)).CompletePasskey(
				t.Context(), req,
			)
			if !signinsvc.IsRefusal(err) || !errors.Is(err, signinsvc.ErrReasonRequired) {
				t.Fatalf("CompletePasskey() = %v, want a refusal that names the missing reason", err)
			}

			if len(w.starts) != 0 || len(w.entries) != 1 || reasonOf(&w.entries[0]) != tc.want {
				t.Errorf("sessions = %d, entries = %+v, want no session and the reason %q",
					len(w.starts), w.entries, tc.want)
			}
		})
	}
}

// TestCompletePasskeyLocalUserNeedsNoReason checks that the reason is for
// break-glass accounts only: a local user ignores it and raises no alert.
func TestCompletePasskeyLocalUserNeedsNoReason(t *testing.T) {
	t.Parallel()

	w := newWorld()
	req := passkeyRequest()
	req.Reason = "typed anyway"

	_, err := passkeyService(t, w, stateOf(localOwner(), 1)).CompletePasskey(t.Context(), req)
	if err != nil {
		t.Fatalf("CompletePasskey() = %v", err)
	}

	if w.starts[0].Alert != nil || w.startEntries[0].Reason != "" {
		t.Errorf("start = %+v, entry = %+v, want no alert and no reason", w.starts[0], w.startEntries[0])
	}
}
