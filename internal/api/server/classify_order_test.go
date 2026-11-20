package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestClassifyOrderKeepsTheAnswers sends the errors that wrap both an error
// of the principal classifiers and an error of the role, grant or token
// classifiers. The role, grant and token classifiers run before the state
// errors of a principal, and after its request and break-glass errors. Each
// error keeps the answer that it had before that order.
func TestClassifyOrderKeepsTheAnswers(t *testing.T) {
	t.Parallel()

	tests := []writeErrorRow{
		{
			// The access token service refuses a token for the namespace
			// service principal with both errors.
			"no token for the namespace service principal",
			fmt.Errorf("%w: the namespace service principal gets no access token: %w",
				accesstoken.ErrNoTokenForKind, principal.ErrReservedSubject),
			http.StatusBadRequest, errcode.PrincipalReservedSubject, nil, "",
		},
		{
			// The access service refuses a grant for the namespace service
			// principal with both errors.
			"no grant for the namespace service principal",
			fmt.Errorf("%w: principal:prn-1 is the namespace service principal, which takes no grant: %w",
				access.ErrInvalid, principal.ErrReservedSubject),
			http.StatusBadRequest, errcode.PrincipalReservedSubject, nil, "",
		},
		{
			"last site administrator with an unknown principal",
			fmt.Errorf("%w: %w", access.ErrLastSiteAdministrator, principal.ErrNotFound),
			http.StatusConflict, errcode.AccessLastSiteAdministrator, nil, "",
		},
		{
			"last site administrator with an unknown grant",
			fmt.Errorf("%w: %w", access.ErrNotFound, access.ErrLastSiteAdministrator),
			http.StatusConflict, errcode.AccessLastSiteAdministrator, nil, "",
		},
		{
			"too few working break-glass accounts with an escalation",
			fmt.Errorf("%w: %w", access.ErrEscalation, principal.ErrNoWorkingBreakGlass),
			http.StatusConflict, errcode.PrincipalNoWorkingBreakGlass, nil,
			"The change leaves too few working break-glass accounts. " +
				"Add or enable a working break-glass account, then try again. " +
				"See the break-glass accounts guide.",
		},
	}

	for i := range tests {
		tc := &tests[i]
		if tc.message == "" {
			tc.message = tc.err.Error()
		}

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkWriteErrorRow(t, *tc, tc.err)
		})
	}
}
