// Package logguard is a log/slog handler that escapes hidden runes in a
// record before the next handler gets it. A hidden rune is a control
// character such as CR, LF, NUL or ESC, a format character such as U+202E,
// or the line and paragraph separators U+2028 and U+2029. An invalid UTF-8
// byte is escaped too. A value from outside the process can hold such a
// rune, and a raw rune in a log line can forge a second line or hide text.
//
// The handler escapes the message, every attribute key, every string value,
// and the text of an error, a fmt.Stringer, an encoding.TextMarshaler and a
// []byte. It resolves each slog.LogValuer first, and it walks nested
// groups. It escapes group names and the attributes that WithAttrs adds.
// Any other value, for example a struct, a map, a slice or a pointer,
// becomes its JSON text, in which the handler escapes each string and each
// object key. A JSON handler writes that text as JSON, so the structure
// stays. A value that has no JSON form becomes "!ERROR:" and the escaped
// text of the encode error, as log/slog writes it. This includes a value
// nested deeper than the depth limit of encoding/json.
//
// The escape is visible text in the style of Go quoting, for example "\n",
// "\x1b" or "\u2028", so a reader can see that the rune was there. A reader
// that decodes the JSON of a structured value sees the same visible text.
//
// A log call never stops the process: a nil pointer with a method gives
// "<nil>", and a panic in a method gives "!PANIC" text.
package logguard

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strconv"
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
				out = slog.StringValue(Escape(panicText(r)))
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
	case json.RawMessage:
		// Raw JSON is a value, not text: it goes through the JSON walk. It
		// comes first, because the type can also have a String method.
		return jsonValue(t)
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

// panicText answers the text of the panic value r. It formats r only when
// it is a string or an error, and it recovers a second panic in Error: fmt
// would call a method of r again and stop the process. Any other value
// gives only its type.
func panicText(r any) string {
	text := fmt.Sprintf("!PANIC: %T", r)

	func() {
		defer func() {
			if recover() != nil {
				text = fmt.Sprintf("!PANIC: %T", r)
			}
		}()

		switch v := r.(type) {
		case string:
			text = "!PANIC: " + v
		case error:
			text = "!PANIC: " + v.Error()
		default:
		}
	}()

	return text
}

// nilText is the text of a nil pointer, as log/slog writes it.
const nilText = "<nil>"

// isNilPointer reports whether x holds a nil pointer.
func isNilPointer(x any) bool {
	v := reflect.ValueOf(x)

	return v.Kind() == reflect.Pointer && v.IsNil()
}

// jsonValue answers the JSON text of x with Escape run on each string and
// each object key, as a value that a JSON handler writes as JSON and a text
// handler writes as text. When x has no JSON form, for example a cycle or a
// channel, it answers "!ERROR:" and the escaped text of the encode error.
// It never formats x with fmt: the default text of a cycle never ends.
func jsonValue(x any) slog.Value {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(x); err != nil {
		return errorValue(err)
	}

	// Encode appends a newline. A log value holds none.
	return escapeEncoded(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

// escapeEncoded answers the JSON text js, which an encoder wrote, with
// Escape run on each string and each object key.
func escapeEncoded(js []byte) slog.Value {
	// A string of js can hold a hidden rune only as a raw rune or as a
	// JSON escape, which starts with a backslash.
	if !NeedsEscape(string(js)) && bytes.IndexByte(js, '\\') < 0 {
		return slog.AnyValue(escapedJSON(js))
	}

	return walkValue(newTokenSource(js))
}

// walkValue answers the JSON text that src reads, with Escape run on each
// string and each object key, or "!ERROR:" and the escaped text of the
// error of the walk.
func walkValue(src tokenSource) slog.Value {
	out, err := escapeTokens(src)
	if err != nil {
		// Defense in depth: the encoder checks its own output, and the
		// decoder and the encoder share one depth limit, so no input that
		// the encoder wrote fails the walk today. This branch stays so that
		// text that the walk cannot read never reaches the handler, if the
		// encoder or the decoder changes.
		return errorValue(err)
	}

	return slog.AnyValue(escapedJSON(out))
}

// errorValue answers "!ERROR:" and the escaped text of err, as the value of
// an attribute that has no JSON form. An error text can hold a value from
// outside, for example the text of an error from a MarshalJSON method.
func errorValue(err error) slog.Value {
	return slog.StringValue(Escape("!ERROR:" + err.Error()))
}

// errTrailing reports JSON text with more after its first value.
var errTrailing = errors.New("JSON text has more after its value")

// escapeJSONStrings answers the JSON text js with Escape run on each string
// and each object key. It keeps the order of js.
func escapeJSONStrings(js []byte) ([]byte, error) {
	return escapeTokens(newTokenSource(js))
}

// newTokenSource answers a decoder of js that keeps each number as its
// text.
func newTokenSource(js []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(js))
	dec.UseNumber()

	return dec
}

// escapeTokens reads one JSON value from dec and answers it with Escape run
// on each string and each object key. It keeps the order of the value. It
// fails when dec has more after the value.
func escapeTokens(dec tokenSource) ([]byte, error) {
	var out bytes.Buffer

	if err := copyValue(&out, dec); err != nil {
		return nil, err
	}

	// Defense in depth: the encoder writes one value, so no input has more
	// text today. The check stays so that the walk never drops text that a
	// changed encoder writes after the value.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errTrailing
	}

	return out.Bytes(), nil
}

// tokenSource reads JSON tokens: a *json.Decoder.
type tokenSource interface {
	Token() (json.Token, error)
	More() bool
}

// copyValue reads one JSON value from dec and writes it to out, with each
// string escaped.
func copyValue(out *bytes.Buffer, dec tokenSource) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read JSON: %w", err)
	}

	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			return copyObject(out, dec)
		}

		return copyArray(out, dec)
	case string:
		return writeJSONString(out, Escape(t))
	case json.Number:
		out.WriteString(t.String())
	case bool:
		out.WriteString(strconv.FormatBool(t))
	case nil:
		out.WriteString("null")
	default:
		// Defense in depth: json.Decoder answers no other token type, so no
		// input reaches this branch today. It fails closed for a token
		// type that a changed decoder could add.
		return fmt.Errorf("%w: %T", errToken, tok)
	}

	return nil
}

// errToken reports a JSON token of a type that copyValue does not know.
var errToken = errors.New("unknown JSON token")

// copyObject writes the members of an object, whose "{" dec has read, with
// each key and each string escaped.
func copyObject(out *bytes.Buffer, dec tokenSource) error {
	out.WriteByte('{')

	for i := 0; dec.More(); i++ {
		if i > 0 {
			out.WriteByte(',')
		}

		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("read JSON key: %w", err)
		}

		// Defense in depth: json.Decoder answers only a string for a key,
		// so no input reaches this check today. It stays so that a key of
		// another type never reaches the output unescaped, if the token
		// source changes. A test reaches it with a fake token source.
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("%w: key %T", errToken, tok)
		}

		if err := writeJSONString(out, Escape(key)); err != nil {
			return err
		}

		out.WriteByte(':')

		if err := copyValue(out, dec); err != nil {
			return err
		}
	}

	return closeDelim(out, dec, '}')
}

// copyArray writes the items of an array, whose "[" dec has read, with each
// string escaped.
func copyArray(out *bytes.Buffer, dec tokenSource) error {
	out.WriteByte('[')

	for i := 0; dec.More(); i++ {
		if i > 0 {
			out.WriteByte(',')
		}

		if err := copyValue(out, dec); err != nil {
			return err
		}
	}

	return closeDelim(out, dec, ']')
}

// closeDelim reads the closing delimiter of an object or an array and
// writes it.
func closeDelim(out *bytes.Buffer, dec tokenSource, end byte) error {
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("read JSON: %w", err)
	}

	out.WriteByte(end)

	return nil
}

// writeJSONString writes s as a JSON string, without HTML escapes.
func writeJSONString(out *bytes.Buffer, s string) error {
	var tmp bytes.Buffer

	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("write JSON string: %w", err)
	}

	out.Write(bytes.TrimSuffix(tmp.Bytes(), []byte("\n")))

	return nil
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
