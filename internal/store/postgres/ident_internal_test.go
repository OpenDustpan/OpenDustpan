package postgres

import (
	"errors"
	"net/url"
	"testing"
)

// TestOpenRefusesSchemaThatIsNotPlain sends hostile search_path values
// straight to Open, as if no other layer checked them.
func TestOpenRefusesSchemaThatIsNotPlain(t *testing.T) {
	t.Parallel()

	for _, schema := range []string{`a"b`, "a.b", "$user", "x y", "1abc", "a-b"} {
		dsn := "postgres://tester@127.0.0.1:1/none?connect_timeout=1&search_path=" + url.QueryEscape(schema)

		st, err := Open(t.Context(), dsn)
		if st != nil {
			t.Fatalf("Open(search_path %q) = a store, want none", schema)
		}

		if !errors.Is(err, errIdentifier) {
			t.Errorf("Open(search_path %q) = %v, want errIdentifier", schema, err)
		}
	}
}

// TestCheckIdent checks the allowlist of plain identifiers.
func TestCheckIdent(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"public", "restore_stage_00ff", "_x", "A1", "test_0a1b_control"} {
		if err := checkIdent(name); err != nil {
			t.Errorf("checkIdent(%q) = %v, want nil", name, err)
		}
	}

	for _, name := range []string{"", `a"b`, "a.b", "1abc", "a b", "a-b", "$user", "a\nb", "été", "a;b"} {
		if err := checkIdent(name); !errors.Is(err, errIdentifier) {
			t.Errorf("checkIdent(%q) = %v, want errIdentifier", name, err)
		}
	}
}

// TestPgIdentDoublesQuotes sends hostile names straight to pgIdent, as if
// checkIdent had not run.
func TestPgIdentDoublesQuotes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, want string }{
		{"plain", `"plain"`},
		{`a"b`, `"a""b"`},
		{`"`, `""""`},
		{`x"; DROP SCHEMA public; --`, `"x""; DROP SCHEMA public; --"`},
		{"a.b", `"a.b"`},
		{"", `""`},
	} {
		if got := pgIdent(tc.name); got != tc.want {
			t.Errorf("pgIdent(%q) = %s, want %s", tc.name, got, tc.want)
		}
	}
}
