package logsink_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/logsinktest"
)

// hostileRecord holds v in the message, a key, a string value and a raw
// JSON value.
func hostileRecord(v string) logsink.Record {
	raw, err := json.Marshal(map[string]string{"k": v})
	if err != nil {
		panic(err)
	}

	return logsink.Record{
		Time:    time.Date(2026, time.November, 20, 0, 0, 0, 0, time.UTC),
		Level:   logsink.LevelError,
		Message: v,
		Attrs: []logsink.Attr{
			logsink.String("k"+v, v),
			logsink.RawJSON("raw", raw),
			{Key: "list", Value: []string{v}},
		},
	}
}

func TestMarshalJSONEscapesHiddenRunes(t *testing.T) {
	t.Parallel()

	for name, v := range logsinktest.HostileText() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			line, err := logsink.MarshalJSON(hostileRecord(v))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			logsinktest.CheckNoHiddenRune(t, "MarshalJSON", line)

			var back map[string]any
			if err := json.Unmarshal(line, &back); err != nil {
				t.Fatalf("line is not JSON: %v: %q", err, line)
			}

			if want := strings.ToValidUTF8(v, "\uFFFD"); back["msg"] != want {
				t.Errorf("msg = %q, want %q", back["msg"], want)
			}
		})
	}
}

func TestTrimToFitKeepsEscape(t *testing.T) {
	t.Parallel()

	for name, v := range logsinktest.HostileText() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			long := strings.Repeat(v, 200)
			r := logsink.Record{
				Time:    time.Date(2026, time.November, 20, 0, 0, 0, 0, time.UTC),
				Level:   logsink.LevelError,
				Message: long,
				Attrs:   []logsink.Attr{logsink.String("note", long)},
			}

			out, ok, err := logsink.TrimToFit(r, 1024)
			if err != nil || !ok {
				t.Fatalf("trim: ok %v, err %v", ok, err)
			}

			line, err := logsink.MarshalJSON(out)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			if len(line) > 1024 {
				t.Errorf("line of %d bytes, want at most 1024", len(line))
			}

			logsinktest.CheckNoHiddenRune(t, "trimmed line", line)
		})
	}
}

func TestEscapeJSON(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		`"plain"`:        `"plain"`,
		"\"a\x7fb\"":     `"a\u007fb"`,
		"\"a\u0085b\"":   `"a\u0085b"`,
		"\"a\u2028b\"":   "\"a\\u2028b\"",
		"\"a\nb\"":       `"a\u000ab"`,
		"\"a\xffb\"":     `"a\ufffdb"`,
		"\"\U000E0001\"": `"\udb40\udc01"`,
		`"ü 日本 \u2028"`:  `"ü 日本 \u2028"`,
	} {
		got := string(logsink.EscapeJSON([]byte(in)))
		if got != want {
			t.Errorf("EscapeJSON(%q) = %q, want %q", in, got, want)
		}

		var s string
		if err := json.Unmarshal([]byte(got), &s); err != nil {
			t.Errorf("EscapeJSON(%q) = %q is not JSON: %v", in, got, err)
		}
	}
}
