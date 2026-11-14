package registry_test

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	apiregistry "opendustpan.org/opendustpan/internal/api/registry"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestPublishDenialWritesAuditEntry checks that a provider publish that the
// authorizer refuses writes one denied entry to the audit log of the namespace.
func TestPublishDenialWritesAuditEntry(t *testing.T) {
	t.Parallel()

	f := newFixture(t, identity.ActionRegistryPublish)

	got := f.call(t, "publisher", http.MethodPut, base+"/providers/payments/versions/1.0.0", []byte("{}"))
	wantCode(t, "publish with the action denied", got, http.StatusForbidden)

	entries, err := f.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: 100})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var denied []audit.Entry

	for i := range entries.Entries {
		if entries.Entries[i].Outcome == audit.OutcomeDenied {
			denied = append(denied, entries.Entries[i])
		}
	}

	if len(denied) != 1 {
		t.Fatalf("denied audit entries: got %d, want 1", len(denied))
	}

	if denied[0].Action != string(identity.ActionRegistryPublish) {
		t.Errorf("action of the denied entry: got %q, want %q", denied[0].Action, identity.ActionRegistryPublish)
	}
}

// TestNewHandlerRefusesNoNamespaces checks that the handler does not start
// without the namespaces, and only for that reason: every other dependency is
// set. A refusal of a namespace could not be written without them.
func TestNewHandlerRefusesNoNamespaces(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := apiregistry.NewHandler(apiregistry.Config{
		Service: &service.Service{}, Authenticator: testAuthn{}, Authorizer: testAuthz{deny: nil},
		MaxUploadBytes: maxUpload, Audit: f.store, Namespaces: nil,
		OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, Idempotency: nil,
		Logger: slog.New(slog.DiscardHandler),
	})
	if !errors.Is(err, apiregistry.ErrMissingDependency) {
		t.Errorf("NewHandler without namespaces: got %v, want %v", err, apiregistry.ErrMissingDependency)
	}

	if err != nil && !strings.Contains(err.Error(), "namespaces") {
		t.Errorf("error names the missing dependency: got %q, want namespaces", err.Error())
	}
}
