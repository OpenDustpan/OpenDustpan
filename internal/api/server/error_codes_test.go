package server_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errUnclassified is an error that no classify function maps.
var errUnclassified = errors.New("disk on fire")

// TestWriteErrorCodes proves that each error that the shared classify
// functions map answers its status, its code of the catalog and the
// arguments of the code, also when the error is wrapped.
func TestWriteErrorCodes(t *testing.T) {
	t.Parallel()

	active := &maintenance.ActiveWorkError{Work: []string{"pulumi/acme/web/prod", "opentofu/acme/net"}, Canceled: nil}

	tests := []writeErrorRow{
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized, errcode.AuthUnauthenticated, nil, ""},
		{"denied", identity.ErrDenied, http.StatusForbidden, errcode.AccessDenied, nil, ""},
		{
			"collection over the cap",
			&access.CollectionUnitCapError{Key: access.SettingCollectionUnitCap, Count: 10001, Cap: 10000},
			http.StatusUnprocessableEntity, errcode.AccessCollectionUnitCap,
			map[string]any{"count": int64(10001), "cap": int64(10000), "key": access.SettingCollectionUnitCap},
			"",
		},
		{"invalid object", identity.ErrInvalidObject, http.StatusBadRequest, errcode.RequestInvalidObject, nil, ""},
		{
			"invalid body", apiserver.ErrInvalidBodyForTest, http.StatusBadRequest, errcode.RequestBodyInvalid, nil,
			"invalid JSON body",
		},
		{
			"invalid override switches body", apiserver.ErrOverrideSwitchesBodyForTest, http.StatusBadRequest,
			errcode.RequestBodyInvalid, nil,
			`invalid override switches body: provide {"allowForcedWrites":true|false,"allowUnlockedWrites":true|false}`,
		},
		{
			"invalid namespace name",
			namespace.ErrInvalidName,
			http.StatusBadRequest,
			errcode.NamespaceInvalidName,
			nil,
			"",
		},
		{
			"invalid namespace body", apiserver.ErrInvalidNamespaceBodyForTest, http.StatusBadRequest,
			errcode.RequestBodyInvalid, nil, `invalid namespace body: provide {"name":"..."}`,
		},
		{
			"invalid namespace page",
			namespace.ErrInvalidPage,
			http.StatusBadRequest,
			errcode.RequestPageSizeInvalid,
			nil,
			"",
		},
		{
			"invalid namespace cursor", apiserver.ErrNamespaceCursorForTest, http.StatusBadRequest,
			errcode.RequestPageTokenInvalid, nil, "invalid cursor: provide the nextCursor of the page before",
		},
		{"namespace exists", namespace.ErrAlreadyExists, http.StatusConflict, errcode.NamespaceAlreadyExists, nil, ""},
		{"fenced", namespace.ErrFenced, http.StatusConflict, errcode.NamespaceFenced, nil, ""},
		{"namespace not found", namespace.ErrNotFound, http.StatusNotFound, errcode.NamespaceNotFound, nil, ""},
		{"invalid state", maintenance.ErrInvalidState, http.StatusBadRequest, errcode.MaintenanceInvalidState, nil, ""},
		{
			"active work", active, http.StatusConflict, errcode.MaintenanceActiveWork,
			map[string]any{"workCount": int64(2), "canceledCount": int64(0)},
			"",
		},
		{
			"active work without units", maintenance.ErrActiveWork, http.StatusConflict, errcode.MaintenanceActiveWork,
			map[string]any{"workCount": int64(0), "canceledCount": int64(0)},
			"",
		},
		{
			"busy", maintenance.ErrBusy, http.StatusServiceUnavailable, errcode.MaintenanceBusy, nil,
			"cannot enter maintenance mode: the writes in flight did not end in time: try again later",
		},
		{"held", maintenance.ErrHeld, http.StatusConflict, errcode.MaintenanceHeld, nil, ""},
		{
			"in maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable,
			errcode.MaintenanceInMaintenance, nil, "",
		},
		{
			"state unavailable", maintenance.ErrUnavailable, http.StatusInternalServerError,
			errcode.MaintenanceUnavailable, nil, "",
		},
		{
			"removal alert unrecorded", signinsvc.ErrRemovalAlertUnrecorded, http.StatusServiceUnavailable,
			errcode.PrincipalRemovalAlertUnrecorded, nil,
			"the removal alert and its audit entry could not be written",
		},
		{"unclassified", errUnclassified, http.StatusInternalServerError, errcode.ServerInternal, nil, ""},
	}

	for _, tc := range tests {
		for _, err := range []error{tc.err, fmt.Errorf("serve the route: %w", tc.err)} {
			t.Run(tc.name+"/"+err.Error(), func(t *testing.T) {
				t.Parallel()

				checkWriteErrorRow(t, tc, err)
			})
		}
	}
}

// writeErrorRow is one row of TestWriteErrorCodes: the error, the status, the
// code and the arguments that its answer holds, and its message when the row
// names one.
type writeErrorRow struct {
	name    string
	err     error
	status  int
	code    string
	args    map[string]any
	message string
}

// checkWriteErrorRow writes err and checks the status, the code and the
// arguments of the answer and, when the row names a message, its message.
func checkWriteErrorRow(t *testing.T, tc writeErrorRow, err error) {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/x", http.NoBody)
	rec := httptest.NewRecorder()
	apiserver.WriteErrorForTest(rec, r, err)

	if rec.Code != tc.status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
		errcodetest.Want{Code: tc.code, Args: tc.args, RequestBody: nil})

	// Only the rows that name a message check it. The namespace paths answer
	// the text of the error they got, which is the wrapped text when the error
	// is wrapped; the request paths answer the sentinel text.
	if tc.message == "" {
		return
	}

	var body struct {
		Message string `json:"message"`
	}

	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &body); decodeErr != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), decodeErr)
	}

	if body.Message != tc.message && body.Message != err.Error() {
		t.Fatalf("message = %q, want %q or %q: %s", body.Message, tc.message, err.Error(), rec.Body.String())
	}
}

// TestWriteErrorWithoutCode proves that an error that its classify function
// gives no code yet keeps its status and its message, and answers without
// errorCode.
func TestWriteErrorWithoutCode(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/x", http.NoBody)
	rec := httptest.NewRecorder()
	apiserver.WriteErrorForTest(rec, r, audit.ErrChainMigrationsStopped)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}

	if _, ok := body["errorCode"]; ok || rec.Code != http.StatusServiceUnavailable ||
		body["message"] != audit.ErrChainMigrationsStopped.Error() {
		t.Fatalf("answer = %d %s, want 503 with the message and no errorCode", rec.Code, rec.Body.String())
	}
}

// TestEnterErrorCodes proves that a forced enter that canceled work answers
// the code of the active work with its counts, and keeps the list of the
// canceled units.
func TestEnterErrorCodes(t *testing.T) {
	t.Parallel()

	active := &maintenance.ActiveWorkError{
		Work:     []string{"pulumi/acme/web/prod"},
		Canceled: []string{"opentofu/acme/net", "opentofu/acme/dns"},
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/maintenance", http.NoBody)
	rec := httptest.NewRecorder()
	apiserver.WriteEnterErrorForTest(rec, r, active)

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusConflict, rec.Body.Bytes(), errcodetest.Want{
		Code:        errcode.MaintenanceActiveWork,
		Args:        map[string]any{"workCount": int64(1), "canceledCount": int64(2)},
		RequestBody: nil,
	})

	var body struct {
		Canceled []string `json:"canceled"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !slices.Equal(body.Canceled, active.Canceled) {
		t.Fatalf("canceled = %v, want %v: %s", body.Canceled, active.Canceled, rec.Body.String())
	}
}

// TestUnknownRouteCode proves that a path that no route serves answers 404
// with the code of an unknown route.
func TestUnknownRouteCode(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/no-such-route", http.NoBody)
	rec := httptest.NewRecorder()
	apiserver.ServeEmptyForTest(rec, r)

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusNotFound, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.ServerRouteNotFound, Args: nil, RequestBody: nil})
}
