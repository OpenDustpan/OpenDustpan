// Package github is the adapter of the Forge port for GitHub and for GitHub
// Enterprise Server, which has another API address. It supports the auth
// kinds app (a GitHub App installation) and token (a fine-grained
// personal access token).
//
// The adapter does Check now. Each other method returns forge.ErrUnsupported,
// and Capabilities does not list it, until the method lands. Every request
// goes through the egress rules: the address of the API passes the URL
// check when a connection opens and the address check when the network
// connection opens, and the adapter follows no redirect.
//
// The adapter never logs a secret and never puts a part of the private key,
// the JWT or a token in an error.
package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
)

// Config holds the settings of an Opener.
type Config struct {
	// Rules are the outbound rules, with the ranges that the operator
	// allows. The zero value allows no range.
	Rules egress.Rules
	// RootCAs holds the certificate authorities that the adapter trusts.
	// Nil means the system roots. A test sets it for a local TLS server.
	RootCAs *x509.CertPool
	// Now returns the time. Nil means time.Now.
	Now func() time.Time
}

// Opener makes a forge.Forge for a GitHub connection. It implements
// forge.Opener and is safe for concurrent use.
type Opener struct {
	rules  egress.Rules
	client *http.Client
	now    func() time.Time
}

var _ forge.Opener = (*Opener)(nil)

// NewOpener returns an Opener.
func NewOpener(cfg Config) (*Opener, error) {
	for _, p := range cfg.Rules.Allow {
		if !p.IsValid() {
			return nil, fmt.Errorf("%w: an allowed address range is not valid", forge.ErrInvalid)
		}
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	client := egresshttp.ClientWith(
		cfg.Rules,
		egress.NoRedirect,
		egress.HTTPSOnly,
		egresshttp.Options{RootCAs: cfg.RootCAs, Resolver: nil},
	)

	return &Opener{rules: cfg.Rules, client: client, now: now}, nil
}

// CloseIdleConnections closes the idle network connections of the adapter.
func (o *Opener) CloseIdleConnections() {
	o.client.CloseIdleConnections()
}

// Kind returns forge.KindGitHub.
func (*Opener) Kind() forge.Kind { return forge.KindGitHub }

// Open returns the Forge of c with the credential cred. It makes no call to
// GitHub. It returns forge.ErrInvalid for a connection of another kind, an
// auth kind other than app and token, a missing or bad parameter, an API
// address that is not an https address that the egress rules allow, and a
// secret that it cannot use.
func (o *Opener) Open( //nolint:ireturn // The port returns the Forge interface.
	_ context.Context, c forge.Connection, cred forge.Credential,
) (forge.Forge, error) {
	if c.Kind != forge.KindGitHub {
		return nil, fmt.Errorf(
			"%w: the GitHub adapter cannot open a connection of the kind %q",
			forge.ErrInvalid,
			c.Kind,
		)
	}

	base, err := o.baseURL(c.BaseURL)
	if err != nil {
		return nil, err
	}

	f := &gitHub{o: o, base: base, push: c.Push, auth: c.Auth, appID: "", installationID: "", token: "", key: nil}

	switch c.Auth {
	case forge.AuthApp:
		if err := f.openApp(c, cred); err != nil {
			return nil, err
		}
	case forge.AuthToken:
		if len(cred.Secret) == 0 {
			return nil, fmt.Errorf("%w: the GitHub token is empty", forge.ErrInvalid)
		}

		f.token = string(cred.Secret)
	default:
		return nil, fmt.Errorf("%w: the GitHub adapter does not support the auth kind %q", forge.ErrInvalid, c.Auth)
	}

	return f, nil
}

// baseURL checks the API address and returns it with no trailing slash.
func (o *Opener) baseURL(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: the GitHub API address is empty", forge.ErrInvalid)
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("%w: the GitHub API address must be an https address with no query", forge.ErrInvalid)
	}

	// Defense in depth: every request passes this check again in the HTTP
	// client, and the dial guard checks the resolved address. This check
	// stays so that a connection with a refused address never opens.
	if err := o.rules.CheckURL(raw, egress.HTTPSOnly); err != nil {
		return "", fmt.Errorf("%w: the GitHub API address is not allowed: %w", forge.ErrInvalid, err)
	}

	return strings.TrimRight(raw, "/"), nil
}

// gitHub is a forge.Forge for one GitHub connection and one credential.
type gitHub struct {
	o              *Opener
	key            *rsa.PrivateKey
	base           string
	auth           forge.AuthKind
	appID          string
	installationID string
	token          string
	push           bool
}

// Capabilities lists what the adapter does for this connection, with no
// call to GitHub. It lists no capability yet: each other method returns
// forge.ErrUnsupported until it lands. A method that lands adds its
// capability here, and a connection with no push access gets neither
// forge.CapCommit nor forge.CapReview.
func (f *gitHub) Capabilities() forge.Capabilities {
	return forge.Capabilities{}
}

// unsupported returns the error of a method that the adapter does not have.
func unsupported(method string) error {
	return fmt.Errorf("%w: GitHub %s is not available yet", forge.ErrUnsupported, method)
}

// Head returns forge.ErrUnsupported.
func (*gitHub) Head(context.Context, string, string) (forge.Commit, error) {
	return forge.Commit{}, unsupported("Head")
}

// ReadFile returns forge.ErrUnsupported.
func (*gitHub) ReadFile(context.Context, string, string, string, int64) (forge.File, error) {
	return forge.File{}, unsupported("ReadFile")
}

// Compare returns forge.ErrUnsupported.
func (*gitHub) Compare(context.Context, string, string, string, int64) (forge.Comparison, error) {
	return forge.Comparison{}, unsupported("Compare")
}

// Commit returns forge.ErrUnsupported.
func (*gitHub) Commit(context.Context, string, forge.CommitRequest) (forge.Commit, error) {
	return forge.Commit{}, unsupported("Commit")
}

// FindCommit returns forge.ErrUnsupported.
func (*gitHub) FindCommit(context.Context, string, string, string, time.Time) (forge.Commit, error) {
	return forge.Commit{}, unsupported("FindCommit")
}

// OpenReview returns forge.ErrUnsupported.
func (*gitHub) OpenReview(context.Context, string, forge.ReviewRequest) (forge.Review, error) {
	return forge.Review{}, unsupported("OpenReview")
}

// ReadReview returns forge.ErrUnsupported.
func (*gitHub) ReadReview(context.Context, string, string) (forge.Review, error) {
	return forge.Review{}, unsupported("ReadReview")
}

// CloseReview returns forge.ErrUnsupported.
func (*gitHub) CloseReview(context.Context, string, string) error {
	return unsupported("CloseReview")
}

// RepoToken returns forge.ErrUnsupported.
func (*gitHub) RepoToken(context.Context, string) (forge.Token, error) {
	return forge.Token{}, unsupported("RepoToken")
}

// ParseEvent returns forge.ErrUnsupported.
func (*gitHub) ParseEvent(context.Context, forge.EventRequest, [][]byte) (forge.Event, error) {
	return forge.Event{}, unsupported("ParseEvent")
}

// RotateSelf returns forge.ErrUnsupported: GitHub has no call that rotates
// a token.
func (*gitHub) RotateSelf(context.Context, time.Time) (forge.RotatedToken, error) {
	return forge.RotatedToken{}, unsupported("RotateSelf")
}

// openApp reads the app parameters and the private key of an app connection.
func (f *gitHub) openApp(c forge.Connection, cred forge.Credential) error {
	f.appID, f.installationID = c.Params[forge.ParamAppID], c.Params[forge.ParamInstallationID]

	if !validID(f.appID) {
		return fmt.Errorf("%w: the GitHub App ID is missing or not a number", forge.ErrInvalid)
	}

	if !validID(f.installationID) {
		return fmt.Errorf("%w: the GitHub installation ID is missing or not a number", forge.ErrInvalid)
	}

	key, err := parsePrivateKey(cred.Secret)
	if err != nil {
		return err
	}

	f.key = key

	return nil
}
