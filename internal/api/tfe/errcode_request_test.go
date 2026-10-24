package tfe_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestRequestErrorCodes proves the code of a missing credential, of each
// refusal, and of each request or state that is not valid.
func TestRequestErrorCodes(t *testing.T) {
	t.Parallel()

	none := func(code string) errcodetest.Want {
		return errcodetest.Want{Code: code, Args: nil, RequestBody: nil}
	}

	checkErrorCodes(t, []errorCodeCase{
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized, none(errcode.AuthUnauthenticated)},
		{
			"override refused", workspace.NewOverrideRefusedError(workspace.OverrideForced, "write refused"),
			http.StatusForbidden, none(errcode.WorkspaceOverrideRefused),
		},
		{"csrf", sessionauth.ErrCSRF, http.StatusForbidden, none(errcode.SessionCSRF)},
		{"other site", sessionauth.ErrOtherSite, http.StatusForbidden, none(errcode.SessionOtherSite)},
		{"denied", identity.ErrDenied, http.StatusForbidden, none(errcode.AccessDenied)},
		{"upload denied", workspace.ErrUploadDenied, http.StatusForbidden, none(errcode.StateVersionUploadDenied)},
		{"invalid body", tfe.ErrInvalidBodyForTest, http.StatusUnprocessableEntity, none(errcode.RequestBodyInvalid)},
		{
			"invalid query", tfe.ErrInvalidQueryForTest, http.StatusUnprocessableEntity,
			none(errcode.RequestInvalidQuery),
		},
		{"missing filter", tfe.ErrMissingFilterForTest, http.StatusBadRequest, none(errcode.RequestInvalidQuery)},
		{
			"reveal path too long", tfe.ErrRevealPathTooLongForTest, http.StatusUnprocessableEntity,
			errcodetest.Want{
				Code: errcode.StateVersionRevealPathTooLong, Args: map[string]any{"maxBytes": int64(1024)},
				RequestBody: nil,
			},
		},
		{"invalid reason", audit.ErrInvalidReason, http.StatusUnprocessableEntity, none(errcode.AuditReasonInvalid)},
		{
			"invalid workspace name", workspace.ErrInvalidName, http.StatusUnprocessableEntity,
			none(errcode.WorkspaceInvalidName),
		},
		{
			"invalid namespace name", namespace.ErrInvalidName, http.StatusUnprocessableEntity,
			none(errcode.NamespaceInvalidName),
		},
		{
			"invalid object", identity.ErrInvalidObject, http.StatusUnprocessableEntity,
			none(errcode.RequestInvalidObject),
		},
		{"invalid state", workspace.ErrInvalidState, http.StatusUnprocessableEntity, none(errcode.StateVersionInvalid)},
		{
			"stale serial", workspace.ErrStaleSerial, http.StatusUnprocessableEntity,
			none(errcode.StateVersionStaleSerial),
		},
		{
			"lineage mismatch", workspace.ErrLineageMismatch, http.StatusUnprocessableEntity,
			none(errcode.StateVersionLineageMismatch),
		},
		{
			"checksum mismatch", workspace.ErrChecksumMismatch, http.StatusUnprocessableEntity,
			none(errcode.StateVersionChecksumMismatch),
		},
	})
}
