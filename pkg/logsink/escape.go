package logsink

import (
	"bytes"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
)

// EscapeJSON answers the JSON text js with each hidden rune and each
// invalid UTF-8 byte written as a JSON escape. A hidden rune is a control
// character such as CR, LF, NUL, ESC, DEL or NEL (U+0085), a format
// character such as U+202E, or U+2028 or U+2029. An invalid byte becomes
// "\ufffd". js must be compact JSON, as encoding/json writes it: such text
// holds a hidden rune or an invalid byte only inside a string, so each
// escape keeps the text valid JSON. Text without a hidden rune comes back
// as it is.
//
// encoding/json escapes the control characters below U+0020 and U+2028 and
// U+2029, but it writes DEL, the C1 controls such as NEL and the format
// characters raw, and a reader of a log file or a syslog receiver can take
// NEL as a line break. A sink calls EscapeJSON on each JSON text that it
// writes, so its output holds no hidden rune whatever the encoder does.
func EscapeJSON(js []byte) []byte {
	if !needsJSONEscape(js) {
		return js
	}

	out := bytes.NewBuffer(make([]byte, 0, len(js)))

	for i := 0; i < len(js); {
		r, size := utf8.DecodeRune(js[i:])

		switch {
		case r == utf8.RuneError && size == 1:
			out.WriteString(`\ufffd`)
		case ident.IsHiddenRune(r):
			writeJSONRuneEscape(out, r)
		default:
			out.Write(js[i : i+size])
		}

		i += size
	}

	return out.Bytes()
}

// needsJSONEscape reports whether js holds a hidden rune or an invalid
// UTF-8 byte.
func needsJSONEscape(js []byte) bool {
	for i := 0; i < len(js); {
		r, size := utf8.DecodeRune(js[i:])
		if (r == utf8.RuneError && size == 1) || ident.IsHiddenRune(r) {
			return true
		}

		i += size
	}

	return false
}

// maxBMPRune is the highest rune that one "\uXXXX" escape holds.
const maxBMPRune = 0xFFFF

// writeJSONRuneEscape writes r as a JSON "\uXXXX" escape, or as a UTF-16
// surrogate pair of two escapes when r is above U+FFFF.
func writeJSONRuneEscape(out *bytes.Buffer, r rune) {
	if r <= maxBMPRune {
		fmt.Fprintf(out, `\u%04x`, r)

		return
	}

	hi, lo := utf16.EncodeRune(r)
	fmt.Fprintf(out, `\u%04x\u%04x`, hi, lo)
}
