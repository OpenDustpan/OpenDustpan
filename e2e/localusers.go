//go:build e2e

package e2e

import "net/http"

// localUsersCoveringTest drives the routes of the local users and the
// password sign-in: no CLI makes a local user or signs in with a password.
const localUsersCoveringTest = "TestLocalPasswordSignIn"

// localUserAllowEntries returns the routes of the local users and of the
// password sign-in. The steps of localUsersCoveringTest drive each one
// through the server of each store: the administrator makes a user, the
// person sets the password with the set-up code, signs in with a cookie jar,
// changes the password, and the administrator reads the policy and tries a
// reset with a token. Ten wrong passwords lock the name; the administrator
// reads the lock and unlocks it.
func localUserAllowEntries() []AllowEntry {
	reason := "Console route: no CLI makes a local user or signs in with a password. " +
		"The harness plays the browser with a cookie jar and the administrator with a token, and checks each answer."

	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/dustpan/v1/local-users"},
		{http.MethodPost, "/api/dustpan/v1/local-users/redeem"},
		{http.MethodGet, "/api/dustpan/v1/local-users/policy"},
		{http.MethodPost, "/api/dustpan/v1/local-users/reset"},
		{http.MethodPost, "/api/dustpan/v1/local-users/unlock"},
		{http.MethodGet, "/api/dustpan/v1/local-users/locks"},
		{http.MethodPost, "/api/dustpan/v1/sign-in/password"},
		{http.MethodPost, "/api/dustpan/v1/me/password"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: localUsersCoveringTest, Reason: reason,
		})
	}

	return out
}
