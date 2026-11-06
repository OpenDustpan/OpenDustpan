//go:build e2e

package e2e

import "net/http"

// passkeysCoveringTest drives the routes of the passkeys of a person: no CLI
// adds a passkey.
const enrollmentCoveringTest = "TestPasskeyEnrollment"

// passkeyAllowEntries returns the routes of the passkeys of a person. The
// steps of enrollmentCoveringTest drive each one through the server of each
// store: a person signs in with a password, adds a passkey with a software
// authenticator, lists it, and removes it.
func passkeyAllowEntries() []AllowEntry {
	reason := "Console route: no CLI adds a passkey. The harness plays the browser with a cookie jar and a " +
		"software authenticator, and checks each answer."

	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/dustpan/v1/me/passkeys/register/begin"},
		{http.MethodPost, "/api/dustpan/v1/me/passkeys/register/finish"},
		{http.MethodGet, "/api/dustpan/v1/me/passkeys"},
		{http.MethodDelete, "/api/dustpan/v1/me/passkeys/{id}"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: enrollmentCoveringTest, Reason: reason,
		})
	}

	return out
}
