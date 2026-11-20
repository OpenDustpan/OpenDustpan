package logguard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/logguard"
	"opendustpan.org/opendustpan/pkg/logsink"
)

// hostileText answers the values that each layer must escape. The forged
// value holds a line that looks like a second record.
func hostileText() map[string]string {
	return map[string]string{
		"CR LF":     "a\r\nb",
		"LF":        "a\nb",
		"NUL":       "a\x00b",
		"ESC":       "a\x1b[31mb",
		"U+2028":    "a\u2028b",
		"U+2029":    "a\u2029b",
		"NEL":       "a\u0085b",
		"DEL":       "a\x7fb",
		"U+202E":    "a\u202eb",
		"bad UTF-8": "a\xffb",
		"forged":    "ok\nlevel=ERROR msg=forged",
	}
}

// textError is an error whose text is the test value.
type textError string

func (e textError) Error() string { return string(e) }

// logMessage logs v as the message through a log.Logger, so the message
// is not a constant.
func logMessage(l *slog.Logger, v string) {
	slog.NewLogLogger(l.Handler(), slog.LevelInfo).Print(v)
}

// rawHandler writes each record as one line with no escaping at all, as a
// handler or a sink that fails to escape would. Groups add a prefix.
type rawHandler struct {
	mu     *sync.Mutex
	out    *strings.Builder
	prefix string
	attrs  []string
}

func newRaw() *rawHandler {
	return &rawHandler{mu: &sync.Mutex{}, out: &strings.Builder{}, prefix: "", attrs: nil}
}

func (h *rawHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *rawHandler) Handle(_ context.Context, r slog.Record) error {
	parts := append([]string{r.Message}, h.attrs...)

	r.Attrs(func(a slog.Attr) bool {
		parts = appendRaw(parts, h.prefix, a)

		return true
	})

	h.mu.Lock()
	defer h.mu.Unlock()

	h.out.WriteString(strings.Join(parts, " ") + "\n")

	return nil
}

func (h *rawHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := append([]string(nil), h.attrs...)
	for _, a := range attrs {
		out = appendRaw(out, h.prefix, a)
	}

	return &rawHandler{mu: h.mu, out: h.out, prefix: h.prefix, attrs: out}
}

func (h *rawHandler) WithGroup(name string) slog.Handler {
	return &rawHandler{mu: h.mu, out: h.out, prefix: h.prefix + name + ".", attrs: h.attrs}
}

func (h *rawHandler) text() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.out.String()
}

// appendRaw writes a with its default text form and no escaping. It does
// not resolve a LogValuer, so it relies on the guard for that.
func appendRaw(parts []string, prefix string, a slog.Attr) []string {
	if a.Value.Kind() == slog.KindGroup {
		for _, m := range a.Value.Group() {
			parts = appendRaw(parts, prefix+a.Key+".", m)
		}

		return parts
	}

	return append(parts, prefix+a.Key+"="+fmt.Sprint(a.Value.Any()))
}

type stringer struct{ s string }

func (s stringer) String() string { return s.s }

type valuer struct{ s string }

func (v valuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("inner", v.s))
}

type texter struct {
	err error
	s   string
}

func (t texter) MarshalText() ([]byte, error) { return []byte(t.s), t.err }

type plain struct{ Name string }

// checkOneLine fails when out holds a hidden rune, an invalid byte, or more
// than one line.
func checkOneLine(t *testing.T, what, out string) {
	t.Helper()

	line := strings.TrimSuffix(out, "\n")
	if strings.Contains(line, "\n") {
		t.Errorf("%s: output holds more than one line: %q", what, out)
	}

	if !utf8.ValidString(line) {
		t.Errorf("%s: output holds invalid UTF-8: %q", what, out)
	}

	if r, found := ident.FirstHidden(line); found {
		t.Errorf("%s: output holds hidden rune %U: %q", what, r, out)
	}

	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(l, "level=ERROR msg=forged") {
			t.Errorf("%s: the forged line is its own line: %q", what, out)
		}
	}
}

func TestHandlerEscapesHostileValues(t *testing.T) {
	t.Parallel()

	cases := map[string]func(l *slog.Logger, v string){
		"message":      logMessage,
		"string value": func(l *slog.Logger, v string) { l.Info("m", slog.String("k", v)) },
		"key":          func(l *slog.Logger, v string) { l.Info("m", slog.String(v, "x")) },
		"with":         func(l *slog.Logger, v string) { l.With(slog.String("k", v)).Info("m") },
		"with key":     func(l *slog.Logger, v string) { l.With(slog.String(v, "x")).Info("m") },
		"group name":   func(l *slog.Logger, v string) { l.WithGroup(v).Info("m", slog.Int("n", 1)) },
		"with in group": func(l *slog.Logger, v string) {
			l.WithGroup("g").With(slog.String("k", v)).Info("m")
		},
		"nested group": func(l *slog.Logger, v string) {
			l.Info("m", slog.Group("a", slog.Group("b", slog.String("k", v))))
		},
		"group key":  func(l *slog.Logger, v string) { l.Info("m", slog.Group(v, slog.String("k", "x"))) },
		"error":      func(l *slog.Logger, v string) { l.Info("m", slog.Any("error", textError(v))) },
		"wrapped":    func(l *slog.Logger, v string) { l.Info("m", slog.Any("error", fmt.Errorf("x: %w", textError(v)))) },
		"stringer":   func(l *slog.Logger, v string) { l.Info("m", slog.Any("k", stringer{v})) },
		"log valuer": func(l *slog.Logger, v string) { l.Info("m", slog.Any("k", valuer{v})) },
		"text":       func(l *slog.Logger, v string) { l.Info("m", slog.Any("k", texter{err: nil, s: v})) },
		"text error": func(l *slog.Logger, v string) { l.Info("m", slog.Any("k", texter{err: textError(v), s: ""})) },
		"bytes":      func(l *slog.Logger, v string) { l.Info("m", slog.Any("k", []byte(v))) },
		"struct":     func(l *slog.Logger, v string) { l.Info("m", slog.Any("k", plain{v})) },
		"map":        func(l *slog.Logger, v string) { l.Info("m", slog.Any("k", map[string]string{"x": v})) },
	}

	for site, log := range cases {
		for name, value := range hostileText() {
			t.Run(site+"/"+name, func(t *testing.T) {
				t.Parallel()

				raw := newRaw()
				log(slog.New(logguard.New(raw)), value)

				out := raw.text()
				if out == "" {
					t.Fatal("no record reached the next handler")
				}

				checkOneLine(t, site, out)
			})
		}
	}
}

func TestHandlerKeepsVisibleEscape(t *testing.T) {
	t.Parallel()

	raw := newRaw()
	slog.New(logguard.New(raw)).Info("ok\nlevel=ERROR msg=forged", slog.String("k", "\x1b\u2028\xff"))

	want := `ok\nlevel=ERROR msg=forged k=\x1b\u2028\xff` + "\n"
	if got := raw.text(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestHandlerKeepsCleanValues(t *testing.T) {
	t.Parallel()

	raw := newRaw()
	slog.New(logguard.New(raw)).Info("plain ü 日本", slog.Any("k", plain{"x"}), slog.Int("n", 3))

	if got, want := raw.text(), `plain ü 日本 k={"Name":"x"} n=3`+"\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestHandlerLeavesCallerRecord(t *testing.T) {
	t.Parallel()

	r := slog.NewRecord(time.Time{}, slog.LevelInfo, "a\nb", 0)
	r.AddAttrs(slog.String("k", "c\nd"))

	if err := logguard.New(newRaw()).Handle(t.Context(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if r.Message != "a\nb" {
		t.Errorf("caller message changed to %q", r.Message)
	}

	r.Attrs(func(a slog.Attr) bool {
		if a.Value.String() != "c\nd" {
			t.Errorf("caller attribute changed to %q", a.Value.String())
		}

		return true
	})
}

func TestHandlerOverStdlibHandlers(t *testing.T) {
	t.Parallel()

	for name, value := range hostileText() {
		for format, mk := range map[string]func(*strings.Builder) slog.Handler{
			"json": func(b *strings.Builder) slog.Handler { return slog.NewJSONHandler(b, nil) },
			"text": func(b *strings.Builder) slog.Handler { return slog.NewTextHandler(b, nil) },
		} {
			t.Run(format+"/"+name, func(t *testing.T) {
				t.Parallel()

				var b strings.Builder

				l := slog.New(logguard.New(mk(&b)))
				r := slog.NewRecord(time.Date(2026, time.November, 20, 0, 0, 0, 0, time.UTC), slog.LevelInfo, value, 0)
				r.AddAttrs(slog.String(value, value), slog.Any("error", textError(value)))

				if err := l.Handler().Handle(t.Context(), r); err != nil {
					t.Fatalf("handle: %v", err)
				}

				checkOneLine(t, format, b.String())
			})
		}
	}
}

func TestEscape(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"":                "",
		"plain":           "plain",
		"a\r\nb":          `a\r\nb`,
		"\x00":            `\x00`,
		"\t":              `\t`,
		"\u0085":          `\x85`,
		"\u2029":          `\u2029`,
		"\xff":            `\xff`,
		"\U000E0001":      `\U000e0001`,
		`C:\path\to\file`: `C:\path\to\file`,
	} {
		if got := logguard.Escape(in); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
	}
}

type inner struct{ S string }

type outer struct{ P *inner }

// TestHandlerEscapesValuesBehindPointers sends values that hold a hidden
// rune behind a pointer. Their default text is only an address, so the
// guard must not trust it.
func TestHandlerEscapesValuesBehindPointers(t *testing.T) {
	t.Parallel()

	cases := map[string]func(l *slog.Logger, v string){
		"struct field": func(l *slog.Logger, v string) { l.Info("m", slog.Any("v", outer{P: &inner{S: v}})) },
		"pointer":      func(l *slog.Logger, v string) { l.Info("m", slog.Any("v", &inner{S: v})) },
		"map": func(l *slog.Logger, v string) {
			l.Info("m", slog.Any("v", map[string]*inner{"k": {S: v}}))
		},
		"slice": func(l *slog.Logger, v string) { l.Info("m", slog.Any("v", []*inner{{S: v}})) },
		"logsink attr": func(l *slog.Logger, v string) {
			l.Info("m", slog.Any("v", logsink.Attr{Value: &inner{S: v}, Key: "k"}))
		},
		"with": func(l *slog.Logger, v string) { l.With(slog.Any("v", outer{P: &inner{S: v}})).Info("m") },
		"group": func(l *slog.Logger, v string) {
			l.Info("m", slog.Group("g", slog.Any("v", []*inner{{S: v}})))
		},
	}

	for _, format := range []string{"json", "text", "raw"} {
		for site, log := range cases {
			for name, value := range hostileText() {
				t.Run(format+"/"+site+"/"+name, func(t *testing.T) {
					t.Parallel()

					checkOneLine(t, format+" "+site, logThrough(format, func(l *slog.Logger) { log(l, value) }))
				})
			}
		}
	}
}

// logThrough runs log on a logger with the guard in front of the handler
// named by format (json, text or raw) and answers the output.
func logThrough(format string, log func(l *slog.Logger)) string {
	var b strings.Builder

	switch format {
	case "json":
		log(slog.New(logguard.New(slog.NewJSONHandler(&b, nil))))
	case "text":
		log(slog.New(logguard.New(slog.NewTextHandler(&b, nil))))
	default:
		raw := newRaw()
		log(slog.New(logguard.New(raw)))

		return raw.text()
	}

	return b.String()
}

func TestHandlerKeepsJSONStructure(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	slog.New(logguard.New(slog.NewJSONHandler(&b, nil))).Info("m", slog.Any("v", outer{P: &inner{S: "a<b\u0085"}}))

	if want := `"v":{"P":{"S":"a<b\\x85"}}`; !strings.Contains(b.String(), want) {
		t.Errorf("output = %q, want it to hold %q", b.String(), want)
	}
}

type ptrStringer struct{ s string }

func (p *ptrStringer) String() string { return p.s }

type ptrError struct{ s string }

func (p *ptrError) Error() string { return p.s }

// ptrTexter has a value receiver, so a nil *ptrTexter panics when Go
// copies the value for the call.
type ptrTexter struct {
	err error
	s   string
}

func (p ptrTexter) MarshalText() ([]byte, error) { return []byte(p.s), p.err }

type panicStringer struct{}

func (panicStringer) String() string { panic("a\nforged") }

type panicJSON struct{}

func (panicJSON) MarshalJSON() ([]byte, error) { panic("a\nforged") }

// TestHandlerNeverPanics sends values whose methods fail: a log call must
// not stop the process.
func TestHandlerNeverPanics(t *testing.T) {
	t.Parallel()

	var (
		nilStringer *ptrStringer
		nilError    *ptrError
		nilTexter   *ptrTexter
	)

	for name, tc := range map[string]struct {
		value any
		want  string
	}{
		"typed-nil stringer":   {value: nilStringer, want: "<nil>"},
		"typed-nil error":      {value: nilError, want: "<nil>"},
		"typed-nil text":       {value: nilTexter, want: "<nil>"},
		"text error":           {value: ptrTexter{err: textError("a\nb"), s: ""}, want: `!ERROR:a\nb`},
		"panic in String":      {value: panicStringer{}, want: `!PANIC: a\nforged`},
		"panic in MarshalJSON": {value: panicJSON{}, want: `!PANIC: a\nforged`},
		"nil error interface":  {value: error(nil), want: "<nil>"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw := newRaw()
			slog.New(logguard.New(raw)).Info("m", slog.Any("k", tc.value))

			want := "m k=" + tc.want + "\n"
			if got := raw.text(); got != want {
				t.Errorf("output = %q, want %q", got, want)
			}
		})
	}
}

func TestEscapeLine(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"plain":           "plain",
		"a\nb":            `a\nb`,
		`a\nb`:            `a\\nb`,
		`C:\x`:            `C:\\x`,
		"ok\nlevel=ERROR": `ok\nlevel=ERROR`,
		"a\x00\u2028":     `a\x00\u2028`,
	} {
		if got := logguard.EscapeLine(in); got != want {
			t.Errorf("EscapeLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// discard is a writer that drops its input, so the benchmark measures the
// work of the handler and not a write.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// BenchmarkHandle compares a JSON handler with and without the guard, for
// a record with three attributes, one of them a small struct.
func BenchmarkHandle(b *testing.B) {
	type small struct {
		Name  string
		Count int
	}

	for name, h := range map[string]slog.Handler{
		"plain": slog.NewJSONHandler(discard{}, nil),
		"guard": logguard.New(slog.NewJSONHandler(discard{}, nil)),
	} {
		b.Run(name, func(b *testing.B) {
			l := slog.New(h)

			b.ReportAllocs()

			for b.Loop() {
				l.Info("request", slog.String("route", "/api/stacks"), slog.Int("status", 200),
					slog.Any("unit", small{Name: "prod", Count: 3}))
			}
		})
	}
}

// bad holds hidden runes for the round of structured values: NEL, U+202E,
// DEL, CR LF and NUL.
const bad = "a\u0085b\u202ec\x7fd\r\nX\x00"

// checkDecodedStrings decodes the JSON line out and fails when a string or
// an object key in it holds a hidden rune: a reader that decodes the JSON
// must see the visible escape, not the rune.
func checkDecodedStrings(t *testing.T, out string) {
	t.Helper()

	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("output is not one JSON value: %v: %q", err, out)
	}

	for _, text := range decodedStrings(v, nil) {
		if r, found := ident.FirstHidden(text); found {
			t.Errorf("decoded string or key holds hidden rune %U: %q", r, text)
		}
	}
}

// decodedStrings appends each string and each object key of the decoded
// JSON value v to out.
func decodedStrings(v any, out []string) []string {
	switch x := v.(type) {
	case string:
		return append(out, x)
	case map[string]any:
		for k, m := range x {
			out = decodedStrings(m, append(out, k))
		}
	case []any:
		for _, m := range x {
			out = decodedStrings(m, out)
		}
	default:
	}

	return out
}

// TestHandlerEscapesDecodedJSON sends structured values with hidden runes in
// strings, keys and raw JSON. A reader that decodes the JSON line, for
// example with jq -r, must see the same visible escape as for a string
// value.
func TestHandlerEscapesDecodedJSON(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(map[string]string{bad: bad})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for name, value := range map[string]any{
		"struct field": outer{P: &inner{S: bad}},
		"map key":      map[string]*inner{bad: {S: bad}},
		"slice":        []any{bad, []string{bad}},
		"raw JSON":     json.RawMessage(raw),
		"logsink attr": logsink.Attr{Value: &inner{S: bad}, Key: bad},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var b strings.Builder

			slog.New(logguard.New(slog.NewJSONHandler(&b, nil))).Info("m", slog.Any("v", value))

			checkOneLine(t, name, b.String())
			checkDecodedStrings(t, b.String())

			if !strings.Contains(b.String(), `a\\x85b\\u202ec\\x7fd\\r\\nX\\x00`) {
				t.Errorf("output = %q, want the visible escape of the guard", b.String())
			}
		})
	}
}

// TestHandlerEscapesDecodedJSONForEachRune sends each hostile value alone
// in a struct field. encoding/json escapes some of them (CR, LF, NUL,
// U+2028), so the raw output holds no hidden rune, but a reader that
// decodes the JSON still must not see one.
func TestHandlerEscapesDecodedJSONForEachRune(t *testing.T) {
	t.Parallel()

	for name, value := range hostileText() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var b strings.Builder

			slog.New(logguard.New(slog.NewJSONHandler(&b, nil))).Info("m", slog.Any("v", inner{S: value}))

			checkOneLine(t, name, b.String())
			checkDecodedStrings(t, b.String())
		})
	}
}

// failMarshal fails its JSON encode with an error whose text is the test
// value.
type failMarshal struct{ s string }

func (f failMarshal) MarshalJSON() ([]byte, error) { return []byte(f.s), textError(f.s) }

// TestHandlerEscapesEncodeErrors sends values that have no JSON form. The
// guard writes "!ERROR:" and the escaped text of the encode error.
func TestHandlerEscapesEncodeErrors(t *testing.T) {
	t.Parallel()

	self := map[string]any{}
	self["self"] = self

	loop := []any{nil}
	loop[0] = loop

	for name, tc := range map[string]struct {
		value any
		want  string
	}{
		"error text": {value: failMarshal{s: bad}, want: `a\x85b\u202ec\x7fd\r\nX\x00`},
		"channel": {value: struct {
			C chan int
			S string
		}{C: nil, S: bad}, want: "unsupported type: chan int"},
		"map cycle":   {value: self, want: "encountered a cycle"},
		"slice cycle": {value: loop, want: "encountered a cycle"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw := newRaw()
			slog.New(logguard.New(raw)).Info("m", slog.Any("k", tc.value))

			out := raw.text()
			checkOneLine(t, name, out)

			if !strings.Contains(out, "k=!ERROR:") || !strings.Contains(out, tc.want) {
				t.Errorf("output = %q, want !ERROR: and %q", out, tc.want)
			}
		})
	}
}

// bomb panics with itself in its Error method, so a formatter that prints
// the panic value panics again.
type bomb struct{}

func (bomb) Error() string { panic(bomb{}) }

type bombStringer struct{}

func (bombStringer) String() string { panic(bomb{}) }

// TestHandlerSurvivesPanicValues sends methods whose panic value panics
// again when it is printed. The log call must still return.
func TestHandlerSurvivesPanicValues(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		value any
		want  string
	}{
		"error":    {value: bomb{}, want: "m k=!PANIC: logguard_test.bomb\n"},
		"stringer": {value: bombStringer{}, want: "m k=!PANIC: logguard_test.bomb\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw := newRaw()
			slog.New(logguard.New(raw)).Info("m", slog.Any("k", tc.value))

			if got := raw.text(); got != tc.want {
				t.Errorf("output = %q, want %q", got, tc.want)
			}
		})
	}
}
