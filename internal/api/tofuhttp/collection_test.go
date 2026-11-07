package tofuhttp_test

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/internal/api/tofuunit"
)

// newPlacedHandler returns a handler over units whose Placer is placer.
func newPlacedHandler(t *testing.T, units *fakeUnits, placer *tofuunit.Placer) http.Handler {
	t.Helper()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  tokenAuth{},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     units,
		Namespaces:     fakeNamespaces{},
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
