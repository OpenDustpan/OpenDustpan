package sessiontest

import (
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// CASAnswer is a form of the validation answer of the test CAS server.
type CASAnswer int

// Forms of a validation answer that the cases of the CAS protocol ask for.
const (
	// CASAnswerNoAuthenticationDate is a success with no attribute
	// authenticationDate.
	CASAnswerNoAuthenticationDate CASAnswer = iota + 1
	// CASAnswerDTD is a success with a document type declaration and an
	// entity.
	CASAnswerDTD
	// CASAnswerDeep is a success whose elements nest deeper than 64.
	CASAnswerDeep
	// CASAnswerSlow is a success that comes after the timeout of the
	// method.
	CASAnswerSlow
)

// CASFixture holds the hooks of the cases of the CAS protocol.
type CASFixture struct {
	// AnswerWith returns the callback of a ticket for the redirect r and
	// the person p whose validation answer has the form a.
	AnswerWith func(t *testing.T, r session.Redirect, p TestPerson, a CASAnswer) session.Callback
	// AnswerForService returns the callback of the redirect r with a
	// ticket that the CAS server issued for p and the service URL service,
	// not for the service URL of r.
	AnswerForService func(t *testing.T, r session.Redirect, p TestPerson, service string) session.Callback
	// WithSubjectAttribute returns a fixture over a fresh CAS server whose
	// method reads the subject from an attribute, and whose server sends
	// the user name of the person as the CAS user.
	WithSubjectAttribute func(t *testing.T) RedirectFixture
	// WithResolvedAddress returns a fixture over a fresh CAS server whose
	// URL has a host name that resolves to addr, and whose method applies
	// the egress rules with no exception for the loopback.
	WithResolvedAddress func(t *testing.T, addr netip.Addr) RedirectFixture
	// Validations returns the count of the validation requests that
	// reached the CAS server.
	Validations func() int64
	// ExternalURL is the external URL of the installation that the method
	// is configured with.
	ExternalURL string
}

// runCASCases runs the cases of the CAS protocol. Each case needs the
// hooks of probe.CAS; a fixture without them fails each case.
func runCASCases(t *testing.T, probe RedirectFixture, factory func(t *testing.T) RedirectFixture) {
	t.Helper()

	cases := []struct {
		run  func(t *testing.T, f RedirectFixture)
		name string
	}{
		{name: "FinishReturnsTheSubjectOfTheConfiguredAttribute", run: casSubjectOfTheConfiguredAttribute},
		{name: "ACASValidationWithRenewSetsTheAuthTime", run: casRenewSetsTheAuthTime},
		{
			name: "ACASValidationWithoutRenewHasAuthTimeKnownFalseWithNoAuthenticationDate",
			run:  casWithoutRenewHasNoAuthTime,
		},
		{name: "TheServiceURLComesFromTheServerNotFromTheCallback", run: casServiceURLFromTheServer},
		{name: "ATicketOutsideTheAllowlistIsErrMethodInput", run: casTicketOutsideTheAllowlist},
		{name: "AnXMLAnswerWithADTDIsErrMethodAnswer", run: casDTDAnswer},
		{name: "AnXMLAnswerDeeperThanTheLimitIsErrMethodAnswer", run: casDeepAnswer},
		{name: "EachOutboundConnectionGoesThroughTheEgressRules", run: casOutboundThroughTheEgressRules},
		{
			name: "AnEndpointThatResolvesToARefusedAddressIsErrMethodUnavailable",
			run:  casEndpointResolvesToARefusedAddress,
		},
		{name: "ASecondUseOfATicketIsErrReplay", run: casSecondUseOfATicket},
		{name: "AProviderThatDoesNotAnswerInTimeIsErrMethodUnavailable", run: casSlowProvider},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if probe.CAS == nil {
				t.Fatal("the fixture of a CAS method has no CAS hooks")
			}

			c.run(t, factory(t))
		})
	}
}

// casSubjectOfTheConfiguredAttribute is the case FinishReturnsTheSubjectOfTheConfiguredAttribute.
func casSubjectOfTheConfiguredAttribute(t *testing.T, f RedirectFixture) {
	t.Helper()

	g := f.CAS.WithSubjectAttribute(t)
	p := alice()

	if _, a := signIn(t, g, p, false); a.Subject != p.Subject || a.UserName != p.UserName {
		t.Fatalf("subject %q and user name %q, want %q from the attribute and %q", a.Subject, a.UserName,
			p.Subject, p.UserName)
	}
}

// casRenewSetsTheAuthTime is the case ACASValidationWithRenewSetsTheAuthTime.
func casRenewSetsTheAuthTime(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, true)

	a, err := f.Method.Finish(t.Context(), f.CAS.AnswerWith(t, r, alice(), CASAnswerNoAuthenticationDate))
	if err != nil {
		t.Fatalf("Finish() = %v", err)
	}

	checkValid(t, a, f.Now())

	if !a.AuthTimeKnown || a.AuthTime.Before(r.Pending.StartedAt.Add(-session.AssertionSkew)) ||
		a.AuthTime.After(f.Now().Add(session.AssertionSkew)) {
		t.Fatalf("auth time = %v (known %v), want the time of the validation", a.AuthTime, a.AuthTimeKnown)
	}
}

// casWithoutRenewHasNoAuthTime is the case
// ACASValidationWithoutRenewHasAuthTimeKnownFalseWithNoAuthenticationDate.
func casWithoutRenewHasNoAuthTime(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)

	a, err := f.Method.Finish(t.Context(), f.CAS.AnswerWith(t, r, alice(), CASAnswerNoAuthenticationDate))
	if err != nil {
		t.Fatalf("Finish() = %v", err)
	}

	checkValid(t, a, f.Now())

	if a.AuthTimeKnown || !a.AuthTime.IsZero() {
		t.Fatalf("auth time = %v (known %v), want unknown and zero", a.AuthTime, a.AuthTimeKnown)
	}
}

// casServiceURLFromTheServer is the case TheServiceURLComesFromTheServerNotFromTheCallback.
func casServiceURLFromTheServer(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)

	u, err := url.Parse(r.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}

	service := u.Query().Get("service")
	if !strings.HasPrefix(service, f.CAS.ExternalURL+"/") || !strings.Contains(service, "/"+f.ProviderID+"/") {
		t.Fatalf("service = %q, want a URL under %q for the provider %q", service, f.CAS.ExternalURL, f.ProviderID)
	}

	// A ticket for another service, as an attacker gets it for a service of
	// its own, does not validate: the method sends its own service URL.
	cb := f.CAS.AnswerForService(t, r, alice(), "https://attacker.example.com/cas/callback")

	a, err := f.Method.Finish(t.Context(), cb)
	checkRefused(t, a, err, session.ErrMethodRejected)
}

// casTicketOutsideTheAllowlist is the case ATicketOutsideTheAllowlistIsErrMethodInput.
func casTicketOutsideTheAllowlist(t *testing.T, f RedirectFixture) {
	t.Helper()

	for _, ticket := range []string{
		"PT-1-cHJveHk", "TGT-1-Z3JhbnQ", "st-1-bG93ZXI", "ST-", "ST-" + strings.Repeat("a", 254),
		"ST-1-a%2Fb", "ST-1-a/b", "ST-1-a b", "ST-1-a\nb", "ST-1-a&pgtUrl=https://evil.example.com", "ST-1-é",
	} {
		r := begin(t, f, false)
		cb := f.Answer(t, r, alice())
		cb.Ticket = ticket

		a, err := f.Method.Finish(t.Context(), cb)
		checkRefused(t, a, err, session.ErrMethodInput)
	}

	if n := f.CAS.Validations(); n != 0 {
		t.Fatalf("%d validation requests reached the CAS server, want 0", n)
	}
}

// casDTDAnswer is the case AnXMLAnswerWithADTDIsErrMethodAnswer.
func casDTDAnswer(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)

	a, err := f.Method.Finish(t.Context(), f.CAS.AnswerWith(t, r, alice(), CASAnswerDTD))
	checkRefused(t, a, err, session.ErrMethodAnswer)
}

// casDeepAnswer is the case AnXMLAnswerDeeperThanTheLimitIsErrMethodAnswer.
func casDeepAnswer(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)

	a, err := f.Method.Finish(t.Context(), f.CAS.AnswerWith(t, r, alice(), CASAnswerDeep))
	checkRefused(t, a, err, session.ErrMethodAnswer)
}

// casOutboundThroughTheEgressRules is the case EachOutboundConnectionGoesThroughTheEgressRules.
func casOutboundThroughTheEgressRules(t *testing.T, f RedirectFixture) {
	t.Helper()

	// The control: with the rules of the fixture, a sign-in reaches the
	// server once.
	signIn(t, f, alice(), false)

	if n := f.CAS.Validations(); n != 1 {
		t.Fatalf("%d validation requests, want 1", n)
	}

	// The same server on the loopback, with rules that have no exception
	// for it, gets no request.
	g := f.CAS.WithResolvedAddress(t, netip.MustParseAddr("127.0.0.1"))
	r := begin(t, g, false)

	a, err := g.Method.Finish(t.Context(), g.Answer(t, r, alice()))
	checkRefused(t, a, err, session.ErrMethodUnavailable)

	if n := g.CAS.Validations(); n != 0 {
		t.Fatalf("%d validation requests passed the egress rules, want 0", n)
	}
}

// casEndpointResolvesToARefusedAddress is the case
// AnEndpointThatResolvesToARefusedAddressIsErrMethodUnavailable.
func casEndpointResolvesToARefusedAddress(t *testing.T, f RedirectFixture) {
	t.Helper()

	for _, addr := range []string{"10.0.0.1", "169.254.169.254", "100.64.0.1", "fd00::1"} {
		g := f.CAS.WithResolvedAddress(t, netip.MustParseAddr(addr))
		r := begin(t, g, false)

		a, err := g.Method.Finish(t.Context(), g.Answer(t, r, alice()))
		checkRefused(t, a, err, session.ErrMethodUnavailable)
	}
}

// casSecondUseOfATicket is the case ASecondUseOfATicketIsErrReplay.
func casSecondUseOfATicket(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	cb := f.Answer(t, r, alice())

	if _, err := f.Method.Finish(t.Context(), cb); err != nil {
		t.Fatalf("first Finish() = %v", err)
	}

	a, err := f.Method.Finish(t.Context(), cb)
	checkRefused(t, a, err, session.ErrReplay)

	// The method refuses the second use itself, before it asks the CAS
	// server.
	if n := f.CAS.Validations(); n != 1 {
		t.Fatalf("%d validation requests, want 1", n)
	}
}

// casSlowProvider is the case AProviderThatDoesNotAnswerInTimeIsErrMethodUnavailable.
func casSlowProvider(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	start := time.Now()

	a, err := f.Method.Finish(t.Context(), f.CAS.AnswerWith(t, r, alice(), CASAnswerSlow))
	checkRefused(t, a, err, session.ErrMethodUnavailable)

	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("Finish() took %v, want the timeout of the method", d)
	}
}
