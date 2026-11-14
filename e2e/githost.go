//go:build e2e

package e2e

import (
	"context"
	"crypto/x509"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	// GitHostAppID is the ID of the app that the fake knows.
	GitHostAppID = githubfake.DefaultAppID
	// GitHostInstallationID is the ID of the installation that the fake knows.
	GitHostInstallationID = githubfake.DefaultInstallationID
)

// e2eCheckBudget is the number of checks for each installation in a minute
// that the scenario can make.
const e2eCheckBudget = 1000

// GitHostFake is a fake GitHub API on a TLS test server, the one of package
// githubfake. It holds the key of one app, verifies the JWT of each request
// with it, and knows one installation. It makes no call to the real GitHub.
type GitHostFake struct {
	fake *githubfake.Fake
}

// NewGitHostFake starts the fake. It stops when the test ends.
func NewGitHostFake(tb testing.TB) *GitHostFake {
	tb.Helper()

	return &GitHostFake{fake: githubfake.New(tb, githubfake.Config{
		AppID:          "",
		AppSlug:        "",
		InstallationID: "",
		Owner:          githubfake.Account{ID: 0, Login: ""},
		User: githubfake.Account{
			ID:    0,
			Login: "",
		},
		UserToken:     "",
		ClassicToken:  false,
		Permissions:   nil,
		ContentsLimit: 0,
		Key:           nil,
		Now:           nil,
		WebhookClient: nil,
	})}
}

// URL is the API address of the fake: an https address on the loopback.
func (g *GitHostFake) URL() string { return g.fake.URL() }

// PrivateKeyPEM is the private key of the fake app, as GitHub gives it.
func (g *GitHostFake) PrivateKeyPEM() string { return g.fake.PrivateKeyPEM() }

// Calls is the number of requests that the fake answered.
func (g *GitHostFake) Calls() int { return g.fake.Calls() }

func (g *GitHostFake) certPool() *x509.CertPool { return g.fake.CertPool() }

// gitHostFakes maps a test to its fake, so that the in-process server of that
// test trusts the fake and may reach the loopback.
var gitHostFakes sync.Map //nolint:gochecknoglobals // Test registry, keyed by the test.

// fakeOf returns the fake that the registry holds. The registry holds nothing else.
func fakeOf(v any) *GitHostFake {
	fake, ok := v.(*GitHostFake)
	if !ok {
		return nil
	}

	return fake
}

// NewEnvWithGitHost starts a server as NewEnv does, with the Git host routes
// on, the loopback allowed for Git host connections, and the certificate of
// fake trusted. The server binary variant is not supported.
func NewEnvWithGitHost(ctx context.Context, tb testing.TB, store Store, fake *GitHostFake) *Env {
	tb.Helper()

	gitHostFakes.Store(tb, fake)
	tb.Cleanup(func() { gitHostFakes.Delete(tb) })

	// The writes of the registration and of an installation need a console
	// session with a recent sign-in.
	return NewEnvWithRealTimeSignIn(ctx, tb, NoCLI(), store)
}

// gitHostOptions returns the options that give the in-process server of tb the
// Git host store of st, and the fake of tb when it has one.
func gitHostOptions(tb testing.TB, st serverStore) []dustpan.Option {
	tb.Helper()

	var opts []dustpan.Option

	switch s := st.(type) {
	case *sqlite.Store:
		opts = append(opts, dustpan.WithGitHostStore(s.GitHost()))
	case *postgres.Store:
		opts = append(opts, dustpan.WithGitHostStore(s.GitHost()))
	default:
		return nil
	}

	v, found := gitHostFakes.Load(tb)
	if !found {
		return opts
	}

	if fake := fakeOf(v); fake != nil {
		opts = append(opts,
			dustpan.WithGitHostLoopbackForTests(),
			// The scenario checks one connection many times in a second.
			dustpan.WithGitHostCheckLimits(forge.CheckLimits{
				PerConnection: time.Nanosecond, PerInstallation: e2eCheckBudget, PerApp: e2eCheckBudget,
				Window: time.Minute,
			}),
			dustpan.WithGitHostRootCAs(fake.certPool()),
		)
	}

	return opts
}
