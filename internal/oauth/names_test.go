package oauth_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/session"
)

// TestOIDCSignInKeepsRealDisplayNames signs in through an OIDC provider
// whose person has a real name with a joiner, a no-break space, a soft
// hyphen or an emoji sequence. The method keeps the name, and the Assertion
// passes the final check of the sign-in.
func TestOIDCSignInKeepsRealDisplayNames(t *testing.T) {
	t.Parallel()

	for name, v := range map[string]string{
		"Persian with a non-joiner":  "\u0639\u0644\u06cc\u200c\u0631\u0636\u0627",
		"Devanagari with a joiner":   "\u0915\u094d\u200d\u0937",
		"no-break space":             "Jean\u00a0Dupont",
		"soft hyphen":                "Mar\u00adtin",
		"emoji sequence with joiner": "\U0001f469\u200d\U0001f4bb Ana",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newMethodSetup(t, oauthtest.ModeOIDC)
			s.provider.SetUser(oauthtest.User{
				Subject: "alice-1", Name: v, PreferredUsername: "alice", Email: "alice@example.com",
			})

			r, err := s.method.Begin(
				t.Context(),
				session.BeginInput{ProviderID: methodProvider, State: "c3RhdGU", Fresh: false},
			)
			if err != nil {
				t.Fatalf("Begin() = %v", err)
			}

			a, err := s.method.Finish(t.Context(), s.browse(t, r))
			if err != nil {
				t.Fatalf("Finish() = %v", err)
			}

			if !slices.Contains(a.Names, v) {
				t.Fatalf("Names = %q, want it to hold %q", a.Names, v)
			}

			if err := session.CheckAssertion(a, s.clock.Now()); err != nil {
				t.Fatalf("CheckAssertion() = %v", err)
			}
		})
	}
}

// TestMethodRefusesSpoofingNames sends each name, user name and email that
// would spoof another name or break a line to the checks of the method
// alone, as if the client had not refused it.
func TestMethodRefusesSpoofingNames(t *testing.T) {
	t.Parallel()

	values := []string{
		"alice\u202egnp.exe", "alice\u2066", "alice\u200f", "ali\u200bce", "\ufeffalice",
		"alice\u2028admin", "alice\u2029", "\u200dalice", "alice \u200c admin",
	}

	fields := map[string]func(id *oauth.Identity, v string){
		"name":      func(id *oauth.Identity, v string) { id.Name = v },
		"user name": func(id *oauth.Identity, v string) { id.PreferredUsername = v },
		"email":     func(id *oauth.Identity, v string) { id.Email = v },
		"subject":   func(id *oauth.Identity, v string) { id.Subject = v },
	}

	for field, set := range fields {
		for _, v := range values {
			id := stubIdentity()
			set(&id, v)

			a, err := stubFinish(t, stubClient{beginErr: nil, finish: nil, authURL: "https://idp.example/a", id: id},
				false)
			if !errors.Is(err, session.ErrMethodAnswer) || a.Subject != "" {
				t.Fatalf("%s %q: Finish() = %+v, %v, want ErrMethodAnswer and the zero Assertion", field, v, a, err)
			}
		}
	}
}
