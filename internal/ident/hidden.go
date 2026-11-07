package ident

import (
	"strings"
	"unicode"
	"unicode/utf8"
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
//
// HasHidden does not refuse invalid UTF-8. Go decodes each invalid byte as
// U+FFFD, which is not a hidden rune. A caller must check utf8.ValidString
// too.
func HasHidden(s string) bool {
	_, found := FirstHidden(s)

	return found
}

// FirstHidden returns the first rune of s for which IsHiddenRune is true, and
// true. It returns 0 and false when s holds none. Like HasHidden, it does not
// refuse invalid UTF-8.
func FirstHidden(s string) (rune, bool) {
	i := strings.IndexFunc(s, IsHiddenRune)
	if i < 0 {
		return 0, false
	}

	r, _ := utf8.DecodeRuneInString(s[i:])

	return r, true
}
