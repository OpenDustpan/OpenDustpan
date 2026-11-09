package postgres_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/unitsettings/unitsettingstest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// unitSettingsHarness returns a harness over a fresh store. The namespaces it
// makes have failover version 1.
func unitSettingsHarness(base string) unitsettingstest.Factory {
	return func(t *testing.T) unitsettingstest.Harness {
		t.Helper()

		return newUnitSettingsHarness(t, openURL(t, isolatedURL(t, base)))
	}
}

func newUnitSettingsHarness(t *testing.T, s *postgres.Store) unitsettingstest.Harness {
	t.Helper()

	return unitsettingstest.Harness{
		Store:   s.UnitSettings(),
		Records: s.Idempotency(idempotencytest.TestWrapper(t, 1)),
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		Unit: func(t *testing.T, ns, tool, name string) identity.Object {
			t.Helper()

			return makeUnit(t, s, ns, tool, name)
		},
		Audit: func(t *testing.T, ns string) []audit.Entry {
			t.Helper()

			return allAuditEntries(t, s, namespace.Name(ns))
		},
	}
}

// makeUnit creates the unit of the tool in the namespace ns, with its stable ID,
// and returns its reference with the ID set. A Pulumi name is project/stack.
func makeUnit(t *testing.T, s *postgres.Store, ns, tool, name string) identity.Object {
	t.Helper()

	fence := ensureNamespace(t, s, namespace.Name(ns))
	now := time.UnixMilli(1_700_000_000_000).UTC()

	switch tool {
	case "pulumi":
		project, stackName, ok := strings.Cut(name, "/")
		if !ok {
			t.Fatalf("stack name %q has no project", name)
		}

		ref, err := stack.ParseRef(ns, project, stackName)
		if err != nil {
			t.Fatalf("parse stack reference: %v", err)
		}

		st := stack.Stack{
			NamespaceID: "", CreatedAt: now, UpdatedAt: now, Tags: map[string]string{}, Ref: ref,
			ID: stack.ID(ident.NewID("stk-")), Version: 0,
		}
		if err := s.CreateStack(t.Context(), st, fence); err != nil {
			t.Fatalf("create stack: %v", err)
		}
	case "opentofu":
		w := workspace.Workspace{
			NamespaceID: "",
			CreatedAt:   now,
			UpdatedAt:   now,
			Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
			LastRelease: workspace.NoRelease(),
			ID:          workspace.ID(ident.NewID("ws-")),
			Revision:    1,
			Ref:         workspace.Ref{Namespace: namespace.Name(ns), Name: workspace.Name(name)},
			Overrides: namespace.Overrides{
				Forced:   false,
				Unlocked: false,
			},
			Origin: identity.StateOrigin{Job: "", Commit: ""},
		}
		if err := s.CreateWorkspace(t.Context(), w, fence); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
	default:
		t.Fatalf("unknown tool %q", tool)
	}

	ref, err := identity.UnitObject(tool, namespace.Name(ns), name)
	if err != nil {
		t.Fatalf("unit reference: %v", err)
	}

	id, found, err := s.UnitID(t.Context(), ref)
	if err != nil || !found {
		t.Fatalf("unit ID of %s = %q, %v, %v", ref, id, found, err)
	}

	return ref.WithUnitID(id)
}

func TestUnitSettingsConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	unitsettingstest.Run(t, unitSettingsHarness(base))
}
