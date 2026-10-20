package pulumi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/identity/identitytest"
)

// deniedEntries returns the entries of the store with the outcome denied.
func deniedEntries(store *memAuditStore) []audit.Entry {
	store.mu.Lock()
	defer store.mu.Unlock()

	var out []audit.Entry

	for i := range store.entries {
		if store.entries[i].Outcome == audit.OutcomeDenied {
			out = append(out, store.entries[i])
		}
	}

	return out
}

// TestRenameDestinationRefusalWritesAnEntry proves a rename with no grant on the
// destination writes one denied entry for unit:manage on the destination unit.
func TestRenameDestinationRefusalWritesAnEntry(t *testing.T) {
	t.Parallel()

	store := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
	sourceOnly := identitytest.NewPolicyAuthorizer("tester", map[identity.Action][]string{
		identity.ActionUnitManage: {"pulumi:acme/web/prod"},
	})
	handler, _ := newAuditedEnforcingHandler(t, sourceOnly, store)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/stacks/acme/web/prod/rename",
		strings.NewReader(`{"newName":"renamed","newProject":"web"}`))
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	got := deniedEntries(store)
	if len(got) != 1 {
		t.Fatalf("denied entries = %d, want 1: %+v", len(got), got)
	}

	if got[0].Action != string(identity.ActionUnitManage) || got[0].Object != "pulumi:acme/web/renamed" ||
		got[0].Subject != "tester" {
		t.Errorf("entry = action %q object %q subject %q", got[0].Action, got[0].Object, got[0].Subject)
	}
}

// TestListStacksInRefusalWritesAnEntry proves a stack list of one namespace
// with no grant writes one denied entry for the first action that is refused.
func TestListStacksInRefusalWritesAnEntry(t *testing.T) {
	t.Parallel()

	store := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
	// The route check passes with caller:read. The list then needs unit:read
	// on the namespace as well, and the policy grants it nowhere.
	readOnly := identitytest.NewPolicyAuthorizer("tester", map[identity.Action][]string{
		identity.ActionCallerRead: {identity.ServerObject().String()},
	})
	handler, _ := newAuditedEnforcingHandler(t, readOnly, store)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/user/stacks?organization=acme",
		http.NoBody,
	)
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	got := deniedEntries(store)
	if len(got) != 1 {
		t.Fatalf("denied entries = %d, want 1: %+v", len(got), got)
	}

	if got[0].Action != string(identity.ActionUnitRead) || got[0].Subject != "tester" {
		t.Errorf("entry = action %q subject %q, want %q", got[0].Action, got[0].Subject, identity.ActionUnitRead)
	}
}
