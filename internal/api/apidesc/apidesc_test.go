package apidesc_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

func op(method, path string, actions ...string) apidesc.Operation {
	return apidesc.Operation{Method: method, Path: path, Actions: actions, Public: false}
}

func TestValidateRefusesHostileInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   apidesc.Operation
	}{
		{"no method", op("", "/api/dustpan/v1/backups")},
		{"lower case method", op("get", "/api/dustpan/v1/backups")},
		{"path outside the prefix", op("GET", "/api/stacks/acme")},
		{"prefix without the slash", op("GET", "/api/dustpan/v1")},
		{"dot segment", op("GET", "/api/dustpan/v1/backups/../../server/x")},
		{"empty path", op("GET", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.op.Validate(); !errors.Is(err, apidesc.ErrInvalidOperation) {
				t.Fatalf("Validate() = %v, want ErrInvalidOperation", err)
			}
		})
	}
}

func TestKeyIgnoresWildcardNames(t *testing.T) {
	t.Parallel()

	a := op("GET", "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}").Key()
	b := op("GET", "/api/dustpan/v1/namespaces/{ns}/units/{id}").Key()

	if a != b {
		t.Fatalf("keys differ: %q and %q", a, b)
	}

	if op("POST", "/api/dustpan/v1/backups").Key() == op("GET", "/api/dustpan/v1/backups").Key() {
		t.Fatal("methods must give different keys")
	}
}

func backupRoutes() []apidesc.Operation {
	return []apidesc.Operation{
		op("GET", "/api/dustpan/v1/backups", "installation:backup"),
		op("GET", "/api/dustpan/v1/backups/{id}", "installation:backup"),
		op("POST", "/api/dustpan/v1/backups", "installation:backup"),
	}
}

func TestDiffAgrees(t *testing.T) {
	t.Parallel()

	d, err := apidesc.Diff(backupRoutes(), backupRoutes())
	if err != nil || !d.Empty() {
		t.Fatalf("Diff() = %+v, %v; want empty", d, err)
	}
}

func TestDiffRouteWithNoEntry(t *testing.T) {
	t.Parallel()

	d, err := apidesc.Diff(backupRoutes(), backupRoutes()[:2])
	if err != nil || len(d.Undescribed) != 1 || d.Undescribed[0] != "POST /api/dustpan/v1/backups" {
		t.Fatalf("Diff() = %+v, %v", d, err)
	}
}

func TestDiffEntryWithNoRoute(t *testing.T) {
	t.Parallel()

	extra := append(backupRoutes(), op("DELETE", "/api/dustpan/v1/backups/{id}", "installation:backup"))

	d, err := apidesc.Diff(backupRoutes(), extra)
	if err != nil || len(d.Unrouted) != 1 {
		t.Fatalf("Diff() = %+v, %v", d, err)
	}
}

func TestDiffActionsDiffer(t *testing.T) {
	t.Parallel()

	changed := backupRoutes()
	changed[0] = op("GET", "/api/dustpan/v1/backups", "installation:observe")

	d, err := apidesc.Diff(backupRoutes(), changed)
	if err != nil || len(d.ActionMismatch) != 1 {
		t.Fatalf("Diff() = %+v, %v", d, err)
	}
}

func TestDiffPublicFlagDiffers(t *testing.T) {
	t.Parallel()

	public := apidesc.Operation{Method: "GET", Path: "/api/dustpan/v1/capabilities", Actions: nil, Public: true}
	closed := apidesc.Operation{Method: "GET", Path: "/api/dustpan/v1/capabilities", Actions: nil, Public: false}

	d, err := apidesc.Diff([]apidesc.Operation{public}, []apidesc.Operation{closed})
	if err != nil || len(d.ActionMismatch) != 1 {
		t.Fatalf("Diff() = %+v, %v", d, err)
	}
}

func TestDiffFailsClosed(t *testing.T) {
	t.Parallel()

	routes := backupRoutes()
	inputs := map[string][]apidesc.Operation{
		"outside the prefix": {op("GET", "/outside")},
		"a key twice":        {routes[0], routes[0]},
	}

	for name, in := range inputs {
		if _, err := apidesc.Diff(in, routes); !errors.Is(err, apidesc.ErrInvalidOperation) {
			t.Errorf("%s as routes: err = %v", name, err)
		}

		if _, err := apidesc.Diff(routes, in); !errors.Is(err, apidesc.ErrInvalidOperation) {
			t.Errorf("%s as description: err = %v", name, err)
		}
	}
}
