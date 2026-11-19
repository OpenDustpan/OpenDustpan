package pulumi

import "testing"

// TestOneLineRemovesCRAndLF sends hostile text straight to the helper, as if
// the boundary checks and the logger encoding both failed.
func TestOneLineRemovesCRAndLF(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"plain":                  "plain",
		"a\r\nlevel=ERROR forge": "alevel=ERROR forge",
		"\n\n":                   "",
		"x\ry":                   "xy",
	}
	for in, want := range cases {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}
