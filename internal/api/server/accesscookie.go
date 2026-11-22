package server

import (
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/session"
)

// reissueBefore is how close to the expiry a request re-issues the access
// cookie that the stored session authenticated.
const reissueBefore = 30 * time.Second

// accessCookie returns the access cookie of the console with value and the
// longest life maxAge. It is Secure, HttpOnly, SameSite=Strict and Path=/,
// with no Domain, as the prefix __Host- needs.
func accessCookie(value string, maxAge time.Duration) *http.Cookie {
	return browserCookie(session.AccessCookie, value, maxAge, http.SameSiteStrictMode)
}

// setAccessCookie seals the fresh fact of sess and sets the access cookie. A
// sign-in calls it next to the session cookie. A renewal after a password
// change calls it with the zero method: a renewal is not a sign-in and never
// mints a fact.
func (h *Handler) setAccessCookie(
	w http.ResponseWriter,
	r *http.Request,
	sess session.Session,
	how session.SignInMethod,
) {
	if h.signIn == nil || h.signIn.sealer == nil {
		return
	}

	now := h.now()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	now = now.UTC().Truncate(time.Millisecond)
	claims := session.ClaimsOf(sess, how, now)

	var access session.AccessClaims
	access.Claims = claims
	access.IssuedAt = now
	access.ExpiresAt = now.Add(session.AccessLifetime)
	access.KeyID = ""

	raw, err := h.signIn.sealer.SealAccess(r.Context(), access)
	if err != nil {
		return
	}

	http.SetCookie(w, accessCookie(raw, time.Until(access.ExpiresAt)))
}

// clearAccessCookie expires the access cookie of the browser.
func (h *Handler) clearAccessCookie(w http.ResponseWriter) {
	http.SetCookie(w, accessCookie("", -1))
}

// maybeReissueAccessCookie sets a new access cookie with the claims of the
// opened cookie and a new expiry, for a request that the stored session
// authenticated, near the expiry. It never changes the fresh kind or times.
func (h *Handler) maybeReissueAccessCookie(w http.ResponseWriter, r *http.Request, caller *sessionCaller) {
	if h.signIn == nil || h.signIn.sealer == nil || caller == nil {
		return
	}

	raw, found := apidesc.Cookie(r, session.AccessCookie)
	if !found || raw == "" {
		return
	}

	now := reissueNow(h)
	opened, ok := openForReissue(h, r, raw, now)
	if !ok {
		return
	}

	if !needsReissue(opened, caller.Match, now) {
		return
	}

	renewed := renewedClaims(opened, now)

	sealed, err := h.signIn.sealer.SealAccess(r.Context(), renewed)
	if err != nil {
		return
	}

	http.SetCookie(w, accessCookie(sealed, time.Until(renewed.ExpiresAt)))
}

// reissueNow returns the clock of h for a re-issue.
func reissueNow(h *Handler) time.Time {
	now := h.now()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	return now.UTC()
}

// openForReissue opens the access cookie raw for a re-issue.
func openForReissue(h *Handler, r *http.Request, raw string, now time.Time) (session.AccessClaims, bool) {
	var none session.AccessClaims

	opened, err := h.signIn.sealer.OpenAccess(r.Context(), raw, now)
	if err != nil {
		return none, false
	}

	return opened, true
}

// needsReissue reports whether opened, bound to m, needs a new cookie at now.
func needsReissue(opened session.AccessClaims, m session.Match, now time.Time) bool {
	var none session.FreshFact

	// Defense in depth: the stored session authenticated the request first;
	// this bind stays so that a forged, expired or unbound cookie gets no
	// new cookie.
	if session.BindFact(opened, m, now) == none {
		return false
	}

	if opened.ExpiresAt.IsZero() || !now.Before(opened.ExpiresAt) {
		return false
	}

	return opened.ExpiresAt.Sub(now) <= reissueBefore
}

// renewedClaims returns opened with a new issue and expiry and the same
// fresh kind and times.
func renewedClaims(opened session.AccessClaims, now time.Time) session.AccessClaims {
	renewed := opened
	renewed.IssuedAt = now.Truncate(time.Millisecond)
	renewed.ExpiresAt = renewed.IssuedAt.Add(session.AccessLifetime)
	// Defense in depth: the handler copies the claims of the opened cookie;
	// this copy stays so that a re-issue never moves a fresh time.
	renewed.FreshAt = opened.FreshAt
	renewed.MFAAt = opened.MFAAt
	renewed.FreshKind = opened.FreshKind

	return renewed
}

// ldapSignInMethod returns the method of a password sign-in at a directory.
func ldapSignInMethod() session.SignInMethod {
	return session.SignInMethod{
		Method: session.MethodLDAP, AuthTime: time.Time{}, AuthTimeKnown: false,
		Factors: []session.Factor{session.FactorPassword},
	}
}

// passkeySignInMethod returns the method of a passkey sign-in.
func passkeySignInMethod() session.SignInMethod {
	return session.SignInMethod{
		Method: session.MethodPasskey, AuthTime: time.Time{}, AuthTimeKnown: false, Factors: nil,
	}
}

// localSignInMethod returns the method of a local password sign-in from its
// assertion.
func localSignInMethod(a session.Assertion) session.SignInMethod {
	return session.SignInMethod{
		Method: a.Method, AuthTime: a.AuthTime, AuthTimeKnown: a.AuthTimeKnown, Factors: a.Factors,
	}
}

// ssoSignInMethod returns the method of a redirect sign-in from its
// assertion. The time comes only from the provider.
func ssoSignInMethod(a session.Assertion) session.SignInMethod {
	return session.SignInMethod{
		Method: a.Method, AuthTime: a.AuthTime, AuthTimeKnown: a.AuthTimeKnown, Factors: a.Factors,
	}
}

// emptySignInMethod returns no method: a renewal and a dev sign-in mint no
// fact.
func emptySignInMethod() session.SignInMethod {
	return session.SignInMethod{
		Method: "", AuthTime: time.Time{}, AuthTimeKnown: false, Factors: nil,
	}
}
