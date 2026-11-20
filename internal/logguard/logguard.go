// Package logguard is a log/slog handler that escapes hidden runes in a
// record before the next handler gets it. A hidden rune is a control
// character such as CR, LF, NUL or ESC, a format character such as U+202E,
// or the line and paragraph separators U+2028 and U+2029. An invalid UTF-8
// byte is escaped too. A value from outside the process can hold such a
// rune, and a raw rune in a log line can forge a second line or hide text.
//
// The handler escapes the message, every attribute key, every string value,
// the text of an error, a fmt.Stringer and an encoding.TextMarshaler, and
// any other value whose default text holds a hidden rune. It resolves each
// slog.LogValuer first, and it walks nested groups. It escapes group names
// and the attributes that WithAttrs adds.
//
// The escape is visible text in the style of Go quoting, for example "\n",
// "\x1b" or "\u2028", so a reader can see that the rune was there.
package logguard

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/logsink"
)

// Handler escapes hidden runes in each record and passes the record to the
// next handler.
type Handler struct {
	next slog.Handler
}

var _ slog.Handler = (*Handler)(nil)

// New answers a handler that escapes hidden runes and then calls next.
func New(next slog.Handler) *Handler {
	return &Handler{next: next}
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle implements slog.Handler. It builds a new record with the escaped
// message and attributes, so the caller's record stays as it is.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, Escape(r.Message), r.PC)

	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(escapeAttr(a))

		return true
	})

	if err := h.next.Handle(ctx, out); err != nil {
		return fmt.Errorf("log record: %w", err)
	}

	return nil
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, 0, len(attrs))

	for _, a := range attrs {
		out = append(out, escapeAttr(a))
	}

	return &Handler{next: h.next.WithAttrs(out)}
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(Escape(name))}
}

// escapeAttr answers a with an escaped key and an escaped value.
func escapeAttr(a slog.Attr) slog.Attr {
	return slog.Attr{Key: Escape(a.Key), Value: escapeValue(a.Value.Resolve())}
}

// escapeValue answers v with each hidden rune of its text escaped. v is
// resolved. A number, a bool, a duration and a time hold no text from
// outside, so they stay as they are.
func escapeValue(v slog.Value) slog.Value {
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(Escape(v.String()))
	case slog.KindGroup:
		members := v.Group()
		out := make([]slog.Attr, 0, len(members))

		for _, m := range members {
			out = append(out, escapeAttr(m))
		}

		return slog.GroupValue(out...)
	case slog.KindAny, slog.KindLogValuer:
		return escapeAny(v.Any())
	case slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindBool, slog.KindDuration, slog.KindTime:
		return v
	}

	// Fail closed: a kind that this handler does not know becomes its
	// escaped text.
	return slog.StringValue(Escape(v.String()))
}

// escapeAny answers the value of an Any attribute. An error, a
// fmt.Stringer, an encoding.TextMarshaler and a []byte become their escaped
// text, because a handler writes them as text. Any other value becomes its
// JSON text with each hidden rune written as a JSON escape, so a JSON
// handler still writes its structure. The default text of such a value is
// not checked: it shows only the address of a value behind a pointer, and
// a handler can still write the value it points to.
//
// A log call must never stop the process: a nil pointer whose method has a
// pointer receiver becomes "<nil>", as log/slog writes it, and a panic in a
// method becomes "!PANIC" text, as fmt writes it.
func escapeAny(x any) slog.Value {
	if x == nil {
		return slog.AnyValue(nil)
	}

	if isNilPointer(x) {
		return slog.StringValue(nilText)
	}

	var out slog.Value

	func() {
		defer func() {
			if r := recover(); r != nil {
				out = slog.StringValue(Escape(fmt.Sprintf("!PANIC: %v", r)))
			}
		}()

		out = escapeMethods(x)
	}()

	return out
}

// escapeMethods answers the value of x, which is not nil, through its
// methods. A method can panic: escapeAny recovers.
func escapeMethods(x any) slog.Value {
	switch t := x.(type) {
	case error:
		return slog.StringValue(Escape(t.Error()))
	case fmt.Stringer:
		return slog.StringValue(Escape(t.String()))
	case encoding.TextMarshaler:
		text, err := t.MarshalText()
		if err != nil {
			return slog.StringValue(Escape("!ERROR:" + err.Error()))
		}

		return slog.StringValue(Escape(string(text)))
	case []byte:
		return slog.StringValue(Escape(string(t)))
	default:
		return jsonValue(t)
	}
}

// nilText is the text of a nil pointer, as log/slog writes it.
const nilText = "<nil>"

// isNilPointer reports whether x holds a nil pointer.
func isNilPointer(x any) bool {
	v := reflect.ValueOf(x)

	return v.Kind() == reflect.Pointer && v.IsNil()
}

// jsonValue answers the JSON text of x with each hidden rune escaped, as a
// value that a JSON handler writes as JSON and a text handler writes as
// text. When x has no JSON form, it answers the escaped default text of x.
func jsonValue(x any) slog.Value {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(x); err != nil {
		return slog.StringValue(Escape(fmt.Sprintf("%+v", x)))
	}

	// Encode appends a newline. A log value holds none.
	return slog.AnyValue(escapedJSON(logsink.EscapeJSON(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))))
}

// escapedJSON is JSON text that holds no hidden rune. A JSON handler writes
// it as JSON, a text handler and fmt write it as text.
type escapedJSON []byte

// MarshalJSON implements json.Marshaler.
func (j escapedJSON) MarshalJSON() ([]byte, error) { return j, nil }

// MarshalText implements encoding.TextMarshaler.
func (j escapedJSON) MarshalText() ([]byte, error) { return j, nil }

// String implements fmt.Stringer.
func (j escapedJSON) String() string { return string(j) }

// NeedsEscape reports whether s holds a hidden rune or an invalid UTF-8
// byte.
func NeedsEscape(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || ident.IsHiddenRune(r) {
			return true
		}

		i += size
	}

	return false
}

// Escape answers s with each hidden rune and each invalid UTF-8 byte
// replaced by visible text in the style of Go quoting: "\n", "\r", "\t",
// "\x00" to "\xff" for a rune or byte below 256, "\uXXXX" or "\UXXXXXXXX"
// for a higher rune. Other text stays as it is. The result holds no hidden
// rune. A backslash stays as it is: the JSON handler escapes it, so a
// reader can tell the two apart there. The text handler writes a backslash
// as it is when the value needs no quotes, so in text output an escaped
// line feed and a backslash with an "n" look the same. Use EscapeLine for
// text that no handler writes.
func Escape(s string) string {
	if !NeedsEscape(s) {
		return s
	}

	var b strings.Builder

	b.Grow(len(s) + escapeRoom)

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])

		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case ident.IsHiddenRune(r):
			writeEscapedRune(&b, r)
		default:
			b.WriteString(s[i : i+size])
		}

		i += size
	}

	return b.String()
}

// EscapeLine answers s as Escape does, and also writes each backslash as
// two. Use it for a line that no handler quotes, for example an error line
// on stderr: a reader can then tell an escaped line feed ("\n") from a
// backslash and an "n" ("\\n").
func EscapeLine(s string) string {
	// The backslashes of s are doubled first, so each backslash that Escape
	// adds stays single.
	return Escape(strings.ReplaceAll(s, `\`, `\\`))
}

// Limits of the short escape forms, and the room that Escape adds to its
// buffer for the escapes.
const (
	maxByteRune = 0xFF
	maxBMPRune  = 0xFFFF
	escapeRoom  = 16
)

// writeEscapedRune writes the Go quoting form of r.
func writeEscapedRune(b *strings.Builder, r rune) {
	switch {
	case r == '\n':
		b.WriteString(`\n`)
	case r == '\r':
		b.WriteString(`\r`)
	case r == '\t':
		b.WriteString(`\t`)
	case r <= maxByteRune:
		fmt.Fprintf(b, `\x%02x`, r)
	case r <= maxBMPRune:
		fmt.Fprintf(b, `\u%04x`, r)
	default:
		fmt.Fprintf(b, `\U%08x`, r)
	}
}
