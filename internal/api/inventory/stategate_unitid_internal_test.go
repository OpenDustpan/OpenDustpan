package inventory

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// appendStore is an audit.Store that keeps the entries of Append.
type appendStore struct {
	audit.Store

	mu      sync.Mutex
	entries []audit.Entry
}

func (s *appendStore) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, e)

	return e, nil
}

// acmeNamespaces gives the namespace acme.
type acmeNamespaces struct{}

func (acmeNamespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		ID: "ns-acme", CreatedAt: time.Now(), Name: name, PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

// White-box: the restore and the purge routes resolve the object from the unit
// ID in the path, so a request never reaches the guard with an object by name
// alone. The case sends that object to the guard directly, as if the route
// had failed.
//
// TestGuardStateWriteNeedsTheUnitID checks that the guard of a restore or a
// purge refuses an object with no unit ID with 403 gate.denied, writes the
// refusal to the audit log, and never asks the admission, which is nil here.
func TestGuardStateWriteNeedsTheUnitID(t *testing.T) {
	t.Parallel()

	obj, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	tests := []struct {
		name  string
		check stateCheck
	}{
		{"restore", stateCheck{action: identity.ActionUnitRestore, op: gate.OperationRollback}},
		{"purge", stateCheck{action: identity.ActionUnitPurge, op: gate.OperationDelete}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkNoUnitIDRefused(t, obj, tt.check)
		})
	}
}

// checkNoUnitIDRefused runs one case of TestGuardStateWriteNeedsTheUnitID.
func checkNoUnitIDRefused(t *testing.T, obj identity.Object, check stateCheck) {
	t.Helper()

	store := &appendStore{Store: nil, mu: sync.Mutex{}, entries: nil}

	rec, err := refusal.New(nil, store, acmeNamespaces{}, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatalf("refusal recorder: %v", err)
	}

	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.guard = rec

	var p identity.Principal

	p.Subject = "alice"

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/api/inventory/deleted-units", nil)

	if h.guardStateWrite(w, req, p, obj, check) {
		t.Fatal("guardStateWrite(no unit ID) = true, want a refusal")
	}

	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), errcode.GateDenied) {
		t.Fatalf("answer = %d %s, want 403 %s", w.Code, w.Body.String(), errcode.GateDenied)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.entries) != 1 || store.entries[0].Action != string(check.action) {
		t.Fatalf("audit entries = %+v, want one refusal of %s", store.entries, check.action)
	}
}
