package tfe_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// stubStates serves the States port over a workspace service. It reports the
// current state version as the latest state, and a state the client encrypted
// when encrypted names that version.
type stubStates struct {
	workspaces tfe.WorkspaceService
	encrypted  workspace.StateVersionID
}

var _ outputshare.States = stubStates{workspaces: nil, encrypted: ""}

func (s stubStates) Latest(ctx context.Context, u identity.Object) (outputshare.State, error) {
	name, err := workspace.ParseName(u.UnitName())
	if err != nil {
		return outputshare.State{ID: "", ClientEncrypted: false}, errors.Join(outputshare.ErrUnknownProducer, err)
	}

	ws, err := s.workspaces.Get(ctx, workspace.Ref{Namespace: u.Namespace(), Name: name})
	if err != nil || ws.ID.String() != u.UnitID() {
		return outputshare.State{ID: "", ClientEncrypted: false}, outputshare.ErrUnknownProducer
	}

	sv, err := s.workspaces.Current(ctx, ws.ID)
	if errors.Is(err, workspace.ErrNoState) {
		return outputshare.State{ID: "", ClientEncrypted: false}, nil
	}

	if err != nil {
		return outputshare.State{ID: "", ClientEncrypted: false}, err
	}

	return outputshare.State{ID: sv.ID.String(), ClientEncrypted: sv.ID == s.encrypted}, nil
}

// newTestOutputChecker returns the output sharing checker over the stub
// States of workspaces, with authz as the authorizer.
func newTestOutputChecker(
	t *testing.T,
	authz identity.Authorizer,
	workspaces tfe.WorkspaceService,
) *outputsharesvc.Checker {
	t.Helper()

	return newTestOutputCheckerWith(t, authz, stubStates{workspaces: workspaces, encrypted: ""})
}

// newTestOutputCheckerWith returns the output sharing checker over states.
func newTestOutputCheckerWith(
	t *testing.T, authz identity.Authorizer, states outputshare.States,
) *outputsharesvc.Checker {
	t.Helper()

	checker, err := outputsharesvc.NewChecker(outputsharesvc.CheckerConfig{
		Authorizer: authz, States: states, Namespaces: newTestNamespaces(t), Audit: newTestAudit(),
		Logger: slog.New(slog.DiscardHandler), Now: nil,
	})
	if err != nil {
		t.Fatalf("output checker: %v", err)
	}

	return checker
}
