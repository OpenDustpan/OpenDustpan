package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestCodedErrorAnswer proves that a coded error answers through the catalog:
// a valid error keeps its status, its message, its code and its arguments, and
// an error that the catalog refuses answers the internal error and not the
// status, the code or the arguments of the error.
func TestCodedErrorAnswer(t *testing.T) {
	t.Parallel()

	tooLong := strings.Repeat("a", errcode.MaxIdentifier+1)

	tests := []struct {
		name    string
		err     error
		status  int
		code    string
		args    map[string]any
		message string
	}{
		{
			"code without arguments",
			apiserver.CodedErrorForTest(http.StatusConflict, errcode.GitHostAppNotRegistered, "not registered"),
			http.StatusConflict, errcode.GitHostAppNotRegistered, nil, "not registered",
		},
		{
			"code with an argument",
			apiserver.CodedErrorForTest(http.StatusBadRequest, errcode.PreferencesFieldInvalid, "bad field",
				errcode.Identifier("field", "theme")),
			http.StatusBadRequest, errcode.PreferencesFieldInvalid,
			map[string]any{"field": "theme"},
			"bad field",
		},
		{
			"code not in the catalog",
			apiserver.CodedErrorForTest(http.StatusConflict, "git-host.no-such-code", "not registered"),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
		{
			"empty code",
			apiserver.CodedErrorForTest(http.StatusConflict, "", "not registered"),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
		{
			"argument that the code does not declare",
			apiserver.CodedErrorForTest(http.StatusConflict, errcode.GitHostAppNotRegistered, "not registered",
				errcode.Identifier("field", "theme")),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
		{
			"declared argument that is missing",
			apiserver.CodedErrorForTest(http.StatusBadRequest, errcode.PreferencesFieldInvalid, "bad field"),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
		{
			"argument given two times",
			apiserver.CodedErrorForTest(http.StatusBadRequest, errcode.PreferencesFieldInvalid, "bad field",
				errcode.Identifier("field", "theme"), errcode.Identifier("field", "language")),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
		{
			"argument of another kind",
			apiserver.CodedErrorForTest(http.StatusBadRequest, errcode.PreferencesFieldInvalid, "bad field",
				errcode.Quantity("field", 1)),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
		{
			"empty identifier",
			apiserver.CodedErrorForTest(http.StatusBadRequest, errcode.PreferencesFieldInvalid, "bad field",
				errcode.Identifier("field", "")),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
		{
			"identifier over the limit",
			apiserver.CodedErrorForTest(http.StatusBadRequest, errcode.PreferencesFieldInvalid, "bad field",
				errcode.Identifier("field", tooLong)),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
		{
			"identifier with a line break",
			apiserver.CodedErrorForTest(http.StatusBadRequest, errcode.PreferencesFieldInvalid, "bad field",
				errcode.Identifier("field", "theme\nlanguage")),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/server/x", http.NoBody)
			rec := httptest.NewRecorder()
			apiserver.WriteErrorForTest(rec, r, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: tc.args, RequestBody: nil})

			if !strings.Contains(rec.Body.String(), `"message":"`+tc.message+`"`) {
				t.Fatalf("answer = %s, want message %q", rec.Body.String(), tc.message)
			}
		})
	}
}
