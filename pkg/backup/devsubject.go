package backup

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// CleanSubject returns subject without CR, LF and every other control
// character, without format characters, line and paragraph separators and
// invalid UTF-8, cut to MaxReportedSubjectBytes at a character boundary. cut
// is true when the cut shortened the cleaned text. A subject that comes from
// a backup is untrusted: the strip, the restore service and the sign-in
// denial call this one function before they write a subject to an audit
// entry, the restore record, a log line or an API answer.
func CleanSubject(subject string) (string, bool) {
	var b strings.Builder

	b.Grow(min(len(subject), MaxReportedSubjectBytes+utf8.UTFMax))

	for _, r := range subject {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			r == ' ' || r == ' ' {
			continue
		}

		b.WriteRune(r)
	}

	clean := b.String()
	if len(clean) <= MaxReportedSubjectBytes {
		return clean, false
	}

	end := MaxReportedSubjectBytes
	for end > 0 && !utf8.RuneStart(clean[end]) {
		end--
	}

	return clean[:end], true
}
