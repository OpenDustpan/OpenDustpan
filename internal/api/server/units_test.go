package server_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// seedStack makes the Pulumi stack "<project>/<name>" in the namespace ns
// and returns its unit with the stable ID: a grant names a unit that
// exists.
func seedStack(t *testing.T, st *sqlite.Store, ns namespace.Name, project, name string) identity.Object {
	t.Helper()

	ctx := t.Context()

	rec, err := st.GetNamespace(ctx, ns)
	if err != nil {
		t.Fatalf("namespace of stack: %v", err)
	}

	ref, err := stack.ParseRef(ns.String(), project, name)
	if err != nil {
		t.Fatalf("stack ref: %v", err)
	}

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	s := stack.Stack{
		NamespaceID: "",
		CreatedAt:   at, UpdatedAt: at, Tags: map[string]string{}, Ref: ref,
		ID: stack.ID("stk-" + ns.String() + "-" + project + "-" + name), Version: 0,
	}

	if createErr := st.CreateStack(ctx, s, rec.Fence()); createErr != nil {
		t.Fatalf("create stack: %v", createErr)
	}

	unit, err := identity.UnitObject("pulumi", ns, project+"/"+name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return unit.WithUnitID(string(s.ID))
}
