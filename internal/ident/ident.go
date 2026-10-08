// Package ident validates the identifiers shared by namespaces, projects and stacks.
package ident

// Valid reports whether s is 1..maxLen characters of ASCII letters, digits,
// '-', '_' and '.', and does not start with '.' or '-'.
func Valid(s string, maxLen int) bool {
	if s == "" || len(s) > maxLen || s[0] == '.' || s[0] == '-' {
		return false
	}

	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return false
		}
	}

	return true
}
