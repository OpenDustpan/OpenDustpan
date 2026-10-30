package github_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/forgetest"
	"opendustpan.org/opendustpan/pkg/forge/github"
)

// hostFixture is the Fixture of the conformance suite over the fake GitHub
// host of package githubfake.
type hostFixture struct {
	host *githubfake.Fake
	repo string
}

func commitOf(c githubfake.Commit) forge.Commit {
	return forge.Commit{ID: c.ID, Time: c.Time}
}

func (x *hostFixture) Push(_ context.Context, branch string, files map[string][]byte) (forge.Commit, error) {
	c, err := x.host.Push(x.repo, branch, files, "Push to "+branch)
	if err != nil {
		return forge.Commit{}, fmt.Errorf("push to the fake: %w", err)
	}

	return commitOf(c), nil
}

func (x *hostFixture) Tag(_ context.Context, name, commitID string) error {
	if err := x.host.Tag(x.repo, name, commitID); err != nil {
		return fmt.Errorf("tag in the fake: %w", err)
	}

	return nil
}

func (x *hostFixture) Protect(_ context.Context, branch string) error {
	if err := x.host.Protect(x.repo, branch); err != nil {
		return fmt.Errorf("protect in the fake: %w", err)
	}

	return nil
}

func (x *hostFixture) Merge(context.Context, string) error { return errNotImplemented }

func (x *hostFixture) Event(context.Context, forge.EventKind, []byte) (forge.EventRequest, error) {
	return forge.EventRequest{}, errNotImplemented
}

func (x *hostFixture) Redeliver(context.Context, forge.EventRequest) (forge.EventRequest, error) {
	return forge.EventRequest{}, errNotImplemented
}

func (x *hostFixture) Refuse(context.Context) error {
	x.host.Refuse()

	return nil
}

func (x *hostFixture) Calls(context.Context) (int, error) { return x.host.Calls(), nil }

func (x *hostFixture) SetTokenExpiry(_ context.Context, at time.Time) error {
	x.host.SetTokenExpiry(at)

	return nil
}

func (x *hostFixture) Garble(context.Context) (func(), error) { return x.host.Garble(), nil }

func (x *hostFixture) Unavailable(_ context.Context, retryAfter time.Duration) (func(), error) {
	return x.host.Unavailable(retryAfter), nil
}

const conformanceRepo = "acme/infra"

// newHost starts a fake GitHub host with the repository of the suite, one
// commit on main, and a token for the user.
func newHost(t *testing.T) *githubfake.Fake {
	t.Helper()

	host := githubfake.New(t, githubfake.Config{
		AppID: "", InstallationID: "", Owner: githubfake.Account{ID: 0, Login: ""},
		User: githubfake.Account{ID: 0, Login: ""}, UserToken: "github_pat_" + randomHex(t, 24),
		Permissions: nil, Key: testKey(), Now: nil, WebhookClient: nil,
	})

	if _, err := host.AddRepo(conformanceRepo, map[string][]byte{"README.md": []byte("hello\n")}); err != nil {
		t.Fatal(err)
	}

	return host
}

// newHostOpener returns an opener that trusts the fake host and may reach
// the loopback.
func newHostOpener(t *testing.T, host *githubfake.Fake) *github.Opener {
	t.Helper()

	return newHostOpenerAt(t, host, func() time.Time { return githubfake.Epoch().Add(time.Minute) })
}

// newHostOpenerAt is newHostOpener with a clock for the adapter. The clock
// of the fake starts at its Epoch, so the clock of the adapter does too.
func newHostOpenerAt(t *testing.T, host *githubfake.Fake, now func() time.Time) *github.Opener {
	t.Helper()

	o, err := github.NewOpener(github.Config{
		Rules: egress.Rules{Allow: nil, LoopbackForTests: true}, RootCAs: host.CertPool(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(o.CloseIdleConnections)

	return o
}

// hostConnection returns the connection and the credential of auth for host.
func hostConnection(t *testing.T, host *githubfake.Fake, auth forge.AuthKind) (forge.Connection, forge.Credential) {
	t.Helper()

	conn := forge.Connection{
		ID: "c1", Kind: forge.KindGitHub, Auth: auth, BaseURL: host.URL(), Params: nil, Push: true,
	}
	secret := []byte(host.UserToken())

	if auth == forge.AuthApp {
		conn.Params = map[string]string{
			forge.ParamAppID: host.AppID(), forge.ParamInstallationID: host.InstallationID(),
		}
		secret = pkcs1PEM(host.PrivateKey())
	}

	return conn, forge.Credential{SetID: "s1", Secret: secret, CreatedAt: time.Now()}
}

// conformanceHarness returns a factory of harnesses over the fake GitHub
// host of package githubfake, one host for each case.
func conformanceHarness(auth forge.AuthKind) forgetest.Factory {
	return func(t *testing.T) forgetest.Harness {
		t.Helper()

		host := newHost(t)
		o := newHostOpener(t, host)
		conn, cred := hostConnection(t, host, auth)

		fg, err := o.Open(t.Context(), conn, cred)
		if err != nil {
			t.Fatalf("Open = %v", err)
		}

		account := forge.Account{ID: "777", Login: "acme", ExpiresAt: time.Time{}, NoExpiry: false, Scopes: nil}
		if auth == forge.AuthApp {
			account.ID, account.Login = "99", "acme-org"
		}

		return forgetest.Harness{
			Opener: o, Connection: conn, Credential: cred, EventSecret: []byte(randomHex(t, 32)), Forge: fg,
			Account: account, Repo: conformanceRepo, Fixture: &hostFixture{host: host, repo: conformanceRepo},
		}
	}
}
