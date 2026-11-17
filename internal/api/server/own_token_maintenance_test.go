package server_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// TestOwnTokenRevokeInMaintenanceMode proves that a person ends an own token
// in maintenance mode, with and without an Idempotency-Key. The revoke is on
// the list of reducing routes, so it answers 204 there. A replay with the
// same key answers the stored 204. The ended token no longer signs in.
func TestOwnTokenRevokeInMaintenanceMode(t *testing.T) {
	t.Parallel()

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil, namespaces: nil,
		hostile: nil, passwords: nil, failPolicyRead: nil, idempotency: true,
	})
	sess := e.freshSignIn(t)
	body := ownTokenBody()

	keyed := e.createKeyed(t, sess, "maintenance-create-1", body)
	if keyed.Code != http.StatusCreated {
		t.Fatalf("create before maintenance = %d %s, want 201", keyed.Code, keyed.Body.String())
	}

	var ended tokenDoc

	decodeInto(t, keyed.Body.String(), &ended)

	plain := e.createKeyed(t, sess, "maintenance-create-2", body)
	if plain.Code != http.StatusCreated {
		t.Fatalf("second create before maintenance = %d %s, want 201", plain.Code, plain.Body.String())
	}

	var unkeyed tokenDoc

	decodeInto(t, plain.Body.String(), &unkeyed)

	if err := e.store.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	remove := callerCall{
		http.MethodDelete, "/api/dustpan/v1/me/tokens/" + ended.ID, "", "",
		map[string]string{"Idempotency-Key": "maintenance-revoke-1"},
	}

	deleted := e.sendWithHeaders(t, sess, remove)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("keyed revoke in maintenance mode = %d %s, want 204", deleted.Code, deleted.Body.String())
	}

	assertTokenEnded(t, e, ended.Secret)

	replay := e.sendWithHeaders(t, sess, remove)
	if replay.Code != http.StatusNoContent || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("keyed revoke replay in maintenance mode = %d %s, want the stored 204 marked as a replay",
			replay.Code, replay.Body.String())
	}

	plainRemove := callerCall{http.MethodDelete, "/api/dustpan/v1/me/tokens/" + unkeyed.ID, "", "", nil}

	plainDeleted := e.sendWithHeaders(t, sess, plainRemove)
	if plainDeleted.Code != http.StatusNoContent {
		t.Fatalf("unkeyed revoke in maintenance mode = %d %s, want 204", plainDeleted.Code, plainDeleted.Body.String())
	}

	assertTokenEnded(t, e, unkeyed.Secret)
}

// assertTokenEnded checks that the secret of an ended token no longer
// authenticates a request.
func assertTokenEnded(t *testing.T, e signInEnv, secret string) {
	t.Helper()

	rec := serveCaller(t, e.handler, callerCall{http.MethodGet, "/api/dustpan/v1/me/access", secret, "", nil})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ended token = %d %s, want 401", rec.Code, rec.Body.String())
	}
}

// TestOwnTokenCreateRefusedInMaintenanceMode proves the other side: a keyed
// create of an own token is not a reducing route, so it answers 503 in
// maintenance mode, and it makes no token.
func TestOwnTokenCreateRefusedInMaintenanceMode(t *testing.T) {
	t.Parallel()

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil, namespaces: nil,
		hostile: nil, passwords: nil, failPolicyRead: nil, idempotency: true,
	})
	sess := e.freshSignIn(t)

	if err := e.store.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	rec := e.createKeyed(t, sess, "maintenance-create-refused", ownTokenBody())
	if rec.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rec.Body.String(), errcode.MaintenanceInMaintenance) {
		t.Fatalf("keyed create in maintenance mode = %d %s, want 503 with %s",
			rec.Code, rec.Body.String(), errcode.MaintenanceInMaintenance)
	}
}
