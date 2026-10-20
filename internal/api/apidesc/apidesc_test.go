package apidesc_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

func op(method, path string, actions ...string) apidesc.Operation {
	return apidesc.Operation{Method: method, Path: path, Actions: actions, Public: false}
}

func publicOp(method, path string, actions ...string) apidesc.Operation {
	return apidesc.Operation{Method: method, Path: path, Actions: actions, Public: true}
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
		{"method TRACE", op("TRACE", "/api/dustpan/v1/backups", "installation:backup")},
		{"method CONNECT", op("CONNECT", "/api/dustpan/v1/backups", "installation:backup")},
		{"method with a space", op("GET ", "/api/dustpan/v1/backups", "installation:backup")},
		{"empty segment", op("GET", "/api/dustpan/v1/backups//x", "installation:backup")},
		{"trailing slash", op("GET", "/api/dustpan/v1/backups/", "installation:backup")},
		{"prefix with nothing after it", op("GET", "/api/dustpan/v1/", "installation:backup")},
		{"single dot segment", op("GET", "/api/dustpan/v1/backups/./x", "installation:backup")},
		{"encoded dot", op("GET", "/api/dustpan/v1/backups/%2e%2e/x", "installation:backup")},
		{"query", op("GET", "/api/dustpan/v1/backups?x=1", "installation:backup")},
		{"fragment", op("GET", "/api/dustpan/v1/backups#x", "installation:backup")},
		{"space", op("GET", "/api/dustpan/v1/back ups", "installation:backup")},
		{"carriage return", op("GET", "/api/dustpan/v1/backups\r", "installation:backup")},
		{"line feed", op("GET", "/api/dustpan/v1/backups\nX: y", "installation:backup")},
		{"upper case literal", op("GET", "/api/dustpan/v1/Backups", "installation:backup")},
		{"wildcard name with a capital", op("GET", "/api/dustpan/v1/backups/{Id}", "installation:backup")},
		{"wildcard name with a digit first", op("GET", "/api/dustpan/v1/backups/{1id}", "installation:backup")},
		{"empty wildcard name", op("GET", "/api/dustpan/v1/backups/{}", "installation:backup")},
		{"wildcard with a literal part", op("GET", "/api/dustpan/v1/backups/x{id}", "installation:backup")},
		{"rest wildcard not last", op("GET", "/api/dustpan/v1/files/{path...}/x", "installation:backup")},
		{"end anchor not last", op("GET", "/api/dustpan/v1/{$}/x", "installation:backup")},
		{"no actions on a route that is not public", op("GET", "/api/dustpan/v1/backups")},
		{"actions on a public route", publicOp("GET", "/api/dustpan/v1/capabilities", "caller:read")},
		{"empty action", op("GET", "/api/dustpan/v1/backups", "")},
		{"action without a colon", op("GET", "/api/dustpan/v1/backups", "backup")},
		{"action with a capital", op("GET", "/api/dustpan/v1/backups", "Installation:backup")},
		{"action with a space", op("GET", "/api/dustpan/v1/backups", "installation: backup")},
		{"action with a line feed", op("GET", "/api/dustpan/v1/backups", "installation:backup\n")},
		{"action with two colons", op("GET", "/api/dustpan/v1/backups", "a:b:c")},
		{"repeated action", op("GET", "/api/dustpan/v1/backups", "installation:backup", "installation:backup")},
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

func TestValidateAllowsGoodOperations(t *testing.T) {
	t.Parallel()

	good := []apidesc.Operation{
		op("GET", "/api/dustpan/v1/backups", "installation:backup"),
		op("DELETE", "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}", "unit:delete"),
		op("PATCH", "/api/dustpan/v1/namespaces/-/units", "unit:read", "namespace:read"),
		op("GET", "/api/dustpan/v1/files/{path...}", "unit:read"),
		op("GET", "/api/dustpan/v1/{$}", "caller:read"),
		publicOp("GET", "/api/dustpan/v1/capabilities"),
	}

	for _, o := range good {
		if err := o.Validate(); err != nil {
			t.Errorf("Validate(%s %s) = %v", o.Method, o.Path, err)
		}
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
	if err != nil || len(d.Unrouted) != 1 || d.Unrouted[0] != "DELETE /api/dustpan/v1/backups/{}" {
		t.Fatalf("Diff() = %+v, %v", d, err)
	}
}

func TestDiffActionsDiffer(t *testing.T) {
	t.Parallel()

	changed := backupRoutes()
	changed[0] = op("GET", "/api/dustpan/v1/backups", "installation:observe")

	d, err := apidesc.Diff(backupRoutes(), changed)
	if err != nil || len(d.Mismatch) != 1 || d.Mismatch[0] != "GET /api/dustpan/v1/backups" {
		t.Fatalf("Diff() = %+v, %v", d, err)
	}
}

func TestDiffPathNamesDiffer(t *testing.T) {
	t.Parallel()

	renamed := backupRoutes()
	renamed[1] = op("GET", "/api/dustpan/v1/backups/{backup_id}", "installation:backup")

	d, err := apidesc.Diff(backupRoutes(), renamed)
	if err != nil || len(d.Mismatch) != 1 || d.Mismatch[0] != "GET /api/dustpan/v1/backups/{}" {
		t.Fatalf("Diff() = %+v, %v", d, err)
	}
}

func TestDiffPublicFlagDiffers(t *testing.T) {
	t.Parallel()

	public := apidesc.Operation{Method: "GET", Path: "/api/dustpan/v1/capabilities", Actions: nil, Public: true}
	closed := apidesc.Operation{
		Method:  "GET",
		Path:    "/api/dustpan/v1/capabilities",
		Actions: []string{"caller:read"},
		Public:  false,
	}

	d, err := apidesc.Diff([]apidesc.Operation{public}, []apidesc.Operation{closed})
	if err != nil || len(d.Mismatch) != 1 || d.Mismatch[0] != "GET /api/dustpan/v1/capabilities" {
		t.Fatalf("Diff() = %+v, %v", d, err)
	}
}

func TestDiffFailsClosed(t *testing.T) {
	t.Parallel()

	routes := backupRoutes()
	inputs := map[string][]apidesc.Operation{
		"outside the prefix": {op("GET", "/outside", "installation:backup")},
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
