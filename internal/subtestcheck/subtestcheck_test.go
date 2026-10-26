package subtestcheck_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/subtestcheck"
)

func TestCheckFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		// want holds the line and the variable of each finding.
		want []string
	}{
		{"a parent check of a variable that the subtest assigns", "reported_assign_test.go", []string{"17 opened"}},
		{"a parent check that reads a map in its arguments", "reported_arg_test.go", []string{"16 got"}},
		{"an append and a field increment in a loop", "reported_append_field_test.go", []string{"23 seen", "26 r"}},
		{"an atomic method and an atomic function", "reported_atomic_test.go", []string{"19 calls", "22 legacy"}},
		{"a delete call", "reported_delete_test.go", []string{"17 m"}},
		{"a group test wrapper", "clean_group_test.go", nil},
		{"a variable that no subtest writes", "clean_unwritten_test.go", nil},
		{"a subtest that is not parallel", "clean_serial_test.go", nil},
		{"a name that the subtest declares itself", "clean_local_name_test.go", nil},
		{"a t.Cleanup after the t.Run call", "clean_cleanup_test.go", nil},
		{"a deferred function", "clean_defer_test.go", nil},
		{"a parameter that shadows the variable", "clean_shadow_param_test.go", nil},
		{"a var that shadows the variable", "clean_shadow_var_test.go", nil},
		{"a check before the t.Run call", "clean_before_test.go", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join("testdata", tc.file), nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var got []string
			for _, fd := range subtestcheck.CheckFile(fset, f) {
				got = append(got, strconv.Itoa(fd.Pos.Line)+" "+fd.Variable)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("findings = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
	}{
		{"testdata is skipped when it is below the root", []string{"."}, 0, ""},
		{"a root with findings", []string{"testdata"}, 1, "reads"},
		{"a root that does not exist", []string{"missing"}, 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			code := subtestcheck.Run(tc.args, &out, &errOut)
			if code != tc.wantCode {
				t.Errorf("code = %d, want %d (stderr %q)", code, tc.wantCode, errOut.String())
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tc.wantOut)
			}
		})
	}
}
