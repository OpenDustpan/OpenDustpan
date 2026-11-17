package decisionrecord_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// failingAuthorizer answers every check with err, which is not a refusal. It
// stands for a grant store that does not answer.
type failingAuthorizer struct{ err error }

func (a failingAuthorizer) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return a.err
}

func (failingAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// errGrantStoreDown is an authorizer error that is not a refusal.
var errGrantStoreDown = errors.New("grant store down")

// TestAuthorizerErrorFailsClosed proves that an authorizer error that is not
// a refusal never lets a read through, on each route. The answer is the one
// 404, or 503 for an error that wraps gate.ErrUnavailable, with no record
// content and no read of the store.
func TestAuthorizerErrorFailsClosed(t *testing.T) {
	t.Parallel()

	routes := []struct {
		name string
		path string
		id   string
	}{
		{"namespace route", "/api/dustpan/v1/namespaces/acme/decision-records/" + recordID, recordID},
		{"installation route", "/api/dustpan/v1/decision-records/" + instalRecord, instalRecord},
	}

	errs := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"an error that is not a refusal", errGrantStoreDown, http.StatusNotFound},
		{
			"an error that wraps gate.ErrUnavailable",
			fmt.Errorf("%w: grant store down", gate.ErrUnavailable), http.StatusServiceUnavailable,
		},
	}

	for _, rt := range routes {
		for _, e := range errs {
			t.Run(rt.name+" with "+e.name, func(t *testing.T) {
				t.Parallel()
				checkAuthorizerError(t, rt.path, rt.id, e.err, e.wantStatus)
			})
		}
	}
}

func checkAuthorizerError(t *testing.T, path, id string, authzErr error, wantStatus int) {
	t.Helper()

	records := newFakeRecords()
	handler := newHandler(t, failingAuthorizer{err: authzErr}, records)

	w := get(t.Context(), t, handler, path, testToken)
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, wantStatus, w.Body)
	}

	if strings.Contains(w.Body.String(), id) || strings.Contains(w.Body.String(), "access") {
		t.Errorf("body holds record content: %s", w.Body)
	}

	if records.gets != 0 {
		t.Errorf("store reads = %d, want 0", records.gets)
	}
}

// TestNamespaceRouteEarlyChecks proves three layers of the namespace route,
// each with the later layers out of reach: the name parse before any
// authorization check, the namespace lookup before the store read, and the
// 503 of a namespace store that does not answer.
func TestNamespaceRouteEarlyChecks(t *testing.T) {
	t.Parallel()

	t.Run("a malformed namespace name runs no check and reads no store", func(t *testing.T) {
		t.Parallel()
		checkMalformedNamespaceName(t)
	})
	t.Run("an unknown namespace reads no record", func(t *testing.T) {
		t.Parallel()
		checkNamespaceLookup(t, "absent", http.StatusNotFound, "")
	})
	t.Run("a namespace store that does not answer gives 503", func(t *testing.T) {
		t.Parallel()
		checkNamespaceLookup(t, "down", http.StatusServiceUnavailable, "gate.unavailable")
	})
}

func checkMalformedNamespaceName(t *testing.T) {
	t.Helper()

	for _, name := range []string{"bad%20name", "a%2Fb", "@server", "-x", ".x", "a+b"} {
		authz := newAuthorizer(namespaceHolds(namespace.Name(name))...)
		sink := &testAudit{entries: nil}
		records := newFakeRecords()
		handler := build(t, authz, records, sink)

		w := get(t.Context(), t, handler, "/api/dustpan/v1/namespaces/"+name+"/decision-records/"+recordID, testToken)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%q: status = %d, want 404; body: %s", name, w.Code, w.Body)
		}

		if len(authz.checks) != 0 {
			t.Errorf("%q: checks = %v, want none", name, authz.checks)
		}

		if len(sink.entries) != 0 || records.gets != 0 {
			t.Errorf("%q: audit entries = %d, store reads = %d, want 0 and 0", name, len(sink.entries), records.gets)
		}
	}
}

// checkNamespaceLookup reads a record of ns, which the authorizer allows and
// the store holds, and proves the status, the code and that the record store
// is not read.
func checkNamespaceLookup(t *testing.T, ns namespace.Name, wantStatus int, wantCode string) {
	t.Helper()

	records := newFakeRecords()
	records.byKey[recordKey(ns, recordID)] = storedRecord(ns, recordID)
	handler := newHandler(t, newAuthorizer(namespaceHolds(ns)...), records)

	w := get(t.Context(), t, handler, "/api/dustpan/v1/namespaces/"+string(ns)+"/decision-records/"+recordID, testToken)
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, wantStatus, w.Body)
	}

	if wantCode != "" && !strings.Contains(w.Body.String(), wantCode) {
		t.Errorf("body = %s, want the code %s", w.Body, wantCode)
	}

	if strings.Contains(w.Body.String(), recordID) {
		t.Errorf("body names the record ID: %s", w.Body)
	}

	if records.gets != 0 {
		t.Errorf("store reads = %d, want 0", records.gets)
	}
}

// TestServiceRefusesScopesWithoutARecord proves that the service refuses the
// zero scope and the server scope written as a namespace, with a store that
// would return a record, and that it reads no store for them.
func TestServiceRefusesScopesWithoutARecord(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		scope gate.Scope
	}{
		{"the zero scope", gate.Scope{}},
		{"the server scope as a namespace", gate.NamespaceScope("@server")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			records := newHostileRecords(storedRecord("@server", recordID))
			svc := mustService(t, records)

			_, err := svc.Read(t.Context(), tc.scope, recordID)
			if !errors.Is(err, gate.ErrInvalid) {
				t.Fatalf("Read = %v, want an error that wraps gate.ErrInvalid", err)
			}

			if records.gets != 0 {
				t.Errorf("store reads = %d, want 0", records.gets)
			}
		})
	}
}
