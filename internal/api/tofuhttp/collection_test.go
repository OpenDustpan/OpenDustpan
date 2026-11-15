package tofuhttp_test

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// newPlacedHandler returns a handler over units whose Placer is placer.
func newPlacedHandler(t *testing.T, units *fakeUnits, placer *tofuunit.Placer) http.Handler {
	t.Helper()

	return newPlacedHandlerIn(t, units, placer, fakeNamespaces{})
}

// notHomeNamespaces reads the namespace as fakeNamespaces does, and refuses a
// write, because another installation owns the namespace.
type notHomeNamespaces struct {
	fakeNamespaces
}

func (notHomeNamespaces) WritableHere(context.Context, namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, namespace.ErrNotHome
}

// newPlacedHandlerIn returns a handler over units whose Placer is placer and
// whose namespaces are namespaces.
func newPlacedHandlerIn(
	t *testing.T, units *fakeUnits, placer *tofuunit.Placer, namespaces tofuhttp.NamespaceService,
) http.Handler {
	t.Helper()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  tokenAuth{},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     units,
		Namespaces:     namespaces,
		Audit:          newTestAudit(),
		Gate:           nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         placer,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return h
}

// TestFirstRequestWithoutAPlacerOrRuleIsRefused proves that a first LOCK makes
// no workspace when the server has no Placer (501) or no rule places it (422).
func TestFirstRequestWithoutAPlacerOrRuleIsRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		placer     *tofuunit.Placer
		wantStatus int
		wantText   string
	}{
		{"no placer", nil, http.StatusNotImplemented, "placement rules"},
		{
			"no rule matches",
			&tofuunit.Placer{Rules: fixedRules{none: true}, Collections: fixedCollections{missing: false}},
			http.StatusUnprocessableEntity, "placement-rules",
		},
		{
			"the rule names a collection that is gone",
			&tofuunit.Placer{Rules: fixedRules{none: false}, Collections: fixedCollections{missing: true}},
			http.StatusConflict, "collection",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(false)
			h := newPlacedHandler(t, units, tc.placer)

			rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
			if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantText) {
				t.Fatalf("LOCK = %d %q, want %d naming %q", rec.Code, rec.Body.String(), tc.wantStatus, tc.wantText)
			}

			if units.created != 0 {
				t.Fatalf("created %d workspaces, want none", units.created)
			}
		})
	}
}

// TestFirstRequestInANamespaceOfAnotherInstallation proves that a first LOCK in
// a namespace that another installation owns answers 421 and not the error of
// a missing rule, with or without a rule, and makes no workspace.
func TestFirstRequestInANamespaceOfAnotherInstallation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		placer *tofuunit.Placer
	}{
		{"no rule", &tofuunit.Placer{Rules: fixedRules{none: true}, Collections: fixedCollections{missing: false}}},
		{"no placer", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(false)
			h := newPlacedHandlerIn(t, units, tc.placer, notHomeNamespaces{fakeNamespaces: fakeNamespaces{}})

			rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
			if rec.Code != http.StatusMisdirectedRequest {
				t.Fatalf("LOCK = %d %q, want 421", rec.Code, rec.Body.String())
			}

			if units.created != 0 {
				t.Fatalf("created %d workspaces, want none", units.created)
			}
		})
	}
}

// TestLostCreateRereadIsChecked proves that the re-read after a lost create
// meets the same collection check as the first read. The workspace is hidden
// from the first read, so the request tries a create, finds the workspace
// there, and reads it again. The workspace is in no collection, so the LOCK
// answers 409 and locks nothing.
func TestLostCreateRereadIsChecked(t *testing.T) {
	t.Parallel()

	units := newFakeUnits(true)
	units.hideOnce = true

	h := newPlacedHandler(t, units, &tofuunit.Placer{
		Rules: fixedRules{none: true}, Collections: fixedCollections{missing: true},
	})

	rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "unit-collection") {
		t.Fatalf("LOCK = %d %q, want 409 naming the move command", rec.Code, rec.Body.String())
	}

	if len(units.lockJobs) != 0 {
		t.Fatalf("workspace took %d locks, want none", len(units.lockJobs))
	}
}

// TestWriteToAWorkspaceWithoutPlacerIsRefused proves that a handler with no
// Placer cannot check the collection of an existing workspace, so it refuses
// the LOCK with 501 and locks nothing.
func TestWriteToAWorkspaceWithoutPlacerIsRefused(t *testing.T) {
	t.Parallel()

	units := newFakeUnits(true)
	h := newPlacedHandler(t, units, nil)

	rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("LOCK = %d %q, want 501", rec.Code, rec.Body.String())
	}

	if len(units.lockJobs) != 0 {
		t.Fatalf("workspace took %d locks, want none", len(units.lockJobs))
	}
}

// TestWriteToAWorkspaceInNoCollectionIsRefused proves that a LOCK of an
// existing workspace that no collection holds answers 409, names the move
// command, and locks nothing.
func TestWriteToAWorkspaceInNoCollectionIsRefused(t *testing.T) {
	t.Parallel()

	units := newFakeUnits(true)
	h := newPlacedHandler(t, units, &tofuunit.Placer{
		Rules: fixedRules{none: false}, Collections: fixedCollections{missing: true},
	})

	rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "unit-collection") {
		t.Fatalf("LOCK = %d %q, want 409 naming the move command", rec.Code, rec.Body.String())
	}

	if len(units.lockJobs) != 0 {
		t.Fatalf("workspace took %d locks, want none", len(units.lockJobs))
	}
}
