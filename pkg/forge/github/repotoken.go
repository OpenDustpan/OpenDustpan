package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// repoTokenUser is the user name that GitHub expects with an installation
// token over HTTPS.
const repoTokenUser = "x-access-token"

// repoTokenLife is the longest life of a token that RepoToken answers.
const repoTokenLife = time.Hour

// RepoToken returns a token for the git protocol over HTTPS for repo.
//
// An app connection answers a new installation token for that repository
// only, with the permission to read the contents and no other, and an expiry
// of at most one hour from the call, with Scoped set. It never answers a
// cached token: the caller may give the token to a runner and needs the full
// life. It returns an error, and no token, when GitHub gives a token for more
// repositories or with more permissions than the request, or a token that has
// expired.
//
// A token connection answers its own token with Scoped not set, after one
// read of the repository, so that a missing repository is forge.ErrNotFound
// and a refused token is forge.ErrAuth.
//
// It returns forge.ErrInvalid for a repository name that fails the check of
// the other methods, before any call.
func (f *gitHub) RepoToken(ctx context.Context, repo string) (forge.Token, error) {
	r, err := splitRepo(repo)
	if err != nil {
		return forge.Token{}, err
	}

	if f.auth != forge.AuthApp {
		if _, readErr := f.repositoryID(ctx, r, f.token); readErr != nil {
			return forge.Token{}, readErr
		}

		return forge.Token{
			Username: repoTokenUser, Value: []byte(f.token), ExpiresAt: time.Time{}, Scoped: false,
		}, nil
	}

	now := f.o.now()

	// Defense in depth: the token request names the repository, and GitHub
	// refuses a repository that the installation cannot see. This check stays
	// so that a connection never asks for a token for a repository of another
	// account, whose name may equal the name of a repository of the
	// installation. It does not rely on GitHub or on the caller.
	owner, err := f.installationOwner(ctx)
	if err != nil {
		return forge.Token{}, err
	}

	if !strings.EqualFold(owner, r.owner) {
		return forge.Token{}, fmt.Errorf(
			"%w: the repository is not in the account of the installation",
			forge.ErrNotFound,
		)
	}

	t, err := f.mintToken(ctx, r.name, levelRead)
	if err != nil {
		return forge.Token{}, err
	}

	// A token that has expired is of no use, and a token with a wrong clock
	// would hide a fault of GitHub or of the server clock.
	if !t.expires.After(now) {
		return forge.Token{}, forge.Unavailable(0, "GitHub gave a token that has expired")
	}

	return forge.Token{
		Username:  repoTokenUser,
		Value:     []byte(t.value),
		ExpiresAt: earlier(t.expires, now.Add(repoTokenLife)),
		Scoped:    true,
	}, nil
}

// earlier returns the earlier of two times.
func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}

	return a
}
