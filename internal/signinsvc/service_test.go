package signinsvc_test

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// refusalOf returns the RefusedError in err, or fails the test.
func refusalOf(t *testing.T, err error) *signinsvc.RefusedError {
	t.Helper()

	r, ok := errors.AsType[*signinsvc.RefusedError](err)
	if !ok {
		t.Fatalf("error = %v, want a *RefusedError", err)
	}

	return r
}

// checkRefusedEntry fails unless w holds exactly one audit entry with the
// reason, the provider ID and the kind of the method, and the session never
// started.
func checkRefusedEntry(t *testing.T, w *world, reason, providerID string, kind session.MethodKind) {
	t.Helper()

	if len(w.starts) != 0 {
		t.Fatalf("the refused sign-in started %d sessions", len(w.starts))
	}

	if len(w.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(w.entries))
	}

	e := &w.entries[0]
	if got := reasonOf(e); got != reason {
		t.Errorf("audit reason = %q, want %q", got, reason)
	}

	if got := changeOf(e, "provider"); got != providerID {
		t.Errorf("audit provider = %q, want %q", got, providerID)
	}

	if got := changeOf(e, "method"); got != string(kind) {
		t.Errorf("audit method = %q, want %q", got, kind)
	}

	if e.Outcome == audit.OutcomeSuccess || e.Action != session.ActionSignIn {
		t.Errorf("audit entry = %s %s, want a refusal of the sign-in", e.Action, e.Outcome)
	}
}

// checkRefusalCause fails unless the refusal err holds the cause error, or
// the cause is nil. The cause reaches the caller in the refusal: the audit
// entry holds the reason alone.
func checkRefusalCause(t *testing.T, err, cause error) {
	t.Helper()

	if cause == nil {
		return
	}

	if !errors.Is(err, cause) || !strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("refusal %v does not hold the cause %v", err, cause)
	}
}

// assertionOfKind returns a valid Assertion of the provider corp for the
// method kind.
func assertionOfKind(kind session.MethodKind) session.Assertion {
	a := oidcAssertion()
	a.Method = kind

	if kind == session.MethodLDAP {
		a.Factors = []session.Factor{session.FactorPassword}
	}

	if kind == session.MethodOAuth {
		a.AuthTime, a.AuthTimeKnown = time.Time{}, false
	}

	return a
}

// TestCompleteStartsTheSession checks the good sign-in of each method that
// has a principal source: the source comes from the kind, the session names
// the provider, the entry holds the kind and the factors.
func TestCompleteStartsTheSession(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		kind   session.MethodKind
		source principal.Source
	}{
		{"openid connect", session.MethodOIDC, principal.SourceOIDC},
		{"plain oauth", session.MethodOAuth, principal.SourceOAuth},
		{"ldap", session.MethodLDAP, principal.SourceLDAP},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			p := oidcProvider()
			p.Kind = tc.kind

			res, err := w.service(t).Complete(t.Context(), requestOf(p, assertionOfKind(tc.kind), nil))
			if err != nil {
				t.Fatalf("Complete() = %v", err)
			}

			if res.Method != tc.kind || len(w.signInInputs) != 1 || w.signInInputs[0].Source != tc.source ||
				w.signInInputs[0].Subject != "corp:sub-1" || !w.signInInputs[0].Create {
				t.Fatalf("result %+v, sign-in inputs %+v, want the kind %s with the source %s",
					res, w.signInInputs, tc.kind, tc.source)
			}

			if len(w.starts) != 1 || w.starts[0].Provider != "corp" || w.starts[0].PrincipalID != "prn-1" {
				t.Fatalf("starts = %+v, want one session of prn-1 at corp", w.starts)
			}

			if got := changeOf(&w.startEntries[0], "method"); got != string(tc.kind) {
				t.Errorf("session entry method = %q, want %q", got, tc.kind)
			}
		})
	}
}

// TestSourceOfFailsClosed checks that an unknown, an empty and a local kind
// have no source, and that no kind defaults to oidc.
func TestSourceOfFailsClosed(t *testing.T) {
	t.Parallel()

	for _, kind := range []session.MethodKind{
		"", "unknown", "OIDC", "oidc ", session.MethodLocalPassword, session.MethodPasskey,
		session.MethodSAML, session.MethodCAS, session.MethodHeader,
	} {
		if src, ok := signinsvc.SourceOf(kind); ok || src != "" {
			t.Errorf("SourceOf(%q) = %q, %v, want no source", kind, src, ok)
		}
	}
}

// hostileCase is one hostile input of the route: a change of the provider
// of the route and of the Assertion, and the refusal that must follow.
type hostileCase struct {
	change func(p *signinsvc.Provider, a *session.Assertion)
	name   string
	reason string
	answer signinsvc.Answer
}

// hostileRouteCases returns the cases of TestCompleteRefusesAHostileRoute.
func hostileRouteCases() []hostileCase {
	failed := signinsvc.AnswerFailed

	return []hostileCase{
		{
			func(_ *signinsvc.Provider, a *session.Assertion) { a.ProviderID = "other" },
			"provider mismatch", signinsvc.ReasonProviderMismatch, failed,
		},
		{func(_ *signinsvc.Provider, a *session.Assertion) {
			a.Method, a.AuthTimeKnown, a.AuthTime = session.MethodOAuth, false, time.Time{}
		}, "kind mismatch", signinsvc.ReasonMethodMismatch, failed},
		{
			func(p *signinsvc.Provider, _ *session.Assertion) { p.Kind = "" },
			"empty provider kind", signinsvc.ReasonProviderKind, failed,
		},
		{
			func(p *signinsvc.Provider, _ *session.Assertion) { p.Kind = "unknown" },
			"unknown provider kind", signinsvc.ReasonProviderKind, failed,
		},
		{
			func(p *signinsvc.Provider, _ *session.Assertion) { p.Kind = session.MethodLocalPassword },
			"local kind on a method route", signinsvc.ReasonProviderKind, failed,
		},
		{
			func(p *signinsvc.Provider, _ *session.Assertion) { p.ID = session.LocalProviderID },
			"local provider on a method route", signinsvc.ReasonProviderKind, failed,
		},
		{
			func(p *signinsvc.Provider, _ *session.Assertion) { p.ID = "" },
			"empty provider ID", signinsvc.ReasonProviderKind, failed,
		},
		{
			func(_ *signinsvc.Provider, a *session.Assertion) { a.Method = "" },
			"empty assertion kind", signinsvc.ReasonAssertion, failed,
		},
		{
			func(_ *signinsvc.Provider, a *session.Assertion) { a.Method = "unknown" },
			"unknown assertion kind", signinsvc.ReasonAssertion, failed,
		},
		{
			func(_ *signinsvc.Provider, a *session.Assertion) { a.Subject = "" },
			"empty subject", signinsvc.ReasonAssertion, failed,
		},
		{
			func(_ *signinsvc.Provider, a *session.Assertion) { a.Subject = "a\x00b" },
			"subject with a control character", signinsvc.ReasonAssertion, failed,
		},
		{
			func(_ *signinsvc.Provider, a *session.Assertion) { a.PrincipalID = "prn-other" },
			"method names a principal", signinsvc.ReasonAssertion, failed,
		},
		{
			func(_ *signinsvc.Provider, a *session.Assertion) { a.Groups = []string{"admins"} },
			"groups without GroupsPresent", signinsvc.ReasonAssertion, failed,
		},
		{
			func(_ *signinsvc.Provider, a *session.Assertion) { a.AuthTime = testNow().AddDate(0, 0, 1) },
			"authentication time in the future", signinsvc.ReasonAssertion, failed,
		},
		{
			func(p *signinsvc.Provider, a *session.Assertion) {
				p.Kind, a.Method = session.MethodSAML, session.MethodSAML
			},
			"method with no source", signinsvc.ReasonNoSource, failed,
		},
		{
			func(_ *signinsvc.Provider, a *session.Assertion) { a.Subject = strings.Repeat("a", 252) },
			"composed subject over the limit", signinsvc.ReasonSubjectLength, signinsvc.AnswerRefused,
		},
	}
}

// TestCompleteRefusesAHostileRoute sends an Assertion that does not belong to
// the route straight to the service, as if the method and the adapter had
// passed it. Each case must refuse, change no principal and start no session.
func TestCompleteRefusesAHostileRoute(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileRouteCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			p, a := oidcProvider(), oidcAssertion()
			tc.change(&p, &a)

			_, err := w.service(t).Complete(t.Context(), requestOf(p, a, nil))
			r := refusalOf(t, err)

			if r.Reason != tc.reason || r.Answer != tc.answer {
				t.Fatalf("refusal = %q %d, want %q %d", r.Reason, r.Answer, tc.reason, tc.answer)
			}

			if len(w.signInInputs) != 0 || len(w.disabled) != 0 {
				t.Fatalf("the refusal reached the principal service: %+v, disabled %v", w.signInInputs, w.disabled)
			}

			checkRefusedEntry(t, w, tc.reason, p.ID, p.Kind)
		})
	}
}

// TestCompleteRefusesAPrincipal checks each answer of the principal service
// that refuses a sign-in, and that the audit entry names the subject at the
// provider only after the checks of the Assertion passed.
func TestCompleteRefusesAPrincipal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err    error
		name   string
		reason string
	}{
		{principal.ErrNotFound, "no principal", signinsvc.ReasonNoPrincipal},
		{principal.ErrDisabled, "disabled principal", signinsvc.ReasonDisabled},
		{principal.ErrNotSignInTarget, "not a target", signinsvc.ReasonNotTarget},
		{principal.ErrInvalid, "invalid subject", signinsvc.ReasonSubject},
		{principal.ErrReservedSubject, "reserved subject", signinsvc.ReasonSubject},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			w.principalErr = fmt.Errorf("find: %w", tc.err)

			_, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), oidcAssertion(), nil))
			r := refusalOf(t, err)

			if r.Reason != tc.reason || r.Answer != signinsvc.AnswerRefused {
				t.Fatalf("refusal = %q %d, want %q refused", r.Reason, r.Answer, tc.reason)
			}

			checkRefusedEntry(t, w, tc.reason, "corp", session.MethodOIDC)

			if e := &w.entries[0]; e.Outcome != audit.OutcomeDenied || e.IDPSubject != "sub-1" {
				t.Errorf("entry = %s with sub %q, want denied with the sub value", e.Outcome, e.IDPSubject)
			}
		})
	}
}

// TestCompleteStoreErrorIsNotARefusal checks that a failing store gives an
// error that is not a refusal, and starts no session.
func TestCompleteStoreErrorIsNotARefusal(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.principalErr = errStoreStopped

	_, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), oidcAssertion(), nil))
	if !errors.Is(err, errStoreStopped) {
		t.Fatalf("Complete() = %v, want the store error", err)
	}

	if signinsvc.IsRefusal(err) || len(w.starts) != 0 {
		t.Fatalf("error = %v with %d sessions, want an error that is not a refusal and no session", err, len(w.starts))
	}
}

// TestCompleteMethodErrorReasons checks the audit reason of each error class
// of a method, with the provider ID and the method kind, and that "ID token
// was refused" belongs to the ID token error of OpenID Connect alone.
func TestCompleteMethodErrorReasons(t *testing.T) {
	t.Parallel()

	idToken := fmt.Errorf("%w: %w", session.ErrMethodAnswer, oauth.ErrIDToken)

	cases := []struct {
		err    error
		name   string
		kind   session.MethodKind
		reason string
	}{
		{session.ErrReplay, "replay", session.MethodOIDC, signinsvc.ReasonReplay},
		{session.ErrRequestBinding, "request binding", session.MethodOIDC, signinsvc.ReasonBinding},
		{session.ErrMethodUnavailable, "unavailable", session.MethodOIDC, signinsvc.ReasonUnavailable},
		{session.ErrProviderDisabled, "provider off", session.MethodOIDC, signinsvc.ReasonProviderOff},
		{session.ErrMethodRejected, "provider error", session.MethodOIDC, signinsvc.ReasonProviderError},
		{session.ErrMethodRejected, "directory rejected", session.MethodLDAP, signinsvc.ReasonDirectoryRejected},
		{idToken, "id token of openid connect", session.MethodOIDC, signinsvc.ReasonOpenIDAnswer},
		{idToken, "id token error on plain oauth", session.MethodOAuth, signinsvc.ReasonAnswer},
		{session.ErrMethodAnswer, "answer of openid connect", session.MethodOIDC, signinsvc.ReasonAnswer},
		{session.ErrMethodAnswer, "answer of plain oauth", session.MethodOAuth, signinsvc.ReasonAnswer},
		{session.ErrInvalidAssertion, "invalid assertion", session.MethodOIDC, signinsvc.ReasonAssertion},
		{session.ErrMethodInput, "input", session.MethodOIDC, signinsvc.ReasonInput},
		{session.ErrUntrustedSource, "untrusted source", session.MethodHeader, signinsvc.ReasonUntrustedSource},
		{session.ErrNoAssertion, "no assertion", session.MethodHeader, signinsvc.ReasonNoAssertion},
		{session.ErrGroupsMissing, "groups missing", session.MethodLDAP, signinsvc.ReasonGroupsMissing},
		{errDriver, "unknown class", session.MethodOIDC, signinsvc.ReasonMethodFailed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			p := oidcProvider()
			p.Kind = tc.kind

			var none session.Assertion

			_, err := w.service(t).Complete(t.Context(), requestOf(p, none, fmt.Errorf("method: %w", tc.err)))
			r := refusalOf(t, err)

			if r.Reason != tc.reason || r.Answer != signinsvc.AnswerFailed {
				t.Fatalf("refusal = %q %d, want %q failed", r.Reason, r.Answer, tc.reason)
			}

			checkRefusedEntry(t, w, tc.reason, "corp", tc.kind)

			if tc.reason == signinsvc.ReasonOpenIDAnswer && tc.kind != session.MethodOIDC {
				t.Fatal("ID token was refused for a method that is not OpenID Connect")
			}

			if len(w.signInInputs) != 0 {
				t.Fatalf("a method error reached the principal service: %+v", w.signInInputs)
			}
		})
	}
}

// disabledCase is one input of a person that the provider reports as
// disabled.
type disabledCase struct {
	change       func(w *world, a *session.Assertion)
	name         string
	reason       string
	wantDisabled bool
	wantLookup   bool
}

// disabledCases returns the cases of TestCompleteSubjectDisabled.
func disabledCases() []disabledCase {
	return []disabledCase{
		{func(*world, *session.Assertion) {}, "principal found", signinsvc.ReasonSubjectDisabled, true, true},
		{
			func(w *world, _ *session.Assertion) { w.principalErr = principal.ErrNotFound },
			"no principal", signinsvc.ReasonDisableNoPrincipal, false, true,
		},
		{
			func(w *world, _ *session.Assertion) { w.principalErr = principal.ErrNotSignInTarget },
			"break-glass account", signinsvc.ReasonDisableNotTarget, false, true,
		},
		{
			func(w *world, _ *session.Assertion) { w.principalErr = principal.ErrDisabled },
			"already disabled", signinsvc.ReasonSubjectDisabled, false, true,
		},
		{
			func(_ *world, a *session.Assertion) { a.ProviderID = "other" },
			"provider mismatch", signinsvc.ReasonProviderMismatch, false, false,
		},
		{
			func(_ *world, a *session.Assertion) { a.Method = session.MethodOAuth },
			"kind mismatch", signinsvc.ReasonMethodMismatch, false, false,
		},
		{
			func(_ *world, a *session.Assertion) { a.Subject = "" },
			"empty subject", signinsvc.ReasonAssertion, false, false,
		},
		{
			func(_ *world, a *session.Assertion) { a.Subject = "a\nb" },
			"subject with a control character", signinsvc.ReasonAssertion, false, false,
		},
		{
			func(_ *world, a *session.Assertion) { a.PrincipalID = "prn-1" },
			"assertion names a principal", signinsvc.ReasonAssertion, false, false,
		},
		{
			func(_ *world, a *session.Assertion) { a.Subject = strings.Repeat("a", 252) },
			"composed subject over the limit", signinsvc.ReasonSubjectLength, false, false,
		},
	}
}

// TestCompleteSubjectDisabled checks the steps for a person that the
// provider reports as disabled: a principal that exists is disabled and its
// sessions end; no principal is ever made; a principal that the provider
// cannot manage is never disabled; a hostile Assertion disables nothing.
func TestCompleteSubjectDisabled(t *testing.T) {
	t.Parallel()

	disabledErr := fmt.Errorf("method: %w", session.ErrSubjectDisabled)

	for _, tc := range disabledCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			a := disabledAssertion()
			tc.change(w, &a)

			_, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), a, disabledErr))
			r := refusalOf(t, err)

			if r.Reason != tc.reason || r.Answer != signinsvc.AnswerFailed {
				t.Fatalf("refusal = %q %d, want %q failed", r.Reason, r.Answer, tc.reason)
			}

			checkDisableCalls(t, w, &tc)
			checkRefusedEntry(t, w, tc.reason, "corp", session.MethodOIDC)
		})
	}
}

// checkDisableCalls fails unless w saw the lookups and the disable that tc
// wants, and no lookup that makes a principal.
func checkDisableCalls(t *testing.T, w *world, tc *disabledCase) {
	t.Helper()

	if got := len(w.signInInputs) == 1; got != tc.wantLookup {
		t.Fatalf("lookups = %d, want a lookup: %v", len(w.signInInputs), tc.wantLookup)
	}

	if tc.wantLookup && w.signInInputs[0].Create {
		t.Fatal("the disable path asked to make a principal")
	}

	if got := len(w.disabled) == 1 && len(w.bumped) == 1; got != tc.wantDisabled {
		t.Fatalf("disabled %v, bumped %v, want a disable: %v", w.disabled, w.bumped, tc.wantDisabled)
	}
}

// TestCompleteDisableFailure checks that a failed disable returns the store
// error, writes the refusal, and starts no session.
func TestCompleteDisableFailure(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.disableErr = errStoreStopped

	_, err := w.service(t).Complete(t.Context(),
		requestOf(oidcProvider(), disabledAssertion(), session.ErrSubjectDisabled))
	if !errors.Is(err, errStoreStopped) || signinsvc.IsRefusal(err) {
		t.Fatalf("Complete() = %v, want the error of the store", err)
	}

	checkRefusedEntry(t, w, signinsvc.ReasonDisableFailed, "corp", session.MethodOIDC)
}

// TestCompleteStartRefusals checks the refusal for each error of the start of
// the session that is about the principal.
func TestCompleteStartRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err    error
		name   string
		reason string
	}{
		{session.ErrPrincipalDisabled, "principal disabled after the lookup", signinsvc.ReasonDisabled},
		{session.ErrPrincipalNotFound, "principal gone after the lookup", signinsvc.ReasonNotTarget},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			w.startErr = tc.err

			_, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), oidcAssertion(), nil))
			if r := refusalOf(t, err); r.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", r.Reason, tc.reason)
			}
		})
	}
}

// TestCompleteProviderGone checks that a provider that went off between the
// lookup and the start gives the error for the adapter, not a refusal.
func TestCompleteProviderGone(t *testing.T) {
	t.Parallel()

	for _, startErr := range []error{session.ErrProviderDisabled, session.ErrProviderNotFound} {
		w := newWorld()
		w.startErr = startErr

		_, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), oidcAssertion(), nil))
		if !errors.Is(err, startErr) || signinsvc.IsRefusal(err) {
			t.Fatalf("Complete() = %v, want %v for the adapter and no refusal", err, startErr)
		}
	}
}

// groupsAssertion returns a valid Assertion that carries one group.
func groupsAssertion() session.Assertion {
	a := oidcAssertion()
	a.GroupsPresent, a.Groups = true, []string{"admins"}

	return a
}

// TestCompleteGroupsWithoutApplier checks that with no applier no membership
// changes and the entry of the session says so.
func TestCompleteGroupsWithoutApplier(t *testing.T) {
	t.Parallel()

	w := newWorld()
	if _, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), groupsAssertion(), nil)); err != nil {
		t.Fatalf("Complete() = %v", err)
	}

	if note := changeOf(&w.startEntries[0], "groups"); len(w.claims) != 0 || note == "" {
		t.Fatalf("claims %+v, note %q, want no claim and a note", w.claims, note)
	}
}

// TestCompleteGroupsWithApplier checks that the claims name the source, the
// provider and the principal.
func TestCompleteGroupsWithApplier(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.withGroupsPort = true

	if _, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), groupsAssertion(), nil)); err != nil {
		t.Fatalf("Complete() = %v", err)
	}

	if len(w.claims) != 1 || w.claims[0].SourceID != "corp" || w.claims[0].PrincipalID != "prn-1" ||
		w.claims[0].Source != principal.MembershipSourceClaim || !w.claims[0].Present {
		t.Fatalf("claims = %+v, want the claims of corp for prn-1", w.claims)
	}
}

// TestCompleteGroupsError checks that an error of the applier starts no
// session.
func TestCompleteGroupsError(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.withGroupsPort, w.groupsErr = true, errStoreStopped

	_, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), groupsAssertion(), nil))
	if !errors.Is(err, errStoreStopped) || len(w.starts) != 0 {
		t.Fatalf("Complete() = %v with %d sessions, want the error and no session", err, len(w.starts))
	}
}

// TestCompleteNoGroupsInTheAssertion checks that an Assertion with no groups
// calls no applier.
func TestCompleteNoGroupsInTheAssertion(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.withGroupsPort = true

	if _, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), oidcAssertion(), nil)); err != nil {
		t.Fatalf("Complete() = %v", err)
	}

	if len(w.claims) != 0 {
		t.Fatalf("claims = %+v, want none", w.claims)
	}
}

// TestCompleteGroupsRecordsTheRemovals checks that a sign-in whose claims
// remove and suspend members records the IDs of both with the source of the
// provider.
func TestCompleteGroupsRecordsTheRemovals(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.withGroupsPort, w.withRemovals = true, true
	w.groupsResult = principal.GroupClaimResult{
		Added: nil, Removed: []string{"grp-a"}, Suspended: []string{"grp-b"},
		Refused: nil, Unmapped: nil, NoMapping: false,
	}

	if _, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), groupsAssertion(), nil)); err != nil {
		t.Fatalf("Complete() = %v", err)
	}

	if len(w.removals) != 1 || w.removals[0].Kind != principal.RemovalSourceIdentityProvider ||
		w.removals[0].ID != "corp" {
		t.Fatalf("removals = %+v, want the removals of the provider corp", w.removals)
	}

	if len(w.removedIDs) != 1 || !slices.Equal(w.removedIDs[0], []string{"grp-a", "grp-b"}) {
		t.Fatalf("groups = %v, want grp-a and grp-b", w.removedIDs)
	}
}

// TestCompleteGroupsNoRemovalRecordsNothing checks that a sign-in that
// removes no member calls no recorder.
func TestCompleteGroupsNoRemovalRecordsNothing(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.withGroupsPort, w.withRemovals = true, true
	w.groupsResult = principal.GroupClaimResult{
		Added: []string{"grp-a"}, Removed: nil, Suspended: nil,
		Refused: nil, Unmapped: nil, NoMapping: false,
	}

	if _, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), groupsAssertion(), nil)); err != nil {
		t.Fatalf("Complete() = %v", err)
	}

	if len(w.removals) != 0 {
		t.Fatalf("removals = %+v, want none", w.removals)
	}
}

// TestCompleteGroupsRecordErrorGoesOn checks that a failure to record the
// removals does not refuse the sign-in: the service writes one ERROR line and
// one audit entry of the failed alert, then starts the session.
func TestCompleteGroupsRecordErrorGoesOn(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.withGroupsPort, w.withRemovals, w.removalsErr = true, true, errStoreStopped
	w.groupsResult = principal.GroupClaimResult{
		Added: nil, Removed: []string{"grp-a"}, Suspended: nil,
		Refused: nil, Unmapped: nil, NoMapping: false,
	}

	if _, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), groupsAssertion(), nil)); err != nil {
		t.Fatalf("Complete() = %v, want the sign-in to go on", err)
	}

	if len(w.starts) != 1 {
		t.Fatalf("sessions = %d, want 1", len(w.starts))
	}

	if len(w.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(w.entries))
	}

	checkRemovalAlertFailureEntry(t, w.entries[0])

	if !strings.Contains(w.logs.text(), errcode.PrincipalRemovalAlertFailed) ||
		!strings.Contains(w.logs.text(), "identity-provider") {
		t.Fatalf("log = %q, want the code and the source kind", w.logs.text())
	}

	if w.auditFailed != 0 {
		t.Fatalf("audit failures = %d, want 0", w.auditFailed)
	}
}

// checkRemovalAlertFailureEntry fails unless e is the audit entry of the
// failed removal alert of the provider corp.
func checkRemovalAlertFailureEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Action != principal.AuditActionMassRemovalAlert || e.Outcome != audit.OutcomeFailure ||
		e.Kind != audit.KindSecurity || e.Namespace != audit.ServerScope ||
		e.Subject != "identity-provider:corp" || e.Reason != errcode.PrincipalRemovalAlertFailed {
		t.Fatalf("audit entry = %+v, want the failure of the removal alert", e)
	}
}

// TestCompleteGroupsRecordAndAuditErrorsRefuseTheSignIn checks that the
// sign-in is refused when the audit entry of the failed alert fails too.
func TestCompleteGroupsRecordAndAuditErrorsRefuseTheSignIn(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.withGroupsPort, w.withRemovals, w.removalsErr = true, true, errStoreStopped
	w.auditErr = errStoreStopped
	w.groupsResult = principal.GroupClaimResult{
		Added: nil, Removed: []string{"grp-a"}, Suspended: nil,
		Refused: nil, Unmapped: nil, NoMapping: false,
	}

	_, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), groupsAssertion(), nil))
	if !errors.Is(err, signinsvc.ErrRemovalAlertUnrecorded) || !errors.Is(err, errStoreStopped) {
		t.Fatalf("Complete() = %v, want the unrecorded alert and the audit error", err)
	}

	if len(w.starts) != 0 {
		t.Fatalf("sessions = %d, want 0", len(w.starts))
	}

	if w.auditFailed != 1 {
		t.Fatalf("audit failures = %d, want 1", w.auditFailed)
	}
}

// TestRefusalSurvivesAnAuditFailure checks that a failed audit write still
// refuses the sign-in and reports the failure.
func TestRefusalSurvivesAnAuditFailure(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.auditErr = errStoreStopped

	var none session.Assertion

	_, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), none, session.ErrReplay))
	if r := refusalOf(t, err); r.Reason != signinsvc.ReasonReplay {
		t.Fatalf("reason = %q, want %q", r.Reason, signinsvc.ReasonReplay)
	}

	if w.auditFailed != 1 || len(w.starts) != 0 {
		t.Fatalf("audit failures %d, sessions %d, want 1 and 0", w.auditFailed, len(w.starts))
	}
}

// TestNewRefusesAMissingPort checks that each port is required.
func TestNewRefusesAMissingPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		clear func(cfg *signinsvc.Config)
	}{
		{"principals", func(cfg *signinsvc.Config) { cfg.Principals = nil }},
		{"disabler", func(cfg *signinsvc.Config) { cfg.Disabler = nil }},
		{"generations", func(cfg *signinsvc.Config) { cfg.Generations = nil }},
		{"sessions", func(cfg *signinsvc.Config) { cfg.Sessions = nil }},
		{"audit log", func(cfg *signinsvc.Config) { cfg.Audit = nil }},
		{"temporary administrators", func(cfg *signinsvc.Config) { cfg.TempAdmins = nil }},
		{"logger", func(cfg *signinsvc.Config) { cfg.Logger = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			cfg := signinsvc.Config{
				Principals:     w,
				Disabler:       w,
				Generations:    w,
				Sessions:       w,
				Audit:          w,
				TempAdmins:     w,
				Groups:         nil,
				Removals:       nil,
				States:         nil,
				Locks:          nil,
				NameKeys:       nil,
				Logger:         slog.New(slog.DiscardHandler),
				OnAuditFailure: nil,
			}
			tc.clear(&cfg)

			if _, err := signinsvc.New(cfg); !errors.Is(err, signinsvc.ErrMissingDependency) {
				t.Fatalf("New() = %v, want ErrMissingDependency", err)
			}
		})
	}
}

// lookupCase is one answer of a faulty lookup layer: a principal that the
// lookup returns with a nil error, or a failed read of the temporary
// administrators. cause is the error that the refusal must hold, or nil.
type lookupCase struct {
	change func(w *world)
	cause  error
	name   string
}

// hostileLookups returns the answers that the service must never sign in or
// disable for the subject corp:sub-1, one for each condition of the check.
func hostileLookups() []lookupCase {
	return []lookupCase{
		{func(w *world) { w.principal.Subject = "other:sub-9" }, nil, "other subject"},
		{func(w *world) { w.principal.Kind = principal.KindService }, nil, "not a person"},
		{func(w *world) { w.principal.Namespace = "team-a" }, nil, "principal of a namespace"},
		{func(w *world) { w.principal.BreakGlass = true }, nil, "break-glass account"},
		{func(w *world) { w.tempAdmin = true }, nil, "temporary administrator"},
		{
			func(w *world) { w.tempAdminErr = errStoreStopped }, errStoreStopped,
			"temporary administrator read failed",
		},
	}
}

// TestCompleteSubjectDisabledChecksTheLookup sends the answers that the
// lookup layer should never give, with a nil error, to the disable step. The
// step must disable and bump nothing, as if the lookup layer had failed. A
// failed read of the temporary administrators has its own reason, and the
// refusal holds the read error.
func TestCompleteSubjectDisabledChecksTheLookup(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileLookups() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			tc.change(w)

			reason := signinsvc.ReasonDisableNotTarget
			if tc.cause != nil {
				reason = signinsvc.ReasonTargetLookup
			}

			_, err := w.service(t).Complete(t.Context(),
				requestOf(oidcProvider(), disabledAssertion(), session.ErrSubjectDisabled))
			r := refusalOf(t, err)

			if r.Reason != reason {
				t.Fatalf("reason = %q, want %q", r.Reason, reason)
			}

			if !errors.Is(err, session.ErrSubjectDisabled) {
				t.Fatalf("the refusal holds no method error: %v", err)
			}

			checkRefusalCause(t, err, tc.cause)
			checkCauseLogged(t, w, tc.cause)

			if len(w.disabled) != 0 || len(w.bumped) != 0 {
				t.Fatalf("disabled %v, bumped %v, want none", w.disabled, w.bumped)
			}

			checkRefusedEntry(t, w, reason, "corp", session.MethodOIDC)
		})
	}
}

// TestCompleteChecksTheLookup sends the same answers to the success path.
// The service must refuse and start no session. A failed read of the
// temporary administrators has its own reason, and the refusal holds the
// read error.
func TestCompleteChecksTheLookup(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileLookups() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			tc.change(w)

			reason := signinsvc.ReasonNotTarget
			if tc.cause != nil {
				reason = signinsvc.ReasonTargetLookup
			}

			_, err := w.service(t).Complete(t.Context(), requestOf(oidcProvider(), oidcAssertion(), nil))
			r := refusalOf(t, err)

			if r.Reason != reason {
				t.Fatalf("reason = %q, want %q", r.Reason, reason)
			}

			checkRefusalCause(t, err, tc.cause)

			checkRefusedEntry(t, w, reason, "corp", session.MethodOIDC)

			// A failed read is a fault, not a decision about the person.
			answer, outcome := signinsvc.AnswerRefused, audit.OutcomeDenied
			if tc.cause != nil {
				answer, outcome = signinsvc.AnswerFailed, audit.OutcomeFailure
			}

			if r.Answer != answer || w.entries[0].Outcome != outcome {
				t.Fatalf("answer %v, outcome %q, want %v, %q", r.Answer, w.entries[0].Outcome, answer, outcome)
			}

			checkCauseLogged(t, w, tc.cause)
		})
	}
}

// checkCauseLogged fails unless the log of w holds the text of cause, or the
// cause is nil.
func checkCauseLogged(t *testing.T, w *world, cause error) {
	t.Helper()

	if cause != nil && !strings.Contains(w.logs.text(), cause.Error()) {
		t.Fatalf("the log does not name the cause %q: %s", cause, w.logs.text())
	}
}

// TestCompleteDisableLastAdministrator checks that a disable that the store
// refuses to keep the last administrator is a refusal with an audit entry.
func TestCompleteDisableLastAdministrator(t *testing.T) {
	t.Parallel()

	for _, storeErr := range []error{access.ErrLastSiteAdministrator, principal.ErrNoWorkingBreakGlass} {
		w := newWorld()
		w.disableErr = fmt.Errorf("disable: %w", storeErr)

		_, err := w.service(t).Complete(t.Context(),
			requestOf(oidcProvider(), disabledAssertion(), session.ErrSubjectDisabled))
		if r := refusalOf(t, err); r.Reason != signinsvc.ReasonDisableLastAdmin || r.Answer != signinsvc.AnswerFailed {
			t.Fatalf("refusal = %q %d, want %q failed", r.Reason, r.Answer, signinsvc.ReasonDisableLastAdmin)
		}

		checkRefusedEntry(t, w, signinsvc.ReasonDisableLastAdmin, "corp", session.MethodOIDC)
	}
}
