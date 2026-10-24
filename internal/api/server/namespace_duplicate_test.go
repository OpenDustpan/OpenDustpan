package server_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// wantDuplicate is the message of the 409 of a second create of acme. It
// names the action and the name one time.
const wantDuplicate = `create namespace "acme": namespace already exists`

// TestNamespaceCreateDuplicateText proves that the 409 of a second create of
// a name, with no access service, says what failed one time.
func TestNamespaceCreateDuplicateText(t *testing.T) {
	t.Parallel()

	handler := newNamespaceHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	if status, body := serveNamespace(t, handler, `{"name":"acme"}`); status != http.StatusCreated {
		t.Fatalf("first POST answers %d, want %d (%s)", status, http.StatusCreated, body)
	}

	status, body := serveNamespace(t, handler, `{"name":"acme"}`)
	if doc := decodeError(t, body); status != http.StatusConflict || doc.Message != wantDuplicate {
		t.Fatalf("second POST answers %d %q, want %d %q", status, doc.Message, http.StatusConflict, wantDuplicate)
	}
}

// TestNamespaceCreateDuplicateTextWithAccess proves the same text when the
// namespace, the grant of its creator and the audit entry commit together.
func TestNamespaceCreateDuplicateTextWithAccess(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var doc errorDoc

	f.as(t, "ann", apiCall{http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`, http.StatusConflict}, &doc)

	if doc.Message != wantDuplicate {
		t.Fatalf("duplicate message = %q, want %q", doc.Message, wantDuplicate)
	}
}
