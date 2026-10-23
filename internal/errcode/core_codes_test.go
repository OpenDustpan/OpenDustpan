package errcode_test

import (
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// TestSharedCodes fails for a shared code that the catalog lacks or that has
// other arguments than its line.
func TestSharedCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code string
		args []errcode.Arg
	}{
		{errcode.NamespaceAlreadyExists, nil},
		{errcode.NamespaceUnavailable, nil},
		{errcode.MaintenanceActiveWork, []errcode.Arg{
			{Name: "workCount", Kind: errcode.KindQuantity},
			{Name: "canceledCount", Kind: errcode.KindQuantity},
		}},
		{errcode.MaintenanceHeld, nil},
		{errcode.MaintenanceInvalidState, nil},
		{errcode.MaintenanceUnavailable, nil},
		{errcode.RequestInvalidObject, nil},
		{errcode.RequestIdempotencyKeyLost, nil},
		{errcode.UnitNotFound, nil},
		{errcode.LockLocked, []errcode.Arg{
			{Name: "holder", Kind: errcode.KindIdentifier},
			{Name: "since", Kind: errcode.KindTime},
			{Name: "target", Kind: errcode.KindIdentifier},
			{Name: "targetKind", Kind: errcode.KindIdentifier},
		}},
		{errcode.TokenInvalid, nil},
		{errcode.SecretKeyNotConfigured, nil},
		{errcode.AuditReasonInvalid, nil},
		{errcode.ServerRouteNotFound, nil},
		{errcode.ServerComponentNotFound, nil},
		{errcode.ServerMethodNotAllowed, nil},
	}

	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			spec, ok := errcode.Lookup(tc.code)
			if !ok {
				t.Fatalf("code %q is not in the catalog", tc.code)
			}

			if len(spec.Args) != len(tc.args) {
				t.Fatalf("args = %v, want %v", spec.Args, tc.args)
			}

			for i, a := range tc.args {
				if spec.Args[i] != a {
					t.Errorf("arg %d = %v, want %v", i, spec.Args[i], a)
				}
			}
		})
	}
}

// TestPlannedAreasExist fails for an area that a later change fills and that
// has no spec list or no golden file yet.
func TestPlannedAreasExist(t *testing.T) {
	t.Parallel()

	for _, area := range []string{
		"audit", "audit-chain", "backup", "collection", "context", "grant", "journal", "lock", "override",
		"principal", "provider", "restore", "run", "runner", "secret", "stack", "state-version", "token",
		"unit", "update", "upgrade", "view", "worker",
	} {
		if _, err := os.Stat("testdata/catalog/" + area + ".txt"); err != nil {
			t.Errorf("area %s: %v", area, err)
		}

		file := "catalog_" + strings.ReplaceAll(area, "-", "") + ".go"
		if _, err := os.Stat(file); err != nil {
			t.Errorf("area %s: %v", area, err)
		}
	}
}
