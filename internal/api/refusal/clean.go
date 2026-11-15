package refusal

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
)

// Clean returns s with each control character (Cc), format character (Cf),
// line separator and paragraph separator written as \uXXXX, and each byte that
// is not valid UTF-8 replaced by U+FFFD, cut to at most limit bytes at a rune
// boundary. A value that comes from a request goes through Clean before it
// enters an audit entry, a log line or a reason. Clean changes the text to make
// it safe to store; it is not an input check, and the caller still validates the input.
func Clean(s string, limit int) string {
	var b strings.Builder

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size

		var piece string

		switch {
		case r == utf8.RuneError && size <= 1:
			piece = `�`
		case ident.IsHiddenRune(r):
			piece = fmt.Sprintf(`\u%04x`, r)
		default:
			piece = string(r)
		}

		if b.Len()+len(piece) > limit {
			break
		}

		b.WriteString(piece)
	}

	return b.String()
}
