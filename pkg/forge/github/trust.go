package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/forge"
)

// webFlow is the login that GitHub gives to the committer of a commit that
// GitHub makes itself, as the mutation createCommitOnBranch does.
const webFlow = "web-flow"

// botSuffix ends the login of the bot account of a GitHub App.
const botSuffix = "[bot]"

// maxLoginBytes is the longest login that the adapter accepts.
const maxLoginBytes = 100

// validLogin checks a login of GitHub with an allow list: ASCII letters,
// digits, "-" and "_" (the logins of Enterprise Managed Users have "_"), and
// the suffix "[bot]" for a bot. The login is host data
// that is not trusted.
func validLogin(login string) bool {
	name := strings.TrimSuffix(login, botSuffix)
	if name == "" || len(login) > maxLoginBytes {
		return false
	}

	for i := range len(name) {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}

	return true
}

// commitIdentity returns the login that the connection commits as: the bot
// account of the app for an app connection, and the user of the token for a
// token connection. GitHub names it as the author of each commit that the
// mutation createCommitOnBranch makes. The adapter asks GitHub once and keeps
// the answer in memory.
func (f *gitHub) commitIdentity(ctx context.Context) (string, error) {
	f.mu.Lock()
	known := f.identity
	f.mu.Unlock()

	if known != "" {
		return known, nil
	}

	var (
		login string
		err   error
	)

	if f.auth == forge.AuthApp {
		login, err = f.appLogin(ctx)
	} else {
		login, err = f.tokenLogin(ctx)
	}

	if err != nil {
		return "", err
	}

	// Defense in depth: the two readers above build the login from an answer
	// that is host data. This check stays so that a login that fails it is
	// never the identity that decides which commit counts.
	if !validLogin(login) {
		return "", forge.Unavailable(0, "GitHub gave an answer with no valid login")
	}

	f.mu.Lock()
	f.identity = login
	f.mu.Unlock()

	return login, nil
}

// appLogin reads the slug of the app with the JWT and returns the login of
// its bot account.
func (f *gitHub) appLogin(ctx context.Context) (string, error) {
	jwt, err := signJWT(f.key, f.appID, f.o.now())
	if err != nil {
		return "", err
	}

	if spendErr := f.spendApp(); spendErr != nil {
		return "", spendErr
	}

	resp, err := f.do(ctx, http.MethodGet, "/app", jwt, nil)
	if err != nil {
		return "", err
	}

	var app struct {
		Slug string `json:"slug"`
	}

	if decodeErr := decode(resp.body, &app); decodeErr != nil {
		return "", decodeErr
	}

	// The slug has no "[" : the suffix is added here, never read from the answer.
	if app.Slug == "" || strings.ContainsAny(app.Slug, "[]") || !validLogin(app.Slug) {
		return "", forge.Unavailable(0, "GitHub gave an answer with no valid app")
	}

	return app.Slug + botSuffix, nil
}

// tokenLogin reads the user of the token and returns its login.
func (f *gitHub) tokenLogin(ctx context.Context) (string, error) {
	resp, err := f.do(ctx, http.MethodGet, "/user", f.token, nil)
	if err != nil {
		return "", err
	}

	var user account
	if decodeErr := decode(resp.body, &user); decodeErr != nil {
		return "", decodeErr
	}

	acct, err := accountOf(user)
	if err != nil {
		return "", err
	}

	return acct.Login, nil
}

// untrusted returns the error for a commit that has the trailer and fails a
// check. The text names the commit and the check. It holds no text of the
// host, because the host is not trusted. The commit ID is host data too, but
// listCommits checks it with forge.ValidateCommitID before any commit gets
// here, so it holds only hex digits.
func untrusted(c listedCommit, reason string) error {
	return fmt.Errorf("%w: the commit %s %s", forge.ErrUntrustedCommit, c.SHA, reason)
}

// trustedCommit returns nil when the connection made c, and an error that
// wraps forge.ErrUntrustedCommit when it did not. identity is the login that
// the connection commits as. Each check fails closed, but the checks are not
// independent layers against a push: GitHub sets the logins of the author and
// of the committer from the email of the commit, which the pusher writes. A
// pusher who writes the noreply emails of the identity and of GitHub passes
// checkAuthor and checkCommitter. Only checkVerified stops that commit,
// because GitHub signs a commit only when it makes the commit itself.
func trustedCommit(c listedCommit, identity string) error {
	for _, check := range []func(listedCommit, string) error{checkAuthor, checkCommitter, checkVerified} {
		if err := check(c, identity); err != nil {
			return err
		}
	}

	return nil
}

// checkAuthor requires that the author of c is the identity of the connection.
// GitHub names the author of a commit from createCommitOnBranch as the app
// bot or as the user of the token. The login comes from the email of the
// commit, so this check alone does not stop a push with that email: see
// trustedCommit.
func checkAuthor(c listedCommit, identity string) error {
	if c.Author == nil || !validLogin(c.Author.Login) || c.Author.Login != identity {
		return untrusted(c, "has another author than the identity of the connection")
	}

	return nil
}

// checkCommitter requires that the committer of c is GitHub (web-flow), as
// createCommitOnBranch always makes it. The identity of the connection is not
// a committer that the adapter accepts: no call of the adapter makes such a
// commit. The login comes from the email of the commit, so this check alone
// does not stop a push: see trustedCommit.
func checkCommitter(c listedCommit, _ string) error {
	if c.Committer == nil || !validLogin(c.Committer.Login) || c.Committer.Login != webFlow {
		return untrusted(c, "has a committer that is not GitHub")
	}

	return nil
}

// checkVerified requires that GitHub reports the verification of c and shows
// c as verified. A report that is missing or null, or that has no true
// "verified", is a commit that is not verified: github.com and GitHub
// Enterprise Server report the verification of every commit, so an answer
// without it is not an answer to trust. This is the check that stops a push
// with the emails of the identity and of GitHub, so it is required for
// every connection.
func checkVerified(c listedCommit, _ string) error {
	if c.Commit.Verification == nil || c.Commit.Verification.Verified == nil || !*c.Commit.Verification.Verified {
		return untrusted(c, "is not verified by GitHub")
	}

	return nil
}
