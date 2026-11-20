package inventory_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestPlacementRuleWriteDenialRecords checks which failures of a placement
// rule write the handler records as denials. A refusal that a caller can
// repeat (a rule for a collection that the namespace does not hold, no
// collection ID, a collection ID of an invalid form) writes exactly one
// denial record. Every other failure writes none, so the denial audit and
// its limiter hold refusals only.
func TestPlacementRuleWriteDenialRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		status      int
		code        string
		wantRecords int
	}{
		{
			name:   "rule for a collection that the namespace does not hold",
			method: http.MethodPost, path: placementBase,
			body: ruleBody("web", "*", "elsewhere"), status: http.StatusNotFound,
			code: errcode.CollectionNotFound, wantRecords: 1,
		},
		{
			name:   "collection name instead of an ID on update",
			method: http.MethodPut, path: placementBase + "/plr-none",
			body:   `{"tool":"pulumi","project":"web","stack":"*","collection":"payments"}`,
			status: http.StatusBadRequest, code: errcode.PlacementCollectionIDRequired, wantRecords: 1,
		},
		{
			name:   "collection ID of an invalid form",
			method: http.MethodPost, path: placementBase,
			body: ruleBody("web", "*", "../x"), status: http.StatusBadRequest,
			code: errcode.PlacementInvalidCollectionID, wantRecords: 1,
		},
		{
			name:   "body with an unknown field",
			method: http.MethodPost, path: placementBase,
			body:   `{"tool":"pulumi","project":"web","stack":"*","collectionID":"col-payments","x":1}`,
			status: http.StatusBadRequest, code: errcode.RequestBodyInvalid, wantRecords: 0,
		},
		{
			name: "not an object", method: http.MethodPost, path: placementBase,
			body: `[]`, status: http.StatusBadRequest, code: errcode.RequestBodyInvalid, wantRecords: 0,
		},
		{
			name:   "pattern that fails to parse",
			method: http.MethodPost, path: placementBase,
			body: ruleBody("a/b", "*", "payments"), status: http.StatusBadRequest,
			code: errcode.PlacementInvalidPattern, wantRecords: 0,
		},
		{
			name:   "rule that the namespace does not hold",
			method: http.MethodPut, path: placementBase + "/plr-none",
			body: ruleBody("web", "*", "payments"), status: http.StatusNotFound,
			code: errcode.PlacementRuleNotFound, wantRecords: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, store := newPlacementRouteHandler(t, selectiveAuthorizer{deny: nil})

			code, body := placementCall(t, h, tt.method, tt.path, tt.body)
			expectError(t, code, body, tt.status, tt.code)
			checkDenialRecords(t, store, tt.wantRecords)
			if len(store.entries) != 0 {
				t.Errorf("a refused write left %d rule audit entries, want 0", len(store.entries))
			}
		})
	}
}

// errStoreWrite is the error of a create that fails for no reason a sentinel
// names.
var errStoreWrite = errors.New("store write failed")

// TestPlacementRuleInternalErrorWritesNoDenial checks that a write that fails
// for no reason a sentinel names answers the internal error, without its
// text, and writes no denial record.
func TestPlacementRuleInternalErrorWritesNoDenial(t *testing.T) {
	t.Parallel()

	h, store := newPlacementRouteHandler(t, selectiveAuthorizer{deny: nil})
	store.createErr = errStoreWrite

	code, body := placementCall(t, h, http.MethodPost, placementBase, ruleBody("web", "*", "payments"))
	expectError(t, code, body, http.StatusInternalServerError, errcode.ServerInternal)
	if strings.Contains(body, "store write failed") {
		t.Errorf("the internal answer names the store error: %s", body)
	}
	checkDenialRecords(t, store, 0)
	if len(store.entries) != 0 {
		t.Errorf("a failed write left %d rule audit entries, want 0", len(store.entries))
	}
}

// checkDenialRecords fails the test unless the denial audit of the namespace
// holds exactly want records, each a denied manage-placement-rules entry.
func checkDenialRecords(t *testing.T, store *memPlacements, want int) {
	t.Helper()

	list, err := store.denials.List(t.Context(), "acme", audit.Page{Size: 0, Before: 0})
	if err != nil {
		t.Fatalf("list the denial audit: %v", err)
	}
	if len(list.Entries) != want {
		t.Fatalf("denial records = %d, want %d", len(list.Entries), want)
	}
	for i := range list.Entries {
		e := &list.Entries[i]
		if e.Outcome != audit.OutcomeDenied ||
			e.Action != string(identity.ActionNamespaceManagePlacementRules) {
			t.Errorf("denial record = %+v, want a denied manage-placement-rules entry", *e)
		}
	}
}
