package forgetest

import (
	"bytes"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// repoTokenLife is the longest life that the port allows for a token with
// Scoped set.
const repoTokenLife = time.Hour

// scopedToken asks for the token of the repository of the case and checks the
// fields that every scoped token has.
func scopedToken(t *testing.T, h Harness) forge.Token {
	t.Helper()

	tok, err := h.Forge.RepoToken(t.Context(), h.Repo)
	if err != nil {
		t.Fatalf("RepoToken = %v", err)
	}

	if !tok.Scoped || tok.Username == "" || len(tok.Value) == 0 {
		t.Fatalf(
			"RepoToken = user %q, scoped %v, %d bytes; want a scoped token with a user name",
			tok.Username,
			tok.Scoped,
			len(tok.Value),
		)
	}

	if bytes.Equal(tok.Value, h.Credential.Secret) {
		t.Fatal("RepoToken answered the secret of the credential of the connection")
	}

	return tok
}

func caseRepoTokenReadsOneRepository(t *testing.T, h Harness) {
	t.Helper()

	tok := scopedToken(t, h)

	own, err := h.Fixture.TokenAccess(t.Context(), tok.Value, h.Repo)
	if err != nil {
		t.Fatalf("access to the repository: %v", err)
	}

	if !own.Read {
		t.Fatal("the token does not read its repository")
	}

	other, err := h.Fixture.Clone(t.Context())
	if err != nil {
		t.Fatalf("clone: %v", err)
	}

	got, err := h.Fixture.TokenAccess(t.Context(), tok.Value, other)
	if err != nil {
		t.Fatalf("access to the other repository: %v", err)
	}

	if got.Read || got.Write {
		t.Fatalf("the token reaches another repository of the account: %+v", got)
	}
}

func caseRepoTokenCannotWrite(t *testing.T, h Harness) {
	t.Helper()

	tok := scopedToken(t, h)

	got, err := h.Fixture.TokenAccess(t.Context(), tok.Value, h.Repo)
	if err != nil {
		t.Fatalf("access to the repository: %v", err)
	}

	if got.Write {
		t.Fatal("the token writes its repository")
	}
}

func caseRepoTokenExpires(t *testing.T, h Harness) {
	t.Helper()

	before := h.now()
	tok := scopedToken(t, h)

	if tok.ExpiresAt.IsZero() || !tok.ExpiresAt.After(before) || tok.ExpiresAt.After(h.now().Add(repoTokenLife)) {
		t.Fatalf("ExpiresAt = %v, want after %v and at most one hour after the call", tok.ExpiresAt, before)
	}
}

func caseRepoTokenOfAToken(t *testing.T, h Harness) {
	t.Helper()

	if h.Connection.Auth != forge.AuthToken {
		t.Skip("only a token connection answers its own token")
	}

	tok, err := h.Forge.RepoToken(t.Context(), h.Repo)
	if err != nil {
		t.Fatalf("RepoToken = %v", err)
	}

	if tok.Scoped || tok.Username == "" || !bytes.Equal(tok.Value, h.Credential.Secret) {
		t.Fatalf(
			"RepoToken = user %q, scoped %v; want the own token of the connection, not scoped",
			tok.Username,
			tok.Scoped,
		)
	}
}
