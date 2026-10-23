package namespace_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestParseNameStatesTheRule proves that a refused namespace name gives
// ErrInvalidName with the rule of a name and an echo of at most 40
// characters, and that an empty name gives the same text as an invalid one.
func TestParseNameStatesTheRule(t *testing.T) {
	t.Parallel()

	const rule = "use 1 to 100 ASCII letters, digits, -, _ and ., not starting with - or ."

	for _, tc := range []struct{ in, want string }{
		{in: "", want: `invalid namespace name: "": ` + rule},
		{in: "-x", want: `invalid namespace name: "-x": ` + rule},
		{
			in:   strings.Repeat("a", 101),
			want: `invalid namespace name: "` + strings.Repeat("a", 40) + `"... (cut, 101 characters): ` + rule,
		},
	} {
		_, err := namespace.ParseName(tc.in)
		if !errors.Is(err, namespace.ErrInvalidName) || err.Error() != tc.want {
			t.Errorf("ParseName(%.50q) = %v, want %s", tc.in, err, tc.want)
		}
	}
}
