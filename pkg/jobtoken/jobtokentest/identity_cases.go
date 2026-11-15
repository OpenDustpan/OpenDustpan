package jobtokentest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/jobtoken"
)

const (
	toolPulumi      = "pulumi"
	changeWrongTool = "wrong tool"
)

// identityReadCases checks the stored references without the service or the
// HTTP handler. Each tool must resolve a unit by ID, never by an old name.
func identityReadCases() []suiteCase {
	const count = 14

	out := make([]suiteCase, 0, count)

	for _, tool := range []string{toolPulumi, "opentofu"} {
		for _, change := range []string{
			"rename", "namespace rename", "delete", "remove", "recreate", "unknown ID", changeWrongTool,
		} {
			out = append(out, suiteCase{
				name: tool + " token read after " + change,
				test: func(t *testing.T, h Harness) {
					t.Helper()
					testIdentityRead(t, h, tool, change)
				},
			})
		}
	}

	return out
}

func testIdentityRead(t *testing.T, h Harness, tool, change string) {
	t.Helper()

	name, newName := "prod", "renamed"
	if tool == toolPulumi {
		name, newName = "web/prod", "other/renamed"
	}

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", tool, name)
	starter := h.Principal(t, "alice")
	op := liveOperation("op-identity")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)
	mustIssue(t, h, tok, fence)

	tok, want, found := changeIdentity(t, h, tok, change, newName)
	m, err := h.Store.GetByDigest(t.Context(), tok.Digest)
	if !found {
		if !errors.Is(err, jobtoken.ErrNotFound) {
			t.Fatalf("GetByDigest = %v, want ErrNotFound", err)
		}

		return
	}

	if err != nil {
		t.Fatalf("GetByDigest: %v", err)
	}

	if m.Token.Unit != want || m.Token.Namespace != want.Namespace() || m.Claim.JobUnitID != unit.UnitID() {
		t.Fatal("read token does not have the current name and the original unit ID")
	}
}

func changeIdentity(
	t *testing.T, h Harness, tok jobtoken.Token, change, newName string,
) (jobtoken.Token, identity.Object, bool) {
	t.Helper()

	unit := tok.Unit
	tool, name := unit.Tool(), unit.UnitName()
	want := unit
	found := false

	switch change {
	case "rename":
		want = h.Rename(t, unit, newName)
		h.Unit(t, "acme", tool, name)
		found = true
	case "namespace rename":
		h.RenameNamespace(t, tok.Namespace, "gamma")
		h.Namespace(t, "acme")
		h.Unit(t, "acme", tool, name)

		ref, err := identity.UnitObject(tool, "gamma", name)
		if err != nil {
			t.Fatal(err)
		}

		want = ref.WithUnitID(unit.UnitID())
		found = true
	case "remove":
		h.PurgeUnit(t, unit)
	case "delete":
		h.DeleteUnit(t, unit)
	case "recreate":
		h.PurgeUnit(t, unit)

		recreated := h.Unit(t, "acme", tool, name)
		if recreated.UnitID() == unit.UnitID() {
			t.Fatal("the recreated unit has the old ID")
		}
	case "unknown ID", changeWrongTool:
		ref := unit.WithUnitID("unt-missing")
		if change == changeWrongTool {
			ref = wrongTool(t, unit)
		}

		secret, err := jobtoken.NewSecret()
		if err != nil {
			t.Fatalf("new secret: %v", err)
		}

		tok.Digest = jobtoken.DigestOf(secret)
		tok.Unit = ref
		h.WriteRaw(t, tok)
	default:
		t.Fatalf("unknown identity change %q", change)
	}

	return tok, want, found
}

func wrongTool(t *testing.T, unit identity.Object) identity.Object {
	t.Helper()

	tool, name := toolPulumi, "web/prod"
	if unit.Tool() == toolPulumi {
		tool, name = "opentofu", "prod"
	}

	ref, err := identity.UnitObject(tool, unit.Namespace(), name)
	if err != nil {
		t.Fatal(err)
	}

	return ref.WithUnitID(unit.UnitID())
}
