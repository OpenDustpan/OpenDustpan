package headerauth_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/headerauth"
	"opendustpan.org/opendustpan/pkg/session"
)

// spoofText returns values that make a name look like another name or break
// a line: a bidirectional override and isolate, zero-width characters, the
// line and paragraph separators, and a space that is not the ASCII space.
func spoofText() []string {
	return []string{
		"carol\u202egnp.exe", "carol\u2066", "car\u200bol", "car\u200dol", "\ufeffcarol",
		"carol\u2028admin", "carol\u2029admin", "carol\u00a0smith",
	}
}

// Layer 1, the boundary: the email and the groups of a JWT refuse each
// spoofing character.
func TestTheEmailAndTheGroupsOfAJWTRefuseSpoofingCharacters(t *testing.T) {
	t.Parallel()

	setup := jwtSetup{preset: headerauth.PresetGoogleIAP, alg: es256, claim: groupsClaim}
	f := jwtFixture(setup, newIssuerKeys(t, es256))(t)

	for _, v := range spoofText() {
		for name, edit := range map[string]func(c map[string]any){
			"email": func(c map[string]any) { c["email"] = v + "@example.com" },
			"group": func(c map[string]any) { c[groupsClaim] = []string{"ops", v} },
		} {
			c := f.JWT.Claims(alice())
			edit(c)
			raw := f.JWT.Sign(t, f.JWT.Header(), c)

			_, err := f.Method.FromRequest(t.Context(), facts(map[string][]string{f.UserHeader: {raw}}))
			if !errors.Is(err, session.ErrMethodAnswer) {
				t.Fatalf("%s %q: FromRequest() = %v, want %v", name, v, err, session.ErrMethodAnswer)
			}
		}
	}
}

// Layer 1, the boundary: the groups header of the plain mode refuses each
// spoofing character.
func TestTheGroupsHeaderRefusesSpoofingCharacters(t *testing.T) {
	t.Parallel()

	m := newMethod(t, plainConfig(&atomic.Bool{}, groupsHeader))

	for _, v := range spoofText() {
		f := facts(map[string][]string{
			"X-Auth-User": {"carol"}, "X-Proxy-Secret": {sharedSecret}, groupsHeader: {"ops," + v},
		})

		if _, err := m.FromRequest(t.Context(), f); !errors.Is(err, session.ErrMethodInput) {
			t.Fatalf("groups %q: FromRequest() = %v, want %v", v, err, session.ErrMethodInput)
		}
	}
}
