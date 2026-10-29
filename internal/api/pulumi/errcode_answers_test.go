package pulumi_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// errorAnswer is one error and the answer that the adapter gives for it.
type errorAnswer struct {
	name   string
	err    error
	status int
	code   string
	args   map[string]any
}

// checkErrorAnswers proves that the error writer answers each error, also in
// a wrap, with its status, its code of the catalog and its arguments.
func checkErrorAnswers(t *testing.T, tests []errorAnswer) {
	t.Helper()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/user", http.NoBody)

			pulumi.WriteError(rec, req, fmt.Errorf("operation: %w", tc.err))

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: tc.args, RequestBody: nil})
		})
	}
}

// TestLayerErrorsCarryACode proves that the errors of the request, the
// namespace, the authentication and the maintenance layers answer their code.
func TestLayerErrorsCarryACode(t *testing.T) {
	t.Parallel()

	checkErrorAnswers(t, []errorAnswer{
		{"rate limited", identity.ErrRateLimited, http.StatusTooManyRequests, errcode.AuthRateLimited, nil},
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized, errcode.AuthUnauthenticated, nil},
		{"invalid token", token.ErrInvalid, http.StatusUnauthorized, errcode.AuthUnauthenticated, nil},
		{
			"unchecked token", stack.ErrAccessTokenUnchecked, http.StatusUnauthorized,
			errcode.AuthUnauthenticated, nil,
		},
		{"csrf", sessionauth.ErrCSRF, http.StatusForbidden, errcode.SessionCSRF, nil},
		{"other site", sessionauth.ErrOtherSite, http.StatusForbidden, errcode.SessionOtherSite, nil},
		{"denied", identity.ErrDenied, http.StatusForbidden, errcode.AccessDenied, nil},
		{"invalid body", pulumi.ErrInvalidBody, http.StatusBadRequest, errcode.RequestBodyInvalid, nil},
		{
			"continuation token", pulumi.ErrContinuationToken, http.StatusBadRequest,
			errcode.RequestPageTokenInvalid, nil,
		},
		{
			"reveal path", pulumi.ErrRevealPathTooLong, http.StatusBadRequest, errcode.StackRevealPathTooLong,
			map[string]any{"maxBytes": int64(1024)},
		},
		{"tag filter", pulumi.ErrStackTagFilter, http.StatusBadRequest, errcode.RequestInvalidQuery, nil},
		{"invalid object", identity.ErrInvalidObject, http.StatusBadRequest, errcode.RequestInvalidObject, nil},
		{
			"invalid namespace name", namespace.ErrInvalidName, http.StatusBadRequest,
			errcode.NamespaceInvalidName, nil,
		},
		{"namespace not found", namespace.ErrNotFound, http.StatusNotFound, errcode.NamespaceNotFound, nil},
		{"fenced", namespace.ErrFenced, http.StatusConflict, errcode.NamespaceFenced, nil},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, errcode.NamespaceNotHome, nil},
		{
			"unavailable", namespace.ErrUnavailable, http.StatusServiceUnavailable,
			errcode.NamespaceUnavailable, nil,
		},
		{
			"maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable,
			errcode.MaintenanceInMaintenance, nil,
		},
		{
			"binary below the minimum",
			&schema.MinimumError{SchemaMinimum: "v0.5.0", BinaryVersion: "v0.4.0"},
			http.StatusServiceUnavailable,
			errcode.SchemaBinaryBelowMinimum,
			map[string]any{"binaryVersion": "v0.4.0", "minimumVersion": "v0.5.0"},
		},
		{"unknown", errUnknown, http.StatusInternalServerError, errcode.ServerInternal, nil},
	})
}

// errUnknown is an error that no classify function knows.
var errUnknown = errors.New("an error that the adapter does not know")

// TestBodyRefusalsCarryACode proves that a body that is not JSON, and a
// missing namespace that equals the subject of the caller, answer their code
// on the routes that write the answer themselves.
func TestBodyRefusalsCarryACode(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespace(t, nsStore, "acme")
	seedStack(t, stStore, "acme", "web", "dev")

	handler := newUserHandler(t, nsStore, stStore, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	tests := []struct {
		name, method, target, body, code string
		status                           int
	}{
		{
			"create, not JSON", http.MethodPost, "/api/stacks/acme/web", `{`,
			errcode.RequestBodyInvalid, http.StatusBadRequest,
		},
		{
			"create, not an object", http.MethodPost, "/api/stacks/acme/web", `[]`,
			errcode.RequestBodyInvalid, http.StatusBadRequest,
		},
		{
			"rename, not JSON", http.MethodPost, "/api/stacks/acme/web/dev/rename", `{`,
			errcode.RequestBodyInvalid, http.StatusBadRequest,
		},
		{
			"tags, not JSON", http.MethodPatch, "/api/stacks/acme/web/dev/tags", `{`,
			errcode.RequestBodyInvalid, http.StatusBadRequest,
		},
		{
			"create in the namespace of the caller", http.MethodPost, "/api/stacks/tester/web",
			`{"stackName":"dev"}`, errcode.NamespaceNotFound, http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.target, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "token test-token")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: []byte(tc.body)})
		})
	}
}

// TestBelowMinimumNamesBothVersions proves that the answer to a write that a
// binary below the minimum made names both versions, as the Pulumi CLI shows
// the message to the user.
func TestBelowMinimumNamesBothVersions(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/user", http.NoBody)

	pulumi.WriteError(rec, req, fmt.Errorf("operation: %w",
		&schema.MinimumError{SchemaMinimum: "v0.5.0", BinaryVersion: "v0.4.0"}))

	for _, want := range []string{"v0.4.0", "v0.5.0"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body %s does not name %s", rec.Body.String(), want)
		}
	}
}
