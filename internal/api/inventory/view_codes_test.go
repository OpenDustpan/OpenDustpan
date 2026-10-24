package inventory_test

import (
	"net/http"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// TestViewErrorsHaveCodes fails for an error of the saved view routes that
// answers without its code.
func TestViewErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	checkCodes(t, []codeCase{
		{"not a person", savedview.ErrNotPerson, http.StatusForbidden, code(errcode.ViewNotPerson)},
		{"view not found", savedview.ErrNotFound, http.StatusNotFound, code(errcode.ViewNotFound)},
		{"invalid view", savedview.ErrInvalid, http.StatusBadRequest, code(errcode.ViewInvalid)},
		{"view body", apiinventory.ErrViewBody, http.StatusBadRequest, code(errcode.RequestBodyInvalid)},
		{
			"idempotency key", apiinventory.ErrViewIdempotencyKey, http.StatusBadRequest,
			code(errcode.RequestIdempotencyKeyUnavailable),
		},
		{"view exists", savedview.ErrExists, http.StatusConflict, code(errcode.ViewAlreadyExists)},
		{"view limit", savedview.ErrLimit, http.StatusConflict, code(errcode.ViewLimit)},
	})
}
