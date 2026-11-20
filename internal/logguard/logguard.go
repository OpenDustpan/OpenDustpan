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
	"context"
	"encoding"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
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
// fmt.Stringer and an encoding.TextMarshaler become their escaped text,
// because a handler writes them as text. Any other value stays as it is
// when its default text holds no hidden rune, so a handler can still write
// its structure. Else it becomes its escaped default text.
func escapeAny(x any) slog.Value {
	switch t := x.(type) {
	case nil:
		return slog.AnyValue(nil)
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
		text := fmt.Sprintf("%+v", t)
		if !NeedsEscape(text) {
			return slog.AnyValue(t)
		}

		return slog.StringValue(Escape(text))
	}
}

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
// rune.
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
