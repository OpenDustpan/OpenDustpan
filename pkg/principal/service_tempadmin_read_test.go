package principal_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

var errTempAdminDown = errors.New("temp admin store down")

// failingTempAdminReader is a TempAdmins whose read fails.
type failingTempAdminReader struct{}

func (failingTempAdminReader) IsTempAdmin(context.Context, string) (bool, error) {
	return false, errTempAdminDown
}

// TestSignInPrincipalRefusesAFailedTempAdminRead proves that a failed read of
// the temporary administrators refuses the sign-in of an existing person with
// ErrTempAdminRead and the cause, for an existing person.
func TestSignInPrincipalRefusesAFailedTempAdminRead(t *testing.T) {
	t.Parallel()

	svc, err := principal.NewService(principal.Config{
		TempAdmins: failingTempAdminReader{}, Store: openStore(t), Guard: principal.AdminOnlyGuard{},
		Namespaces: nil, Tokens: nil, GroupReach: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	_, err = svc.CreatePrincipal(t.Context(), testEntry(),
		principal.Input{Subject: "corp:sub-1", DisplayName: "", Kind: principal.KindPerson})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, _, err = svc.SignInPrincipal(t.Context(), testEntry(), principal.SignInInput{
		Subject: "corp:sub-1", Source: principal.SourceOIDC, Names: nil, Create: false,
	})
	if !errors.Is(err, principal.ErrTempAdminRead) || !errors.Is(err, errTempAdminDown) {
		t.Fatalf("error = %v, want ErrTempAdminRead holding the cause", err)
	}

	if errors.Is(err, principal.ErrNotSignInTarget) {
		t.Fatalf("a failed read must not read as a decision about the person: %v", err)
	}
}
