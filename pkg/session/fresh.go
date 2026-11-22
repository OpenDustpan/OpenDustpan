package session

import (
	"slices"
	"time"
)

// FreshFact is the fresh authentication that a request carries. The zero
// FreshFact is no fact.
type FreshFact struct {
	Kind  FreshKind
	MFAAt time.Time
	At    time.Time
}

// SignInMethod is how the person authenticated in this sign-in.
type SignInMethod struct {
	Method        MethodKind
	AuthTime      time.Time
	AuthTimeKnown bool
	Factors       []Factor
}

// ClaimsOf builds the claims of an access cookie from a started session and
// the way the person authenticated. It sets a kind and a time only for a
// step that happened in this sign-in.
func ClaimsOf(s Session, how SignInMethod, now time.Time) Claims {
	nowMS := now.UTC().Truncate(time.Millisecond)

	var out Claims
	out.SignInAt = s.CreatedAt
	out.AuthTime = s.AuthTime
	out.PrincipalID = s.PrincipalID
	out.Provider = s.Provider
	out.Generation = s.Generation

	if how.Method == MethodPasskey {
		out.FreshKind = FreshPasskey
		out.FreshAt = nowMS
		out.MFAAt = nowMS

		return out
	}

	if how.Method == MethodLocalPassword || how.Method == MethodLDAP {
		applyPasswordKind(&out, how.Factors, nowMS)

		return out
	}

	if how.Method == MethodOIDC || how.Method == MethodSAML || how.Method == MethodCAS {
		applySSOKind(&out, how, nowMS)

		return out
	}

	return out
}

// applyPasswordKind sets the fresh kind of out for a password method.
func applyPasswordKind(out *Claims, factors []Factor, nowMS time.Time) {
	if !slices.Contains(factors, FactorPassword) {
		return
	}

	if hasSecondFactor(factors) {
		out.FreshKind = FreshMFA
		out.FreshAt = nowMS
		out.MFAAt = nowMS
	} else {
		out.FreshKind = FreshPassword
		out.FreshAt = nowMS
	}
}

// applySSOKind sets the fresh kind of out for an SSO method. The time comes
// only from the authentication time that the provider gave.
func applySSOKind(out *Claims, how SignInMethod, nowMS time.Time) {
	if !how.AuthTimeKnown || how.AuthTime.IsZero() {
		return
	}

	auth := how.AuthTime.UTC().Truncate(time.Millisecond)
	if auth.After(nowMS) {
		return
	}

	if nowMS.Sub(auth) > MaxTokenFreshness {
		return
	}

	out.FreshKind = FreshSSO
	out.FreshAt = auth
}

// hasSecondFactor reports whether factors holds a second factor that gives a
// fresh authentication with a password. A recovery code never gives one.
func hasSecondFactor(factors []Factor) bool {
	return slices.Contains(factors, FactorTOTP) ||
		slices.Contains(factors, FactorPasskey) ||
		slices.Contains(factors, FactorSSHSignature)
}

// BindFact returns the fact of c when c belongs to the stored session m, and
// the zero FreshFact otherwise.
func BindFact(c AccessClaims, m Match, now time.Time) FreshFact {
	var none FreshFact

	if !bindNotExpired(c, now) {
		return none
	}

	if !bindPrincipal(c, m) {
		return none
	}

	if !bindGeneration(c, m) {
		return none
	}

	if !bindSignInTime(c, m) {
		return none
	}

	if !bindKindAndTimes(c, now) {
		return none
	}

	return FreshFact{Kind: c.FreshKind, MFAAt: c.MFAAt, At: c.FreshAt}
}

// bindNotExpired reports whether c expires after now.
func bindNotExpired(c AccessClaims, now time.Time) bool {
	nowUTC := now.UTC()

	// Defense in depth: the open of the cookie checks the seal and the
	// expiry too; this check stays so that a new caller cannot skip it.
	return !c.ExpiresAt.IsZero() && nowUTC.Before(c.ExpiresAt)
}

// bindPrincipal reports whether c names the person of m.
func bindPrincipal(c AccessClaims, m Match) bool {
	// Defense in depth: the stored session authenticated the request first;
	// this check stays so that a cookie of another person never gives a fact.
	if c.PrincipalID == "" || c.PrincipalID != m.Session.PrincipalID {
		return false
	}

	// Defense in depth: the stored session authenticated the request first;
	// this check stays so that a cookie of another person never gives a fact.
	return c.PrincipalID == m.Owner.ID
}

// bindGeneration reports whether c carries the stored generation of m.
func bindGeneration(c AccessClaims, m Match) bool {
	// Defense in depth: the stored session check compares the generation too;
	// this check stays so that a cookie of an older generation never gives a fact.
	if c.Generation == 0 || c.Generation != m.Generation {
		return false
	}

	// Defense in depth: the stored session check compares the generation too;
	// this check stays so that a cookie of an older generation never gives a fact.
	return c.Generation == m.Session.Generation
}

// bindSignInTime reports whether c carries the sign-in time of m.
func bindSignInTime(c AccessClaims, m Match) bool {
	if c.SignInAt.IsZero() || m.Session.CreatedAt.IsZero() {
		return false
	}

	return c.SignInAt.UTC().UnixMilli() == m.Session.CreatedAt.UTC().UnixMilli()
}

// bindKindAndTimes reports whether c carries a known kind with usable times.
func bindKindAndTimes(c AccessClaims, now time.Time) bool {
	nowUTC := now.UTC()
	kind := c.FreshKind

	if kind != FreshPasskey && kind != FreshMFA && kind != FreshPassword && kind != FreshSSO {
		return false
	}

	if c.FreshAt.IsZero() || c.FreshAt.UTC().After(nowUTC) {
		return false
	}

	// Defense in depth: the handler seals the MFA time with the fresh time;
	// this check stays so that a forged time never gives a fact.
	if c.MFAAt.UTC().After(nowUTC) {
		return false
	}

	if (kind == FreshPasskey || kind == FreshMFA) && c.MFAAt.IsZero() {
		return false
	}

	return true
}
