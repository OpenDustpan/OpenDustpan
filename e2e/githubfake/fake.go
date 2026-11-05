// Package githubfake is a fake GitHub host for the tests of OpenDustpan. It
// serves, on a local TLS server, the REST and GraphQL calls that the GitHub
// adapter of the Forge port makes, and it sends webhook events that are
// signed as GitHub signs them: the header X-Hub-Signature-256 holds
// "sha256=" and the hex HMAC-SHA256 of the body with the event secret, and
// the header X-GitHub-Delivery holds the delivery ID.
//
// The package is test infrastructure. Only test code imports it, and the
// binary never links it.
//
// The fake is deterministic. The clock starts at Epoch() and moves one second
// for each commit and each token. Commit IDs, blob IDs, token values and
// delivery IDs are hashes of counters, so two runs of one test see the same
// values. A blob ID is the real git blob ID of the content. The fake records
// each request in order (see Requests), so that a test can assert what the
// server sent. The record holds no credential: the header Authorization is
// replaced, and Request.Bearer names the kind of the credential.
//
// A repository is a flat map of paths to blobs for each commit. The fake
// has the calls below, which the adapter makes:
//
//	GET    /app/installations/{id}                     the installation (JWT)
//	POST   /app/installations/{id}/access_tokens       an installation token (JWT)
//	DELETE /installation/token                         revoke an installation token
//	GET    /user                                       the user of a token
//	GET    /repos/{owner}/{repo}/commits?sha=&since=   the commits of a ref, newest first, with page and per_page
//	GET    /repos/{owner}/{repo}/commits/{ref}         one commit by branch, tag or ID
//	GET    /repos/{owner}/{repo}/compare/{base}...{head} how head and base relate, with the merge base
//	GET    /repos/{owner}/{repo}/git/ref/heads/{name}  one branch
//	POST   /repos/{owner}/{repo}/git/refs              a new branch at a commit
//	GET    /repos/{owner}/{repo}/contents/{path}?ref=  a file, or a directory listing
//	GET    /repos/{owner}/{repo}/git/blobs/{sha}       a blob
//	GET    /repos/{owner}/{repo}                       the repository, with its ID and default branch
//	POST   /repos/{owner}/{repo}/pulls                 a pull request from a branch of the repository
//	GET    /repos/{owner}/{repo}/pulls?state=&head=&base= the pull requests, with per_page
//	GET    /repos/{owner}/{repo}/pulls/{number}        one pull request, with its mergeable state
//	PATCH  /repos/{owner}/{repo}/pulls/{number}        close a pull request
//	DELETE /repos/{owner}/{repo}/git/refs/heads/{name} delete a branch
//	POST   /graphql                                    the mutation createCommitOnBranch
//
// A call that the fake does not have answers 404. A test adds a call to the
// fake before an adapter method needs it.
package githubfake

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Epoch returns the time of the start of a fake whose Config has no clock.
func Epoch() time.Time {
	return time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
}

// The defaults of Config.
const (
	DefaultAppID          = "4242"
	DefaultInstallationID = "9001"
	DefaultUserToken      = ""
	rsaBits               = 2048
	defaultOwnerID        = 99
	defaultUserID         = 777
	defaultContentsLimit  = 100 << 20
)

// Account is an account of the fake: the owner of the installation, or the
// user of a token.
type Account struct {
	ID    int64
	Login string
}

// Config holds the settings of a Fake. A field with the zero value takes
// the default.
type Config struct {
	// AppID is the ID of the app that the fake knows (DefaultAppID).
	AppID string
	// InstallationID is the ID of the one installation that the fake knows
	// (DefaultInstallationID).
	InstallationID string
	// Owner is the account of the installation (ID 99, login "acme-org").
	Owner Account
	// User is the account that the user token belongs to (ID 777, login
	// "acme").
	User Account
	// UserToken is the one token of a user that the fake accepts. Empty
	// means the fake accepts no user token.
	UserToken string
	// ClassicToken makes UserToken a classic personal access token: the call
	// for the user answers the header X-OAuth-Scopes. A fine-grained token
	// has no scopes, so by default the fake sends no such header.
	ClassicToken bool
	// Permissions are the permissions of the installation (contents read,
	// metadata read). A test that commits through an installation token
	// gives contents write.
	Permissions map[string]string
	// ContentsLimit is the largest file that the contents call answers. For a
	// larger file, GitHub answers 403 with the error code too_large
	// (100 MiB).
	ContentsLimit int
	// Key is the private key of the app. Nil makes a new key of 2048 bits.
	Key *rsa.PrivateKey
	// Now returns the time. Nil means a clock that starts at Epoch() and moves
	// one second at each call.
	Now func() time.Time
	// WebhookClient sends the events (Send). Nil means a client with a ten
	// second timeout that follows no redirect and trusts the system roots.
	WebhookClient *http.Client
}

// Fake is a fake GitHub host. It is safe for concurrent use.
type Fake struct {
	srv *httptest.Server
	key *rsa.PrivateKey
	cfg Config

	mu         sync.Mutex
	repos      map[string]*repo
	moved      map[string]string
	tokens     map[string]*installationToken
	requests   []Request
	deliveries []Sent
	faults     faults
	tick       int64
	counter    uint64
	repoSeq    int64
}

// New starts a fake. It stops when the test ends.
func New(tb testing.TB, cfg Config) *Fake {
	tb.Helper()

	cfg = withDefaults(cfg)

	if cfg.Key == nil {
		key, err := rsa.GenerateKey(rand.Reader, rsaBits)
		if err != nil {
			tb.Fatalf("make the key of the fake GitHub App: %v", err)
		}

		cfg.Key = key
	}

	f := new(Fake)
	f.key, f.cfg = cfg.Key, cfg
	f.repos, f.tokens = map[string]*repo{}, map[string]*installationToken{}
	f.moved = map[string]string{}

	f.srv = httptest.NewUnstartedServer(f.handler())
	f.srv.Config.ErrorLog = log.New(io.Discard, "", 0) // A client that closes its connection is no news.
	f.srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	f.srv.StartTLS()

	tb.Cleanup(f.srv.Close)

	return f
}

func withDefaults(cfg Config) Config {
	if cfg.AppID == "" {
		cfg.AppID = DefaultAppID
	}

	if cfg.InstallationID == "" {
		cfg.InstallationID = DefaultInstallationID
	}

	if cfg.Owner.Login == "" {
		cfg.Owner = Account{ID: defaultOwnerID, Login: "acme-org"}
	}

	if cfg.User.Login == "" {
		cfg.User = Account{ID: defaultUserID, Login: "acme"}
	}

	if cfg.ContentsLimit == 0 {
		cfg.ContentsLimit = defaultContentsLimit
	}

	if cfg.Permissions == nil {
		cfg.Permissions = map[string]string{"contents": levelNameRead, "metadata": levelNameRead}
	}

	return cfg
}

// URL is the API address of the fake: an https address on the loopback.
func (f *Fake) URL() string { return f.srv.URL }

// AppID is the ID of the app that the fake knows.
func (f *Fake) AppID() string { return f.cfg.AppID }

// InstallationID is the ID of the installation that the fake knows.
func (f *Fake) InstallationID() string { return f.cfg.InstallationID }

// UserToken is the token of the user that the fake accepts.
func (f *Fake) UserToken() string { return f.cfg.UserToken }

// CertPool holds the certificate of the fake, so that a client trusts it.
func (f *Fake) CertPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(f.srv.Certificate())

	return pool
}

// PrivateKey is the private key of the app.
func (f *Fake) PrivateKey() *rsa.PrivateKey { return f.key }

// PrivateKeyPEM is the private key of the app, as GitHub gives it.
func (f *Fake) PrivateKeyPEM() string {
	block := &pem.Block{Type: "RSA PRIVATE KEY", Headers: nil, Bytes: x509.MarshalPKCS1PrivateKey(f.key)}

	return string(pem.EncodeToMemory(block))
}

// now returns the next time of the clock. The caller holds the lock.
func (f *Fake) now() time.Time {
	if f.cfg.Now != nil {
		return f.cfg.Now()
	}

	f.tick++

	return Epoch().Add(time.Duration(f.tick) * time.Second)
}

// next returns the next number of the counter. The caller holds the lock.
func (f *Fake) next() uint64 {
	f.counter++

	return f.counter
}
