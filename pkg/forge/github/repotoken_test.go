package github_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/pkg/forge"
)

// canaryValue is the value of the token that the raw GitHub of the tests gives.
// No error may hold it.
const canaryValue = "ghs_canary_0123456789"

// narrowAnswer is the answer of GitHub to a token request for the contents of
// the repository infra, with the expiry expires. An empty expires leaves the
// field out.
func narrowAnswer(expires string) string {
	field := ""
	if expires != "" {
		field = `"expires_at":"` + expires + `",`
	}

	return `{"token":"` + canaryValue + `",` + field +
		`"permissions":{"contents":"read","metadata":"read"},"repository_selection":"selected","repositories":[{"name":"infra"}]}`
}

// tokenClock is the time of the adapter in the tests of the expiry.
func tokenClock() time.Time { return time.Date(2030, time.March, 4, 12, 0, 0, 0, time.UTC) }

func clockAt(at time.Time) func() time.Time { return func() time.Time { return at } }

// TestRepoTokenScoped checks the token of an app connection: one repository,
// the contents read, one hour at most, and Scoped.
func TestRepoTokenScoped(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)
	now := githubfakeClock()

	got, err := o.fg.RepoToken(t.Context(), conformanceRepo)
	if err != nil {
		t.Fatalf("RepoToken = %v", err)
	}

	if !got.Scoped || got.Username != "x-access-token" || len(got.Value) == 0 {
		t.Fatalf("RepoToken = %+v, want a scoped token for x-access-token", got)
	}

	if !slices.Contains(o.host.IssuedTokens(), string(got.Value)) {
		t.Fatal("the token is not one that the host made")
	}

	if !got.ExpiresAt.After(now) || got.ExpiresAt.After(now.Add(time.Hour)) {
		t.Fatalf("ExpiresAt = %v, want after %v and at most one hour later", got.ExpiresAt, now)
	}

	asked := tokenRequestOf(t, o.host)

	if !slices.Equal(asked.Repositories, []string{"infra"}) ||
		len(asked.Permissions) != 1 || asked.Permissions["contents"] != "read" {
		t.Fatalf("the token request asked for %+v, want the contents of infra at read", asked)
	}
}

// tokenRequest is the body of the request for an installation token.
type tokenRequest struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

// tokenRequestOf returns the body of the request for a token that the host got.
func tokenRequestOf(t *testing.T, host *githubfake.Fake) tokenRequest {
	t.Helper()

	var asked tokenRequest

	for _, r := range host.Requests() {
		if strings.HasSuffix(r.URI, "/access_tokens") {
			if err := json.Unmarshal(r.Body, &asked); err != nil {
				t.Fatal(err)
			}

			return asked
		}
	}

	t.Fatal("the host got no request for a token")

	return asked
}

// TestRepoTokenOfATokenConnection checks that a token connection answers its
// own token with Scoped not set, and the errors of the port.
func TestRepoTokenOfATokenConnection(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthToken)

	got, err := o.fg.RepoToken(t.Context(), conformanceRepo)
	if err != nil {
		t.Fatalf("RepoToken = %v", err)
	}

	if got.Scoped || got.Username != "x-access-token" || string(got.Value) != o.host.UserToken() {
		t.Fatalf("RepoToken = %+v, want the own token of the connection, not scoped", got)
	}

	if o.fg.Capabilities().Has(forge.CapRepoToken) {
		t.Fatal("a token connection lists repo-token")
	}

	_, missingErr := o.fg.RepoToken(t.Context(), "acme/no-such-repository")
	if !errors.Is(missingErr, forge.ErrNotFound) {
		t.Fatalf("RepoToken of a missing repository = %v, want ErrNotFound", missingErr)
	}

	o.host.Refuse()

	_, err = o.fg.RepoToken(t.Context(), conformanceRepo)
	if !errors.Is(err, forge.ErrAuth) || strings.Contains(err.Error(), o.host.UserToken()) {
		t.Fatalf("RepoToken with a refused token = %v, want ErrAuth with no token", err)
	}
}

// githubfakeClock is the time of the adapter that newHostOpener makes.
func githubfakeClock() time.Time { return githubfake.Epoch().Add(time.Minute) }

// TestRepoTokenExpiryIsCut checks the expiry of the token against the time of
// the call.
func TestRepoTokenExpiryIsCut(t *testing.T) {
	t.Parallel()

	format := func(d time.Duration) string { return tokenClock().Add(d).Format(time.RFC3339) }

	tests := []struct {
		name    string
		expires string
		want    time.Duration
		refused bool
	}{
		{"24 hours is cut to one hour", format(24 * time.Hour), time.Hour, false},
		{"one hour and one second is cut to one hour", format(time.Hour + time.Second), time.Hour, false},
		{"exactly one hour stays", format(time.Hour), time.Hour, false},
		{"30 minutes stay", format(30 * time.Minute), 30 * time.Minute, false},
		{"one second stays", format(time.Second), time.Second, false},
		{"no expiry gets a short fallback", "", 5 * time.Minute, false},
		{"an expiry that is not a time gets the fallback", "tomorrow", 5 * time.Minute, false},
		{"the time of the call is refused", format(0), 0, true},
		{"a past expiry is refused", format(-time.Minute), 0, true},
		{"a past expiry of a day is refused", format(-24 * time.Hour), 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := rawAppAt(t, narrowAnswer(tc.expires), clockAt(tokenClock()))

			got, err := raw.fg.RepoToken(t.Context(), conformanceRepo)

			if tc.refused {
				requireRefused(t, got, err)

				return
			}

			if err != nil {
				t.Fatalf("RepoToken = %v", err)
			}

			if want := tokenClock().Add(tc.want); !got.ExpiresAt.Equal(want) || !got.Scoped {
				t.Fatalf("RepoToken = %+v, want the expiry %v and Scoped", got, want)
			}
		})
	}
}

// TestRepoTokenWiderGrantIsRefused sends answers that give more than the
// request asked for. The adapter returns an error and no token.
func TestRepoTokenWiderGrantIsRefused(t *testing.T) {
	t.Parallel()

	const start = `"token":"` + canaryValue + `","expires_at":"2030-03-04T12:30:00Z"`

	tests := []struct {
		name string
		body string
	}{
		{
			"contents write",
			`{` + start + `,"permissions":{"contents":"write","metadata":"read"},"repository_selection":"selected","repositories":[{"name":"infra"}]}`,
		},
		{
			"an extra permission",
			`{` + start + `,"permissions":{"contents":"read","administration":"read"},"repository_selection":"selected","repositories":[{"name":"infra"}]}`,
		},
		{"all repositories", `{` + start + `,"permissions":{"contents":"read"},"repository_selection":"all"}`},
		{
			"two repositories",
			`{` + start + `,"permissions":{"contents":"read"},"repository_selection":"selected","repositories":[{"name":"infra"},{"name":"other"}]}`,
		},
		{
			"another repository",
			`{` + start + `,"permissions":{"contents":"read"},"repository_selection":"selected","repositories":[{"name":"other"}]}`,
		},
		{"no repository list", `{` + start + `,"permissions":{"contents":"read"},"repository_selection":"selected"}`},
		{"no permissions", `{` + start + `,"repository_selection":"selected","repositories":[{"name":"infra"}]}`},
		{
			"no token",
			`{"permissions":{"contents":"read"},"repository_selection":"selected","repositories":[{"name":"infra"}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := rawAppAt(t, tc.body, clockAt(tokenClock()))

			got, err := raw.fg.RepoToken(t.Context(), conformanceRepo)
			requireRefused(t, got, err)
		})
	}

	// The control: a narrow answer passes.
	raw := rawAppAt(t, narrowAnswer("2030-03-04T12:30:00Z"), clockAt(tokenClock()))
	if _, err := raw.fg.RepoToken(t.Context(), conformanceRepo); err != nil {
		t.Fatalf("RepoToken with a narrow answer = %v", err)
	}
}

// requireRefused checks that RepoToken gave an error and no token, and that
// the error holds no part of the token that GitHub gave.
func requireRefused(t *testing.T, got forge.Token, err error) {
	t.Helper()

	if err == nil || len(got.Value) != 0 || got.Scoped {
		t.Fatalf("RepoToken = %+v, %v; want an error and no token", got, err)
	}

	if strings.Contains(err.Error(), canaryValue) {
		t.Fatalf("the error holds the token: %v", err)
	}
}

// TestRepoTokenInvalidRepository checks that a bad repository name is refused
// before any call, for each auth kind.
func TestRepoTokenInvalidRepository(t *testing.T) {
	t.Parallel()

	names := []string{
		"",
		"a/b/c",
		"../x",
		"acme/..",
		"/acme/infra",
		"acme",
		`acme\infra`,
		"acme/in\r\nfra",
		"acme/infra\x00",
	}

	for _, auth := range []forge.AuthKind{forge.AuthApp, forge.AuthToken} {
		o := openHost(t, auth)
		before := o.host.Calls()

		for _, name := range names {
			_, err := o.fg.RepoToken(t.Context(), name)
			if !errors.Is(err, forge.ErrInvalid) {
				t.Errorf("%s: RepoToken(%q) = %v, want ErrInvalid", auth, name, err)
			}
		}

		if after := o.host.Calls(); after != before {
			t.Errorf("%s: RepoToken made %d calls for a bad name, want none", auth, after-before)
		}
	}
}

// TestRepoTokenAnotherAccount checks that an app connection asks for no token
// for a repository of an account other than the one of its installation.
func TestRepoTokenAnotherAccount(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)

	if _, err := o.host.AddRepo("stranger/infra", map[string][]byte{"a": []byte("a")}); err != nil {
		t.Fatal(err)
	}

	before := o.host.Calls()

	got, err := o.fg.RepoToken(t.Context(), "stranger/infra")
	if !errors.Is(err, forge.ErrNotFound) || len(got.Value) != 0 {
		t.Fatalf("RepoToken = %+v, %v; want ErrNotFound and no token", got, err)
	}

	for _, r := range o.host.Requests()[before:] {
		if strings.HasSuffix(r.URI, "/access_tokens") {
			t.Fatalf("the adapter asked for a token: %s %s", r.Method, r.URI)
		}
	}
}

// TestRepoTokenMissingRepository checks that an app connection answers
// ErrNotFound for a repository that the installation cannot see.
func TestRepoTokenMissingRepository(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)

	if _, err := o.fg.RepoToken(t.Context(), "acme/no-such-repository"); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("RepoToken = %v, want ErrNotFound", err)
	}
}
