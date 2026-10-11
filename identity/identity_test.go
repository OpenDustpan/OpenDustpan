package identity_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
)

// testAuthorizer allows administrators and denies everyone else, like
// AdminOnly but written independently for the conformance check.
type testAuthorizer struct{}

func (testAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if p.Admin {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func mustName(t *testing.T, s string) namespace.Name {
	t.Helper()

	ns, err := namespace.ParseName(s)
	if err != nil {
		t.Fatalf("parse namespace %q: %v", s, err)
	}

	return ns
}

func TestActionsAreToolNeutral(t *testing.T) {
	t.Parallel()

	for _, a := range []identity.Action{
		identity.ActionNamespaceRead,
		identity.ActionNamespaceAdmin,
		identity.ActionUnitRead,
		identity.ActionUnitWrite,
		identity.ActionUnitManage,
		identity.ActionUnitEncrypt,
		identity.ActionUnitDecrypt,
		identity.ActionCallerRead,
	} {
		lowered := strings.ToLower(string(a))
		if strings.Contains(lowered, "stack") || strings.Contains(lowered, "pulumi") {
			t.Fatalf("action %q names a tool", a)
		}
	}
}

// validCase is one reference that ParseObject must accept.
type validCase struct {
	name       string
	raw        string
	wantNS     string
	wantTool   string
	wantUnit   string
	wantIsUnit bool
}

func TestParseObjectValid(t *testing.T) {
	t.Parallel()

	valid := []validCase{
		{"namespace", "acme", "acme", "", "", false},
		{"unit with slash", "pulumi:acme/web/prod", "acme", "pulumi", "web/prod", true},
		{"unit without slash", "opentofu:acme/network", "acme", "opentofu", "network", true},
	}

	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkValidObject(t, tc)
		})
	}
}

func TestParseObjectInvalid(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"",
		"@caller",
		"pulumi:",
		"pulumi:acme",
		"pulumi:/web",
		"pulumi:acme/",
		":acme/web",
		"pulumi:acme/web:prod",
		"acme/web",
		"../acme",
	}

	for _, raw := range invalid {
		t.Run("invalid "+raw, func(t *testing.T) {
			t.Parallel()

			if _, err := identity.ParseObject(raw); !errors.Is(err, identity.ErrInvalidObject) {
				t.Fatalf("parse %q = %v, want ErrInvalidObject", raw, err)
			}
		})
	}
}

func checkValidObject(t *testing.T, tc validCase) {
	t.Helper()

	obj, err := identity.ParseObject(tc.raw)
	if err != nil {
		t.Fatalf("parse %q: %v", tc.raw, err)
	}

	if obj.String() != tc.raw {
		t.Fatalf("String() = %q, want %q", obj.String(), tc.raw)
	}

	if obj.IsUnit() != tc.wantIsUnit {
		t.Fatalf("IsUnit() = %v, want %v", obj.IsUnit(), tc.wantIsUnit)
	}

	if obj.Namespace().String() != tc.wantNS {
		t.Fatalf("Namespace() = %q, want %q", obj.Namespace(), tc.wantNS)
	}

	if obj.Tool() != tc.wantTool {
		t.Fatalf("Tool() = %q, want %q", obj.Tool(), tc.wantTool)
	}

	if obj.UnitName() != tc.wantUnit {
		t.Fatalf("UnitName() = %q, want %q", obj.UnitName(), tc.wantUnit)
	}
}

func TestObjectConstructors(t *testing.T) {
	t.Parallel()

	ns := mustName(t, "acme")

	nobj := identity.NamespaceObject(ns)
	if nobj.IsUnit() {
		t.Fatal("namespace object reports IsUnit")
	}

	if nobj.String() != "acme" {
		t.Fatalf("namespace String() = %q", nobj.String())
	}

	uobj, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	if !uobj.IsUnit() {
		t.Fatal("unit object reports no unit")
	}

	if uobj.String() != "pulumi:acme/web/prod" {
		t.Fatalf("unit String() = %q", uobj.String())
	}

	if _, err := identity.UnitObject("", ns, "web/prod"); !errors.Is(err, identity.ErrInvalidObject) {
		t.Fatalf("empty tool = %v, want ErrInvalidObject", err)
	}

	if _, err := identity.UnitObject("pulumi", ns, ""); !errors.Is(err, identity.ErrInvalidObject) {
		t.Fatalf("empty name = %v, want ErrInvalidObject", err)
	}
}

// TestCallerObject proves the caller reference names no namespace or unit,
// that ParseObject rejects its wire form, and that a namespace named
// "caller" never equals it.
func TestCallerObject(t *testing.T) {
	t.Parallel()

	cobj := identity.CallerObject()
	if cobj.IsUnit() {
		t.Fatal("caller object reports IsUnit")
	}

	if !cobj.IsCaller() {
		t.Fatal("caller object reports no caller")
	}

	if cobj.String() != "@caller" {
		t.Fatalf("caller String() = %q", cobj.String())
	}

	if cobj.Namespace().String() != "" || cobj.Tool() != "" || cobj.UnitName() != "" {
		t.Fatalf(
			"caller object names %q/%q/%q, want no namespace, tool or unit",
			cobj.Namespace().String(),
			cobj.Tool(),
			cobj.UnitName(),
		)
	}

	if _, err := identity.ParseObject(cobj.String()); !errors.Is(err, identity.ErrInvalidObject) {
		t.Fatalf("parse caller form %q = %v, want ErrInvalidObject", cobj.String(), err)
	}

	nsObj, err := identity.ParseObject("caller")
	if err != nil {
		t.Fatalf("parse namespace caller: %v", err)
	}

	if nsObj.IsCaller() {
		t.Fatal("namespace caller reports caller")
	}

	if nsObj == cobj {
		t.Fatal("namespace caller equals the caller object")
	}
}

func checkAuthorizer(t *testing.T, authorizer identity.Authorizer) {
	t.Helper()

	actions := []identity.Action{
		identity.ActionNamespaceRead,
		identity.ActionNamespaceAdmin,
		identity.ActionUnitRead,
		identity.ActionUnitWrite,
		identity.ActionUnitManage,
		identity.ActionUnitEncrypt,
		identity.ActionUnitDecrypt,
		identity.ActionCallerRead,
	}

	nsObj := identity.NamespaceObject(mustName(t, "acme"))
	unitObj, err := identity.UnitObject("pulumi", mustName(t, "acme"), "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	admin := identity.Principal{Subject: "admin", DisplayName: "admin", Admin: true}
	viewer := identity.Principal{Subject: "viewer", DisplayName: "viewer", Admin: false}

	for _, obj := range []identity.Object{nsObj, unitObj, identity.CallerObject()} {
		for _, action := range actions {
			if err := authorizer.Authorize(t.Context(), admin, action, obj); err != nil {
				t.Fatalf("admin %s on %q: %v", action, obj.String(), err)
			}

			if err := authorizer.Authorize(t.Context(), viewer, action, obj); !errors.Is(
				err,
				identity.ErrDenied,
			) {
				t.Fatalf("viewer %s on %q = %v, want ErrDenied", action, obj.String(), err)
			}
		}
	}
}

func TestAdminOnlyObjects(t *testing.T) {
	t.Parallel()

	checkAuthorizer(t, identity.AdminOnly{})
}

func TestAuthorizerConformance(t *testing.T) {
	t.Parallel()

	checkAuthorizer(t, testAuthorizer{})
}
