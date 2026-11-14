// Package github is the adapter of the Forge port for GitHub and for GitHub
// Enterprise Server, which has another API address. It supports the auth
// kinds app (a GitHub App installation) and token (a fine-grained
// personal access token).
//
// The adapter does Check, Head, ReadFile, Compare, Commit, FindCommit,
// OpenReview, ReadReview, CloseReview, DeleteBranch and SetStatus now. Each other
// method returns forge.ErrUnsupported, and Capabilities does not list it, until
// the method lands. Every request
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
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
)

// schemeHTTPS is the one scheme that the adapter accepts for an address.
const schemeHTTPS = "https"

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
	// Prover seals the proof that Commit adds to a commit, and opens the proof
	// that FindCommit reads. Nil, or a Prover that is not Ready, means that the
	// adapter cannot prove a commit: Capabilities then does not list
	// forge.CapCommit, and Commit and FindCommit return forge.ErrUnsupported.
	Prover Prover
	// AppBudget bounds the calls that the key of an app signs. All Forges of
	// the Opener share it. Nil means a budget of forge.DefaultCheckPerApp
	// calls in forge.DefaultCheckWindow: the adapter never signs with no
	// budget.
	AppBudget *forge.AppBudget
}

// Opener makes a forge.Forge for a GitHub connection. It implements
// forge.Opener and is safe for concurrent use.
type Opener struct {
	rules  egress.Rules
	client *http.Client
	now    func() time.Time
	prover Prover
	budget *forge.AppBudget
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

	keepRedirectStatus(client)

	budget := cfg.AppBudget
	if budget == nil {
		var err error

		budget, err = forge.NewAppBudget(forge.DefaultCheckPerApp, forge.DefaultCheckWindow)
		if err != nil {
			return nil, err
		}
	}

	return &Opener{rules: cfg.Rules, client: client, now: now, prover: cfg.Prover, budget: budget}, nil
}

// redirectError is the error of a redirect that the rules refuse. It keeps
// the status of the redirect, so that a read can tell a repository that has
// a new name from any other redirect. The adapter still follows none.
type redirectError struct {
	status int
	err    error
}

func (e *redirectError) Error() string { return e.err.Error() }

func (e *redirectError) Unwrap() error { return e.err }

// keepRedirectStatus wraps the redirect check of client, which refuses every
// redirect, so that its error carries the status of the redirect.
func keepRedirectStatus(client *http.Client) {
	inner := client.CheckRedirect

	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		var err error
		if inner != nil {
			err = inner(req, via)
		}

		if err != nil && req.Response != nil {
			return &redirectError{status: req.Response.StatusCode, err: err}
		}

		return err
	}
}

// canProve reports whether p is a Prover that has its key.
func canProve(p Prover) bool {
	return p != nil && p.Ready()
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

	f := &gitHub{
		o:              o,
		base:           base,
		push:           c.Push,
		auth:           c.Auth,
		appID:          "",
		installationID: "",
		token:          "",
		key:            nil,
		mu:             sync.Mutex{},
		readTokens:     map[string]readToken{},
		owner:          "",
		contents:       contentsUnknown,
		writes:         contentsUnknown,
		pulls:          contentsUnknown,
		statuses:       contentsUnknown,
		issues:         contentsUnknown,
		identity:       "",
		identityID:     "",
	}

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
	if err != nil || u.Scheme != schemeHTTPS || u.Host == "" || u.RawQuery != "" || u.ForceQuery {
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

	// mu guards the fields below. readTokens holds the installation tokens
	// of an app connection for reads: a token reads one repository, and the
	// adapter keeps it in memory only. owner is the login of the account of
	// the installation. contents says whether the installation has the
	// permission to read the contents, and writes whether it has the
	// permission to write them. Check and the first read set them.
	mu         sync.Mutex
	readTokens map[string]readToken
	owner      string
	contents   contentsAccess
	writes     contentsAccess
	pulls      contentsAccess
	statuses   contentsAccess
	issues     contentsAccess
	// identity is the login that the connection commits as. FindCommit reads
	// it once, see commitIdentity.
	identity string
	// identityID is the numeric ID of the account of identity. UpsertComment
	// reads it once, see commentIdentity.
	identityID string
}

// contentsAccess says whether an installation has the permission to read
// the contents of its repositories.
type contentsAccess int

const (
	// contentsUnknown is the state before any call has shown the permissions.
	contentsUnknown contentsAccess = iota
	contentsGranted
	contentsMissing
)

// Capabilities lists what the adapter does for this connection, with no
// call to GitHub. Each method that is not in the adapter returns
// forge.ErrUnsupported and has no capability here. A method that lands adds
// its capability here, and a connection with no push access gets neither
// forge.CapCommit nor forge.CapReview.
//
// It lists forge.CapFiles unless the last Check showed an installation
// without the permission to read the contents. It lists forge.CapCommit for a
// connection with push access and a Prover in the Config, unless the last Check showed an installation
// without the permission to write the contents. Before a Check, the
// permissions are not known, and the capabilities are listed. A token
// connection always lists them, because a token shows no permissions. It lists
// forge.CapStatus for a connection with push access, unless the last Check
// showed an installation without the permission to write the commit statuses.
func (f *gitHub) Capabilities() forge.Capabilities {
	f.mu.Lock()
	defer f.mu.Unlock()

	app := f.auth == forge.AuthApp
	caps := forge.Capabilities{}

	if !app || f.contents != contentsMissing {
		caps = append(caps, forge.CapFiles, forge.CapCompare, forge.CapTree)
	}

	return append(caps, f.pushCapabilities()...)
}

// unsupported returns the error of a method that the adapter does not have.
func unsupported(method string) error {
	return fmt.Errorf("%w: GitHub %s is not available yet", forge.ErrUnsupported, method)
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

// pushCapabilities lists the capabilities that need push access. The caller
// holds the lock.
func (f *gitHub) pushCapabilities() forge.Capabilities {
	app := f.auth == forge.AuthApp

	var caps forge.Capabilities

	if !f.push {
		return caps
	}

	if canProve(f.o.prover) && (!app || f.writes != contentsMissing) {
		caps = append(caps, forge.CapCommit)
	}

	if !app || (f.writes != contentsMissing && f.pulls != contentsMissing) {
		caps = append(caps, forge.CapReview)
	}

	if !app || f.statuses != contentsMissing {
		caps = append(caps, forge.CapStatus)
	}

	if !app || f.issues != contentsMissing {
		caps = append(caps, forge.CapComment)
	}

	return caps
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
