package selfservice_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/job"
)

// repositoryCase is a repository of a home, a module or a git delivery. A
// credential, a scheme other than https, a query or a fragment is refused.
// secret is a part of the repository that the error must never hold.
type repositoryCase struct {
	name   string
	repo   string
	valid  bool
	secret string
}

func repositoryCases() []repositoryCase {
	withPassword := url.URL{
		Scheme: "https", User: url.UserPassword("ci", "hunter2"), Host: "git.example.com", Path: "/p/infra.git",
	}

	return []repositoryCase{
		{name: "a path", repo: "payments/infra", valid: true, secret: ""},
		{name: "an https URL", repo: "https://git.example.com/payments/infra.git", valid: true, secret: ""},
		{
			name: "an https URL with a user and a password", repo: withPassword.String(),
			valid: false, secret: "hunter2",
		},
		{
			name: "an https URL with a token as the user", repo: "https://ghp_tok3n@git.example.com/p/infra.git",
			valid: false, secret: "ghp_tok3n",
		},
		{
			name: "an https URL with a token in the query", repo: "https://git.example.com/p/infra.git?token=s3cr3t",
			valid: false, secret: "s3cr3t",
		},
		{name: "an scp-like address", repo: "git@git.example.com:p/infra.git", valid: false, secret: "git@"},
		{name: "an http URL", repo: "http://git.example.com/p/infra.git", valid: false, secret: ""},
		{name: "an ssh URL", repo: "ssh://git@git.example.com/p/infra.git", valid: false, secret: ""},
		{name: "a fragment", repo: "https://git.example.com/p/infra.git#main", valid: false, secret: ""},
	}
}

// checkRepositoryError fails when err does not match the validity of tc, or
// when a refusal holds the secret of the repository.
func checkRepositoryError(t *testing.T, err error, tc repositoryCase) {
	t.Helper()

	if tc.valid {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}

		return
	}

	if !errors.Is(err, selfservice.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}

	if tc.secret != "" && strings.Contains(err.Error(), tc.secret) {
		t.Fatalf("err = %q, want no part of the repository", err)
	}
}

// TestHomeRefusesARepositoryWithACredential proves that a home in git
// refuses a repository that holds a credential or is not a path or an https
// URL, so that a request document never stores a credential.
func TestHomeRefusesARepositoryWithACredential(t *testing.T) {
	t.Parallel()

	for _, tc := range repositoryCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := selfservice.Home{
				Kind: selfservice.HomeGit, Connection: "", Repository: tc.repo, Ref: "main",
				Delivery: job.DeliveryReviewRequest,
			}

			checkRepositoryError(t, h.Validate(), tc)
		})
	}
}

// TestBlueprintRefusesARepositoryWithACredential proves that a module from git
// and the repositories of the git delivery of a blueprint refuse a repository
// that holds a credential or is not a path or an https URL, so that a
// blueprint never stores a credential.
func TestBlueprintRefusesARepositoryWithACredential(t *testing.T) {
	t.Parallel()

	fields := map[string]func(b *selfservice.Blueprint, repo string){
		"module": func(b *selfservice.Blueprint, repo string) {
			b.Program = selfservice.Program{Module: &selfservice.Module{
				Source: "", Version: "", Connection: "", Repository: repo, Directory: "postgres", Revision: "v2.4.1",
			}, Component: nil}
		},
		"git delivery": func(b *selfservice.Blueprint, repo string) {
			b.Delivery.Git.Repositories = []string{"payments/infra", repo}
		},
	}

	for field, set := range fields {
		for _, tc := range repositoryCases() {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				_, err := selfservice.ParseBlueprint(edit(t, func(b *selfservice.Blueprint) { set(b, tc.repo) }))
				checkRepositoryError(t, err, tc)
			})
		}
	}
}
