package inventory_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// errDriver stands for an error of a store driver, which no classify
// function knows.
var errDriver = errors.New("driver detail")

// codeCase is one error that a route of the inventory API can answer, with
// the status and the code of the catalog that the answer must carry.
type codeCase struct {
	name   string
	err    error
	status int
	want   errcodetest.Want
}

// checkCodes fails when the answer of an error of cases does not have its
// status, its code and its arguments. Each error is wrapped once, as a service
// wraps it.
func checkCodes(t *testing.T, cases []codeCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			apiinventory.WriteError(t.Context(), rec, fmt.Errorf("serve the route: %w", tc.err))

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(), tc.want)
		})
	}
}

// code returns the Want of a code without arguments.
func code(c string) errcodetest.Want {
	return errcodetest.Want{Code: c, Args: nil, RequestBody: nil}
}

// TestRequestAndCollectionErrorsHaveCodes fails for an error of the
// authentication, of a request or of a collection that answers without its
// code.
func TestRequestAndCollectionErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	checkCodes(t, []codeCase{
		{"rate limited", identity.ErrRateLimited, http.StatusTooManyRequests, code(errcode.AuthRateLimited)},
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized, code(errcode.AuthUnauthenticated)},
		{"csrf", sessionauth.ErrCSRF, http.StatusForbidden, code(errcode.SessionCSRF)},
		{"other site", sessionauth.ErrOtherSite, http.StatusForbidden, code(errcode.SessionOtherSite)},
		{"denied", identity.ErrDenied, http.StatusForbidden, code(errcode.AccessDenied)},
		{
			"missing organization", apiinventory.ErrMissingOrganization, http.StatusBadRequest,
			code(errcode.RequestInvalidQuery),
		},
		{"missing unit", apiinventory.ErrMissingUnit, http.StatusBadRequest, code(errcode.RequestInvalidQuery)},
		{
			"output sharing body", apiinventory.ErrOutputSharingBody, http.StatusBadRequest,
			code(errcode.RequestBodyInvalid),
		},
		{
			"collection body", apiinventory.ErrCollectionBody, http.StatusBadRequest,
			code(errcode.RequestBodyInvalid),
		},
		{"move body", apiinventory.ErrMoveBody, http.StatusBadRequest, code(errcode.RequestBodyInvalid)},
		{"invalid namespace name", namespace.ErrInvalidName, http.StatusBadRequest, code(errcode.NamespaceInvalidName)},
		{"invalid object", identity.ErrInvalidObject, http.StatusBadRequest, code(errcode.RequestInvalidObject)},
		{"namespace not found", namespace.ErrNotFound, http.StatusNotFound, code(errcode.NamespaceNotFound)},
		{"unit not found", coreinventory.ErrUnitNotFound, http.StatusNotFound, code(errcode.UnitNotFound)},
		{"unit not in the inventory", collection.ErrUnitNotFound, http.StatusNotFound, code(errcode.UnitNotFound)},
		{
			"invalid collection name", collection.ErrInvalidName, http.StatusBadRequest,
			code(errcode.CollectionInvalidName),
		},
		{"collection not found", collection.ErrNotFound, http.StatusNotFound, code(errcode.CollectionNotFound)},
		{
			"placement rule collection not found", collection.ErrPlacementCollectionMissing,
			http.StatusNotFound, code(errcode.CollectionNotFound),
		},
		{
			"collection exists", collection.ErrAlreadyExists, http.StatusConflict,
			code(errcode.CollectionAlreadyExists),
		},
		{"collection not empty", collection.ErrNotEmpty, http.StatusConflict, code(errcode.CollectionNotEmpty)},
		{"collection changed", collection.ErrCollectionChanged, http.StatusConflict, code(errcode.CollectionChanged)},
		{"no collection", collection.ErrNoCollection, http.StatusConflict, code(errcode.UnitNoCollection)},
		{
			"move out of a locked collection", collection.ErrMoveOutOfLockedCollection, http.StatusConflict,
			code(errcode.CollectionMoveOutLocked),
		},
		{"unknown error", errDriver, http.StatusInternalServerError, code(errcode.ServerInternal)},
	})
}

// TestOutputReadErrorsHaveCodes fails for an error of a read of outputs that
// answers without its code, or without the unit that its text names.
func TestOutputReadErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	const unit = "pulumi:acme/web/prod"

	obj, err := identity.ParseObject(unit)
	if err != nil {
		t.Fatalf("parse %s: %v", unit, err)
	}

	cases := []codeCase{
		{
			"client encrypted", outputshare.ErrClientEncrypted, http.StatusConflict,
			code(errcode.UnitClientEncrypted),
		},
		{
			"no state", outputsharesvc.ErrNoState, http.StatusNotFound,
			errcodetest.Want{Code: errcode.UnitNoState, Args: map[string]any{"unit": unit}, RequestBody: nil},
		},
		{"state moved", outputshare.ErrStateMoved, http.StatusConflict, code(errcode.UnitStateMoved)},
		{"unknown producer", outputshare.ErrUnknownProducer, http.StatusNotFound, code(errcode.UnitNotFound)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
			apiinventory.WriteOutputsError(rec, req, obj, fmt.Errorf("read outputs: %w", tc.err))

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(), tc.want)
		})
	}
}
