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
		{"an error that is not a refusal", errors.New("grant store down"), http.StatusNotFound},
		{"an error that wraps gate.ErrUnavailable", fmt.Errorf("%w: grant store down", gate.ErrUnavailable), http.StatusServiceUnavailable},
	}

	for _, rt := range routes {
		for _, e := range errs {
			t.Run(rt.name+" with "+e.name, func(t *testing.T) {
				t.Parallel()

				records := newFakeRecords()
				handler := newHandler(t, failingAuthorizer{err: e.err}, records)

				w := get(t.Context(), t, handler, rt.path, testToken)
				if w.Code != e.wantStatus {
					t.Fatalf("status = %d, want %d; body: %s", w.Code, e.wantStatus, w.Body)
				}

				if strings.Contains(w.Body.String(), rt.id) || strings.Contains(w.Body.String(), "access") {
					t.Errorf("body holds record content: %s", w.Body)
				}

				records.mu.Lock()
				defer records.mu.Unlock()

				if records.gets != 0 {
					t.Errorf("store reads = %d, want 0", records.gets)
				}
			})
		}
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
	})

	t.Run("an unknown namespace reads no record", func(t *testing.T) {
		t.Parallel()

		records := newFakeRecords()
		records.byKey[recordKey("absent", recordID)] = storedRecord("absent", recordID)
		handler := newHandler(t, newAuthorizer(namespaceHolds("absent")...), records)

		w := get(t.Context(), t, handler, "/api/dustpan/v1/namespaces/absent/decision-records/"+recordID, testToken)
		if w.Code != http.StatusNotFound || w.Body.String() != notFoundBody {
			t.Fatalf("status = %d, body = %s, want the 404 body", w.Code, w.Body)
		}

		if records.gets != 0 {
			t.Errorf("store reads = %d, want 0", records.gets)
		}
	})

	t.Run("a namespace store that does not answer gives 503", func(t *testing.T) {
		t.Parallel()

		records := newFakeRecords()
		records.byKey[recordKey("down", recordID)] = storedRecord("down", recordID)
		handler := newHandler(t, newAuthorizer(namespaceHolds("down")...), records)

		w := get(t.Context(), t, handler, "/api/dustpan/v1/namespaces/down/decision-records/"+recordID, testToken)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body: %s", w.Code, w.Body)
		}

		if !strings.Contains(w.Body.String(), "gate.unavailable") || strings.Contains(w.Body.String(), recordID) {
			t.Errorf("body = %s, want gate.unavailable and no record ID", w.Body)
		}

		if records.gets != 0 {
			t.Errorf("store reads = %d, want 0", records.gets)
		}
	})
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
