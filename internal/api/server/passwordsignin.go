package server

// The route of the sign-in with a password at a directory:
//
//   - POST /api/dustpan/v1/sign-in/providers/{provider}/password signs a
//     browser in with a user name and a password that the directory of the
//     provider checks (LDAP).

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/session"
)

// directorySignInPath is the path of the password sign-in of a provider.
const directorySignInPath = "/api/dustpan/v1/sign-in/providers/{provider}/password"

// tagSignIn is the tag of the sign-in routes in the description.
const tagSignIn = "sign-in"

// passwordProviderOperationLabels names the password sign-in of a provider in
// the description.
func passwordProviderOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodPost, directorySignInPath): apidesc.Name("signInWithProviderPassword", tagSignIn).
			WithBodies(apidesc.Typed[passwordSignInRequest]("PasswordSignInRequest"), apidesc.NoBody),
	}
}

// PasswordProvider is a sign-in provider of the configuration of the server
// that checks a user name and a password at a directory: LDAP.
type PasswordProvider struct {
	// Method checks the user name and the password. Its kind is
	// session.MethodLDAP.
	Method session.PasswordMethod
	// LDAP are the effective settings that the list of the providers shows.
	// They never hold a password.
	LDAP LDAPSettings
	// ID is the provider ID.
	ID string
	// DisplayName is the name that the console shows.
	DisplayName string
	// AutoCreate makes a principal at the first sign-in of a person.
	AutoCreate bool
}

// LDAPSettings are the settings of an LDAP provider that the list of the
// providers shows. The bind password is not one of them.
type LDAPSettings struct {
	// AllowedRanges are the private-network ranges that a connection to the
	// directory may reach, in addition to public addresses.
	AllowedRanges          []string
	URL                    string
	BindDN                 string
	BaseDN                 string
	UserFilter             string
	UserNameAttribute      string
	EmailAttribute         string
	NameAttribute          string
	Subject                string
	DisabledAttribute      string
	Groups                 string
	GroupBaseDN            string
	GroupFilter            string
	GroupMemberAttribute   string
	GroupMemberOfAttribute string
	GroupNameAttribute     string
	StartTLS               bool
}

// providerLDAP is the LDAP settings of a provider on the wire. BindPasswordSet
// is true when the provider has a bind password; no answer holds it.
type providerLDAP struct {
	AllowedRanges          []string `json:"allowedRanges"`
	URL                    string   `json:"url"`
	BindDN                 string   `json:"bindDn"`
	BaseDN                 string   `json:"baseDn"`
	UserFilter             string   `json:"userFilter"`
	UserNameAttribute      string   `json:"userNameAttribute"`
	EmailAttribute         string   `json:"emailAttribute"`
	NameAttribute          string   `json:"nameAttribute"`
	Subject                string   `json:"subject"`
	DisabledAttribute      string   `json:"disabledAttribute"`
	Groups                 string   `json:"groups"`
	GroupBaseDN            string   `json:"groupBaseDn"`
	GroupFilter            string   `json:"groupFilter"`
	GroupMemberAttribute   string   `json:"groupMemberAttribute"`
	GroupMemberOfAttribute string   `json:"groupMemberOfAttribute"`
	GroupNameAttribute     string   `json:"groupNameAttribute"`
	StartTLS               bool     `json:"startTLS"`
	BindPasswordSet        bool     `json:"bindPasswordSet"`
}

// Answers of the password sign-in of a provider.
var (
	// errPasswordSignInFailed is the one answer of each refused sign-in: a
	// wrong password, an unknown user, a locked name or person, a disabled
	// person, an unknown provider and a request of another site. The audit
	// entry holds the real reason. A body that is not valid answers
	// errLocalUserBody.
	errPasswordSignInFailed = &codedError{
		status: http.StatusUnauthorized, code: errcode.SignInFailed, args: nil,
		message: passwordWrongText,
	}
	// errPasswordSignInUnavailable is the answer when the directory does not
	// answer. It tells nothing about the person.
	errPasswordSignInUnavailable = &codedError{
		status: http.StatusServiceUnavailable, code: errcode.SignInUnavailable, args: nil,
		message: "the directory of the sign-in provider does not answer: try again later",
	}
)

// otherProviderActor is the actor of the audit entry of a sign-in of another
// site whose path names no configured password provider.
const otherProviderActor = "sign-in:password"

// passwordWrongText is the message of each refused password sign-in, on the
// route of a local user and on the route of a provider.
const passwordWrongText = "The user name or the password is wrong."

// errPasswordProvider reports a password provider that the handler cannot
// serve.
var errPasswordProvider = errors.New("password sign-in provider")

// passwordProvidersOf checks the password providers of cfg and returns them
// by ID. A provider needs a valid ID that no other provider has, and a method
// of the kind LDAP.
func passwordProvidersOf(cfg *SignIn) (map[string]PasswordProvider, error) {
	out := make(map[string]PasswordProvider, len(cfg.PasswordProviders))

	for i := range cfg.PasswordProviders {
		p := &cfg.PasswordProviders[i]

		switch {
		case p.Method == nil:
			return nil, fmt.Errorf("%w: method of sign-in provider %q", ErrMissingDependency, p.ID)
		case !session.ValidProviderID(p.ID) || p.ID == session.LocalProviderID || p.ID == session.DevProviderID:
			return nil, fmt.Errorf("%w: the provider ID %q is not valid", errPasswordProvider, p.ID)
		case p.Method.Kind() != session.MethodLDAP:
			return nil, fmt.Errorf("%w: the method of %q is not a password method", errPasswordProvider, p.ID)
		}

		_, redirect := providerIDIn(cfg.Providers, p.ID)
		if _, twice := out[p.ID]; twice || redirect {
			return nil, fmt.Errorf("%w: two providers have the ID %q", errPasswordProvider, p.ID)
		}

		out[p.ID] = *p
	}

	return out, nil
}

// providerIDIn reports whether providers has a provider with the ID id.
func providerIDIn(providers []SignInProvider, id string) (SignInProvider, bool) {
	for _, p := range providers {
		if p.ID == id {
			return p, true
		}
	}

	return SignInProvider{Method: nil, ID: "", DisplayName: "", AutoCreate: false, Stored: false}, false
}

// passwordProviderRoutes returns the route of the password sign-in of the
// providers, or none when the handler has no password provider or no password
// budget.
func (h *Handler) passwordProviderRoutes() []Route {
	if h.signIn == nil || len(h.signIn.passwordProviders) == 0 || h.signIn.passwords == nil {
		return nil
	}

	return []Route{{
		Method: http.MethodPost, Path: directorySignInPath, Scope: ScopePublic, Actions: nil, Resolve: nil,
		ActionsFor: nil, Serve: h.providerPasswordSignIn,
	}}
}

// providerPasswordSignIn signs a browser in with a user name and a password at
// the directory of the provider of the path. It answers 204 with the session
// cookie. Each refusal spends one unit of the password budget of the source
// address and answers 401 with one text, whatever the reason: so the answer
// does not tell that a user or a provider exists, or that a person is locked
// or disabled. A directory that does not answer gets 503.
func (h *Handler) providerPasswordSignIn(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	noReferrer(w)

	// Defense in depth: passwordProviderRoutes mounts this route only with a
	// password provider and the password budget. This check stays so that a
	// change to the route table never serves a password check with no limit.
	if h.signIn == nil || len(h.signIn.passwordProviders) == 0 || h.signIn.service == nil {
		h.writeError(ctx, w, errLocalUsersUnavailable)

		return
	}

	// Login CSRF: refuse a request of another site before the budget and
	// before any store or directory call. It spends nothing. The audit entry
	// names a fixed actor: a configured provider, or one actor for every
	// other path value, so the caller cannot open new keys of the limiter.
	if !h.signIn.checker.SignInFromBody(r) {
		actor := otherProviderActor
		if p, ok := h.signIn.passwordProviders[r.PathValue("provider")]; ok {
			actor = "sign-in:" + p.ID
		}

		h.recordOtherSiteSignIn(ctx, r, actor)
		h.writeError(ctx, w, errPasswordSignInFailed)

		return
	}

	if err := h.admitPassword(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var req passwordSignInRequest
	if err := decodeLocalUserBody(w, r, &req); err != nil {
		h.failPassword(ctx)
		h.writeError(ctx, w, err)

		return
	}

	p, ok := h.signIn.passwordProviders[r.PathValue("provider")]
	if !ok {
		h.failPassword(ctx)
		h.writeError(ctx, w, errPasswordSignInFailed)

		return
	}

	res, err := h.signIn.service.CompletePassword(ctx, &signinsvc.PasswordRequest{
		Entry: signInEntry(r, "", session.ActionSignIn),
		Provider: signinsvc.Provider{
			ID: p.ID, Kind: session.MethodLDAP, AutoCreate: p.AutoCreate, Stored: false,
		},
		Method: p.Method, UserName: req.UserName, Password: session.NewPassword(req.Password),
	})
	if err != nil {
		h.answerPasswordError(ctx, w, err)

		return
	}

	http.SetCookie(w, sessionCookie(res.Secret.Reveal(), h.sessionLength(ctx, res.Session)))
	w.WriteHeader(http.StatusNoContent)
}

// answerPasswordError answers the error of a password sign-in. A refusal has
// its audit entry already.
func (h *Handler) answerPasswordError(ctx context.Context, w http.ResponseWriter, err error) {
	if !signinsvc.IsRefusal(err) {
		h.writeError(ctx, w, err)

		return
	}

	if errors.Is(err, session.ErrMethodUnavailable) {
		// The directory did not answer: the person caused nothing, so the
		// budget of the address does not change.
		h.writeError(ctx, w, errPasswordSignInUnavailable)

		return
	}

	h.failPassword(ctx)
	h.writeError(ctx, w, errPasswordSignInFailed)
}

// passwordProviderOf returns the wire form of a password provider of the
// configuration of the server, with its effective LDAP settings and never the
// bind password.
func passwordProviderOf(p *PasswordProvider) providerResponse {
	s := &p.LDAP

	return providerResponse{
		CreatedAt: nil, UpdatedAt: nil, ID: p.ID, Source: providerSourceStatic, Issuer: "",
		DisplayName: p.DisplayName, ClientID: "", Scopes: []string{}, Version: 0, AutoCreate: p.AutoCreate,
		Enabled: true, ClientSecretSet: false, Mode: string(session.ModeLDAP), AuthorizationURL: "", TokenURL: "",
		UserinfoURL: "", Claims: nil,
		LDAP: &providerLDAP{
			AllowedRanges: nonNilRanges(
				s.AllowedRanges,
			),
			URL:                    s.URL,
			BindDN:                 s.BindDN,
			BaseDN:                 s.BaseDN,
			UserFilter:             s.UserFilter,
			UserNameAttribute:      s.UserNameAttribute,
			EmailAttribute:         s.EmailAttribute,
			NameAttribute:          s.NameAttribute,
			Subject:                s.Subject,
			DisabledAttribute:      s.DisabledAttribute,
			Groups:                 s.Groups,
			GroupBaseDN:            s.GroupBaseDN,
			GroupFilter:            s.GroupFilter,
			GroupMemberAttribute:   s.GroupMemberAttribute,
			GroupMemberOfAttribute: s.GroupMemberOfAttribute,
			GroupNameAttribute:     s.GroupNameAttribute,
			StartTLS:               s.StartTLS,
			BindPasswordSet:        true,
		},
	}
}

// nonNilRanges returns ranges, or an empty list for nil, so the answer shows
// the effective value [] and not null.
func nonNilRanges(ranges []string) []string {
	if ranges == nil {
		return []string{}
	}

	return ranges
}
