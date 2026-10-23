package catalog_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/catalog"
)

func TestCanonicalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, in, want string
	}{
		{"integers", `[0, -0, 1, 1.0, 1e0, 10e-1, 100, 2e1]`, `[0,0,1,1,1,1,100,20]`},
		{"fractions", `[0.5, 1.25e1, -3.75, 0.1]`, `[0.5,12.5,-3.75,0.1]`},
		{
			"large and small numbers", `[1e21, 1e20, 1.5e-7, 0.000001, 123456789012345680000]`,
			`[1e+21,100000000000000000000,1.5e-7,0.000001,123456789012345680000]`,
		},
		{
			"the shortest digits of a double", `[0.30000000000000004, 9007199254740993]`,
			`[0.30000000000000004,9007199254740992]`,
		},
		{
			"object names by UTF-16 code units", `{"b":1,"a":2,"é":3,"😀":4,"ﬁ":5}`,
			`{"a":2,"b":1,"é":3,"😀":4,"ﬁ":5}`,
		},
		{
			"string escapes", `"é<\/\"\\\b\t\n\f\r\u0001\u001f\u007f"`,
			"\"é</\\\"\\\\\\b\\t\\n\\f\\r\\u0001\\u001f\u007f\"",
		},
		{"white space and nesting", "{ \"x\" : [ true , false , null , { } , [ ] ] }", `{"x":[true,false,null,{},[]]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := catalog.CanonicalJSON([]byte(tc.in))
			if err != nil || string(got) != tc.want {
				t.Fatalf("CanonicalJSON(%s) = %s, %v, want %s", tc.in, got, err, tc.want)
			}
		})
	}

	for _, bad := range []string{``, `{`, `1 2`, `1e400`} {
		if _, err := catalog.CanonicalJSON([]byte(bad)); !errors.Is(err, catalog.ErrInvalid) {
			t.Fatalf("CanonicalJSON(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}
