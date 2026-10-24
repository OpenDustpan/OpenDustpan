package objectcontext_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apicontext "opendustpan.org/opendustpan/internal/api/objectcontext"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// errDriver stands for an error of a store driver, which no classify
// function knows.
var errDriver = errors.New("driver detail")

// code returns the Want of a code without arguments.
func code(c string) errcodetest.Want {
	return errcodetest.Want{Code: c, Args: nil, RequestBody: nil}
}

// TestContextErrorsHaveCodes fails for an error of the context routes that
// answers without its status, its code or its arguments. Each error is
// wrapped once, as a service wraps it.
func TestContextErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		err    error
		status int
		want   errcodetest.Want
	}{
		{"rate limited", identity.ErrRateLimited, http.StatusTooManyRequests, code(errcode.AuthRateLimited)},
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized, code(errcode.AuthUnauthenticated)},
		{"csrf", sessionauth.ErrCSRF, http.StatusForbidden, code(errcode.SessionCSRF)},
		{"other site", sessionauth.ErrOtherSite, http.StatusForbidden, code(errcode.SessionOtherSite)},
		{"denied", identity.ErrDenied, http.StatusForbidden, code(errcode.AccessDenied)},
		{"unknown object", apicontext.ErrNotFound, http.StatusNotFound, code(errcode.ContextObjectNotFound)},
		{"unknown namespace", namespace.ErrNotFound, http.StatusNotFound, code(errcode.ContextObjectNotFound)},
		{
			"purged unit", objectcontext.ErrInvalidObject, http.StatusNotFound,
			code(errcode.ContextObjectNotFound),
		},
		{"no API context", objectcontext.ErrNotFound, http.StatusNotFound, code(errcode.ContextNotSet)},
		{"body", apicontext.ErrBody, http.StatusBadRequest, code(errcode.RequestBodyInvalid)},
		{"body text", apicontext.ErrBodyText, http.StatusBadRequest, code(errcode.RequestBodyInvalid)},
		{"include", apicontext.ErrInclude, http.StatusBadRequest, code(errcode.RequestInvalidQuery)},
		{"invalid context", objectcontext.ErrInvalidContext, http.StatusBadRequest, code(errcode.ContextInvalid)},
		{
			"rule error", &objectcontext.RuleError{Rule: "summary must be valid UTF-8"}, http.StatusBadRequest,
			code(errcode.ContextInvalid),
		},
		{
			"unknown section", objectcontext.ErrUnknownSection, http.StatusBadRequest,
			code(errcode.ContextUnknownSection),
		},
		{
			"field from code", &objectcontext.FieldFromCodeError{Field: objectcontext.FieldOwners},
			http.StatusConflict,
			errcodetest.Want{
				Code: errcode.ContextFieldFromCode, Args: map[string]any{"field": "owners"}, RequestBody: nil,
			},
		},
		{
			"field from code without the field", objectcontext.ErrFieldFromCode, http.StatusInternalServerError,
			code(errcode.ServerInternal),
		},
		{"unit deleted", objectcontext.ErrUnitDeleted, http.StatusConflict, code(errcode.ContextUnitDeleted)},
		{"fenced", namespace.ErrFenced, http.StatusConflict, code(errcode.NamespaceFenced)},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, code(errcode.NamespaceNotHome)},
		{
			"in maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable,
			code(errcode.MaintenanceInMaintenance),
		},
		{"unknown error", errDriver, http.StatusInternalServerError, code(errcode.ServerInternal)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			apicontext.WriteError(t.Context(), rec, fmt.Errorf("serve the route: %w", tc.err))

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(), tc.want)
		})
	}
}
