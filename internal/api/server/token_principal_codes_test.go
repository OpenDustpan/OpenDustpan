package server_test

import (
	"fmt"
	"net/http"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestWriteErrorTokenAndPrincipalCodes sends each sentinel of the token and
// principal classify functions through writeError and checks the status, the
// code, the arguments and the message of the answer, also when the error is
// wrapped.
func TestWriteErrorTokenAndPrincipalCodes(t *testing.T) {
	t.Parallel()

	bodyLimit := map[string]any{"maxBytes": int64(16384)}

	tests := []writeErrorRow{
		{
			"rate limited", identity.ErrRateLimited, http.StatusTooManyRequests, errcode.AuthRateLimited, nil,
			"too many failed sign-ins from this address: try again later",
		},
		{"token body", apiserver.ErrTokenBodyForTest, http.StatusBadRequest, errcode.RequestBodyInvalid, nil, ""},
		{"rotate body", apiserver.ErrRotateBodyForTest, http.StatusBadRequest, errcode.RequestBodyInvalid, nil, ""},
		{"policy body", apiserver.ErrPolicyBodyForTest, http.StatusBadRequest, errcode.RequestBodyInvalid, nil, ""},
		{
			"break-glass setting", apiserver.ErrBreakGlassGoneForTest, http.StatusBadRequest,
			errcode.RequestBodyInvalid, nil, "",
		},
		{
			"principal token body", apiserver.ErrPrincipalTokenBodyForTest, http.StatusBadRequest,
			errcode.RequestBodyInvalid, nil, "",
		},
		{"token query", apiserver.ErrTokenQueryForTest, http.StatusBadRequest, errcode.RequestInvalidQuery, nil, ""},
		{"token invalid", accesstoken.ErrInvalid, http.StatusBadRequest, errcode.TokenInvalid, nil, ""},
		{
			"kind gets no token", accesstoken.ErrNoTokenForKind, http.StatusBadRequest,
			errcode.TokenKindHasNone, nil, "",
		},
		{"token not found", accesstoken.ErrNotFound, http.StatusNotFound, errcode.TokenNotFound, nil, ""},
		{
			"token principal not found", accesstoken.ErrPrincipalNotFound, http.StatusNotFound,
			errcode.PrincipalNotFound, nil, "",
		},
		{"wrong kind", accesstoken.ErrWrongKind, http.StatusBadRequest, errcode.TokenWrongKind, nil, ""},
		{
			"no own principal", accesstoken.ErrNoOwnPrincipal, http.StatusForbidden,
			errcode.TokenCallerNotPerson, nil, "",
		},
		{"sign-in required", accesstoken.ErrSignInRequired, http.StatusForbidden, errcode.TokenRequiresSignIn, nil, ""},
		{"token ended", accesstoken.ErrEnded, http.StatusConflict, errcode.TokenEnded, nil, ""},
		{"token exists", accesstoken.ErrAlreadyExists, http.StatusConflict, errcode.TokenAlreadyExists, nil, ""},
		{
			"no token store", apiserver.ErrNoTokenStoreForTest, http.StatusNotImplemented,
			errcode.TokenUnsupported, nil, "",
		},
		{
			"principal body", apiserver.ErrPrincipalBodyForTest, http.StatusBadRequest,
			errcode.RequestBodyInvalid, nil, "",
		},
		{
			"principal change body", apiserver.ErrPrincipalChangeBodyForTest, http.StatusBadRequest,
			errcode.RequestBodyInvalid, nil, "",
		},
		{"group body", apiserver.ErrGroupBodyForTest, http.StatusBadRequest, errcode.RequestBodyInvalid, nil, ""},
		{
			"group change body", apiserver.ErrGroupChangeBodyForTest, http.StatusBadRequest,
			errcode.RequestBodyInvalid, nil, "",
		},
		{"member body", apiserver.ErrMemberBodyForTest, http.StatusBadRequest, errcode.RequestBodyInvalid, nil, ""},
		{
			"namespace principal body", apiserver.ErrNamespacePrincipalBodyForTest, http.StatusBadRequest,
			errcode.RequestBodyInvalid, nil, "",
		},
		{"principal invalid", principal.ErrInvalid, http.StatusBadRequest, errcode.PrincipalInvalid, nil, ""},
		{
			"reserved subject", principal.ErrReservedSubject, http.StatusBadRequest,
			errcode.PrincipalReservedSubject, nil, "",
		},
		{"page query", apiserver.ErrPageQueryForTest, http.StatusBadRequest, errcode.RequestPageSizeInvalid, nil, ""},
		{
			"body too large", apiserver.BodyTooLargeForTest(16384), http.StatusRequestEntityTooLarge,
			errcode.RequestBodyTooLarge, bodyLimit, "",
		},
		{"principal exists", principal.ErrAlreadyExists, http.StatusConflict, errcode.PrincipalAlreadyExists, nil, ""},
		{
			"managed by directory", principal.ErrManagedByDirectory, http.StatusConflict,
			errcode.PrincipalManagedByDirectory, nil, "",
		},
		{"not disabled", principal.ErrNotDisabled, http.StatusConflict, errcode.PrincipalNotDisabled, nil, ""},
		{"disabled", principal.ErrDisabled, http.StatusConflict, errcode.PrincipalDisabled, nil, ""},
		{
			"last site administrator", access.ErrLastSiteAdministrator, http.StatusConflict,
			errcode.AccessLastSiteAdministrator, nil, "",
		},
		{
			"too few working break-glass accounts", principal.ErrNoWorkingBreakGlass, http.StatusConflict,
			errcode.PrincipalNoWorkingBreakGlass, nil,
			"The change leaves too few working break-glass accounts. " +
				"Add a break-glass account or register a passkey for one, then try again. " +
				"See the break-glass accounts guide.",
		},
		{"principal not found", principal.ErrNotFound, http.StatusNotFound, errcode.PrincipalNotFound, nil, ""},
		{
			"no principal store", apiserver.ErrNoPrincipalStoreForTest, http.StatusNotImplemented,
			errcode.PrincipalUnsupported, nil, "",
		},
	}

	for i := range tests {
		tc := &tests[i]
		if tc.message == "" {
			tc.message = tc.err.Error()
		}

		for _, err := range []error{tc.err, fmt.Errorf("serve the route: %w", tc.err)} {
			t.Run(tc.name+"/"+err.Error(), func(t *testing.T) {
				t.Parallel()

				checkWriteErrorRow(t, *tc, err)
			})
		}
	}
}
