package server_test

import (
	"log/slog"
	"testing"

	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// newTestSignInService returns the sign-in service over the store st, as the
// server builds it: no group applier, and the principal records, the session
// generations and the temporary administrators of st.
func newTestSignInService(
	t *testing.T, st *sqlite.Store, sessions *session.Sessions, principals *principal.Service,
) *signinsvc.Service {
	t.Helper()

	svc, err := signinsvc.New(signinsvc.Config{
		Principals:     principals,
		Disabler:       st,
		Generations:    st,
		Sessions:       sessions,
		Audit:          audit.ServerStore(st),
		TempAdmins:     st,
		Groups:         nil,
		Removals:       nil,
		States:         st,
		Logger:         slog.New(slog.DiscardHandler),
		OnAuditFailure: nil,
	})
	if err != nil {
		t.Fatalf("sign-in service: %v", err)
	}

	return svc
}
