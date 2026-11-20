package logguard_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/logguard"
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

	if got, want := raw.text(), "plain ü 日本 k={x} n=3\n"; got != want {
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
