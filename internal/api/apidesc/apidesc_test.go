package apidesc_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"unicode"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// idOf builds a valid operationId from a method and a path, so that two
// operations with different keys have different IDs.
func idOf(method, path string) string {
	var b strings.Builder

	b.WriteString(strings.ToLower(method))

	for _, r := range path {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}

	return b.String()
}

func op(method, path string, actions ...string) apidesc.Operation {
	objects := make(map[string]string, len(actions))
	for _, action := range actions {
		objects[action] = apidesc.KindNamespace
	}

	return apidesc.Operation{
		ID: idOf(method, path), Method: method, Path: path, Tags: []string{"backups"}, Actions: actions,
		ActionObjects: objects,
		Public:        false, IdempotencyKey: false, IfMatch: false, IfMatchRequired: false, ETag: false, Query: nil,
		Status: 0, Bodies: &apidesc.Bodies{Request: apidesc.NoBody, Response: apidesc.NoBody},
	}
}

// publicOp returns a public GET operation.
func publicOp(path string, actions ...string) apidesc.Operation {
	o := op("GET", path, actions...)
	o.Public = true

	return o
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
		{"file suffix not last", op("GET", "/api/dustpan/v1/openapi.json/x", "installation:backup")},
		{"file suffix twice", op("GET", "/api/dustpan/v1/openapi.json.json", "installation:backup")},
		{"file suffix with a capital", op("GET", "/api/dustpan/v1/openapi.JSON", "installation:backup")},
		{"other suffix", op("GET", "/api/dustpan/v1/openapi.yaml", "installation:backup")},
		{"both If-Match flags", func() apidesc.Operation {
			o := op("GET", "/api/dustpan/v1/backups", "installation:backup")
			o.IfMatch, o.IfMatchRequired = true, true

			return o
		}()},
		{"other file name", op("GET", "/api/dustpan/v1/backups.json", "installation:backup")},
		{"file suffix alone", op("GET", "/api/dustpan/v1/.json", "installation:backup")},
		{"dot in the middle", op("GET", "/api/dustpan/v1/open.api.json", "installation:backup")},
		{"no actions on a route that is not public", op("GET", "/api/dustpan/v1/backups")},
		{"actions on a public route", publicOp("/api/dustpan/v1/capabilities", "caller:read")},
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
		publicOp("/api/dustpan/v1/capabilities"),
		publicOp("/api/dustpan/v1/openapi.json"),
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

	public := publicOp("/api/dustpan/v1/capabilities")
	closed := op("GET", "/api/dustpan/v1/capabilities", "caller:read")

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

func TestValidateRefusesBadSuccessStatus(t *testing.T) {
	t.Parallel()

	typed := op("GET", "/api/dustpan/v1/backups", "installation:backup")
	typed.Bodies = &apidesc.Bodies{Request: apidesc.NoBody, Response: apidesc.Typed[struct{}]("Empty")}

	for name, change := range map[string]func(o *apidesc.Operation){
		"below 200":             func(o *apidesc.Operation) { o.Status = 199 },
		"redirect":              func(o *apidesc.Operation) { o.Status = http.StatusFound },
		"error":                 func(o *apidesc.Operation) { o.Status = http.StatusNotFound },
		"negative":              func(o *apidesc.Operation) { o.Status = -204 },
		"204 with a typed body": func(o *apidesc.Operation) { *o = typed; o.Status = http.StatusNoContent },
	} {
		o := op("GET", "/api/dustpan/v1/backups", "installation:backup")
		change(&o)

		if err := o.Validate(); !errors.Is(err, apidesc.ErrInvalidOperation) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidOperation", name, err)
		}
	}
}

func TestValidateRefusesBadIDAndTags(t *testing.T) {
	t.Parallel()

	good := op("GET", "/api/dustpan/v1/backups", "installation:backup")

	tests := []struct {
		name   string
		change func(o *apidesc.Operation)
	}{
		{"no ID", func(o *apidesc.Operation) { o.ID = "" }},
		{"ID with a capital first", func(o *apidesc.Operation) { o.ID = "ListBackups" }},
		{"ID with a dash", func(o *apidesc.Operation) { o.ID = "list-backups" }},
		{"ID with a space", func(o *apidesc.Operation) { o.ID = "list backups" }},
		{"ID with a line feed", func(o *apidesc.Operation) { o.ID = "listBackups\nX: y" }},
		{"ID with a path", func(o *apidesc.Operation) { o.ID = "list/backups" }},
		{"ID too short", func(o *apidesc.Operation) { o.ID = "ab" }},
		{"ID too long", func(o *apidesc.Operation) { o.ID = "a" + strings.Repeat("b", 64) }},
		{"no tag", func(o *apidesc.Operation) { o.Tags = nil }},
		{"four tags", func(o *apidesc.Operation) { o.Tags = []string{"a", "b", "c", "d"} }},
		{"empty tag", func(o *apidesc.Operation) { o.Tags = []string{""} }},
		{"tag with a capital", func(o *apidesc.Operation) { o.Tags = []string{"Backups"} }},
		{"tag with a space", func(o *apidesc.Operation) { o.Tags = []string{"my backups"} }},
		{"tag with a line feed", func(o *apidesc.Operation) { o.Tags = []string{"backups\n"} }},
		{"tag twice", func(o *apidesc.Operation) { o.Tags = []string{"backups", "backups"} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			o := good
			o.Tags = append([]string(nil), good.Tags...)
			tt.change(&o)

			if err := o.Validate(); !errors.Is(err, apidesc.ErrInvalidOperation) {
				t.Fatalf("Validate() = %v, want ErrInvalidOperation", err)
			}
		})
	}
}

func TestDiffContractFieldsDiffer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(o *apidesc.Operation)
	}{
		{"operationId renamed", func(o *apidesc.Operation) { o.ID = "renamedOperation" }},
		{"tag changed", func(o *apidesc.Operation) { o.Tags = []string{"restores"} }},
		{"tag added", func(o *apidesc.Operation) { o.Tags = []string{"backups", "restores"} }},
		{"Idempotency-Key flag", func(o *apidesc.Operation) { o.IdempotencyKey = true }},
		{"If-Match flag", func(o *apidesc.Operation) { o.IfMatch = true }},
		{"required If-Match flag", func(o *apidesc.Operation) { o.IfMatchRequired = true }},
		{"ETag flag", func(o *apidesc.Operation) { o.ETag = true }},
		{"success status", func(o *apidesc.Operation) { o.Status = http.StatusCreated }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			changed := backupRoutes()
			changed[0].Tags = append([]string(nil), changed[0].Tags...)
			tt.change(&changed[0])

			d, err := apidesc.Diff(backupRoutes(), changed)
			if err != nil || len(d.Mismatch) != 1 || d.Mismatch[0] != "GET /api/dustpan/v1/backups" {
				t.Fatalf("Diff() = %+v, %v", d, err)
			}
		})
	}
}

func TestDiffRefusesRepeatedID(t *testing.T) {
	t.Parallel()

	twice := backupRoutes()
	twice[1].ID = twice[0].ID

	if _, err := apidesc.Diff(twice, backupRoutes()); !errors.Is(err, apidesc.ErrInvalidOperation) {
		t.Fatalf("routes with a repeated ID: err = %v", err)
	}

	if _, err := apidesc.Diff(backupRoutes(), twice); !errors.Is(err, apidesc.ErrInvalidOperation) {
		t.Fatalf("description with a repeated ID: err = %v", err)
	}
}
