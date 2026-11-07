package ident

import (
	"strings"
	"unicode"
)

// IsHiddenRune reports whether r is a control character (Cc), a format
// character (Cf, for example U+202E), a line separator (Zl, U+2028) or a
// paragraph separator (Zp, U+2029). Text that holds such a character can
// look different from its bytes, so a stored text that the server shows or
// logs refuses it. This is the one definition: do not write a copy of it.
func IsHiddenRune(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// HasHidden reports whether s holds a rune for which IsHiddenRune is true.
func HasHidden(s string) bool {
	return strings.ContainsFunc(s, IsHiddenRune)
}
