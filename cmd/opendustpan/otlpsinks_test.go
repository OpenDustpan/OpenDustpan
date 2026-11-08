package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOTLPAuditSinkOpensWithAndWithoutHeaders(t *testing.T) {
	t.Parallel()

	cases := map[string]map[string]string{
		"no headers":       {},
		"headers variable": {otlpHeadersEnvVar: "Authorization=Bearer%20abc"},
	}

	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sinks, err := openTestSinks(t, []string{"otlp:https://collector.example:4318"}, envOf(vars))
			if err != nil {
				t.Fatalf("open: %v", err)
			}

			if len(sinks.ship) != 1 || sinks.ship[0].Name != "otlp" || sinks.ship[0].Kind != "otlp" {
				t.Fatalf("ship = %+v, want one sink named otlp", sinks.ship)
			}
		})
	}
}

func TestOTLPAuditSinkTakesAName(t *testing.T) {
	t.Parallel()

	sinks, err := openTestSinks(t, []string{"collector=otlp:https://collector.example:4318"}, noEnv)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if sinks.ship[0].Name != "collector" || sinks.ship[0].Kind != "otlp" {
		t.Fatalf("ship = %+v, want name collector, kind otlp", sinks.ship[0])
	}
}

func TestOTLPSinkRefusesBadItems(t *testing.T) {
	t.Parallel()

	const canary = "canary-value"

	cases := map[string]struct {
		vars map[string]string
		raw  string
	}{
		"plain http":            {raw: "otlp:http://collector.example:4318", vars: nil},
		"no URL":                {raw: "otlp:", vars: nil},
		"user info":             {raw: "otlp:https://u:" + canary + "@collector.example", vars: nil},
		"header without equals": {raw: "otlp:https://c.example", vars: map[string]string{otlpHeadersEnvVar: canary}},
		"header without name": {
			raw:  "otlp:https://c.example",
			vars: map[string]string{otlpHeadersEnvVar: "=" + canary},
		},
		"bad escape": {
			raw:  "otlp:https://c.example",
			vars: map[string]string{otlpHeadersEnvVar: "X-A=" + canary + "%zz"},
		},
		"CRLF through escape": {
			raw:  "otlp:https://c.example",
			vars: map[string]string{otlpHeadersEnvVar: "X-A=" + canary + "%0D%0AX-B:1"},
		},
		"reserved name": {
			raw:  "otlp:https://c.example",
			vars: map[string]string{otlpHeadersEnvVar: "Content-Type=" + canary},
		},
		"name twice": {
			raw:  "otlp:https://c.example",
			vars: map[string]string{otlpHeadersEnvVar: "X-A=" + canary + ",x-a=" + canary},
		},
		"headers file missing": {
			raw:  "otlp:https://c.example",
			vars: map[string]string{otlpHeadersFileEnvVar: "/nonexistent/headers"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := openTestSinks(t, []string{tc.raw}, envOf(tc.vars))
			if !errors.Is(err, errAuditSink) {
				t.Fatalf("open %q: got %v, want an --audit-sink error", tc.raw, err)
			}

			if strings.Contains(err.Error(), canary) {
				t.Fatalf("the error repeats a secret: %v", err)
			}
		})
	}
}

func TestOTLPHeadersParse(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		want map[string]string
		text string
	}{
		"one": {
			text: "Authorization=Bearer%20abc",
			want: map[string]string{"authorization": "Bearer abc"},
		},
		"two with commas":  {text: "X-A=1, X-B=2", want: map[string]string{"x-a": "1", "x-b": "2"}},
		"line breaks":      {text: "X-A=1\nX-B=2\r\n", want: map[string]string{"x-a": "1", "x-b": "2"}},
		"equals in value":  {text: "X-A=a=b", want: map[string]string{"x-a": "a=b"}},
		"encoded comma":    {text: "X-A=a%2Cb", want: map[string]string{"x-a": "a,b"}},
		"empty value":      {text: "X-A=", want: map[string]string{"x-a": ""}},
		"empty items skip": {text: ",,X-A=1,,", want: map[string]string{"x-a": "1"}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := otlpHeadersFrom(
				envOf(map[string]string{otlpHeadersEnvVar: tc.text}),
				otlpHeadersEnvVar,
				otlpHeadersFileEnvVar,
			)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOTLPHeadersRefuseControlCharacters(t *testing.T) {
	t.Parallel()

	const canary = "canary-value"

	cases := map[string]string{
		"CR in value":     "X-A=" + canary + "%0D",
		"LF in value":     "X-A=" + canary + "%0AX-B:1",
		"NUL in value":    "X-A=" + canary + "%00",
		"DEL in value":    "X-A=" + canary + "%7F",
		"tab in name":     "X-\tA=" + canary,
		"escape in name":  "X-\x1bA=" + canary,
		"after good item": "X-A=1,X-B=" + canary + "%0D%0A",
	}

	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := otlpHeadersFrom(
				envOf(map[string]string{otlpHeadersEnvVar: text}),
				otlpHeadersEnvVar,
				otlpHeadersFileEnvVar,
			)
			if err == nil || !errors.Is(err, errSecretControlChar) {
				t.Fatalf("otlpHeadersFrom = %v, %v; want %v", got, err, errSecretControlChar)
			}

			if strings.Contains(err.Error(), canary) {
				t.Fatalf("the error repeats a value: %v", err)
			}
		})
	}
}

func TestOTLPHeadersComeFromAPrivateFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	private := filepath.Join(dir, "private")

	if err := os.WriteFile(private, []byte("X-A=1\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", private, err)
	}

	got, err := otlpHeadersFrom(
		envOf(map[string]string{otlpHeadersFileEnvVar: private}),
		otlpHeadersEnvVar,
		otlpHeadersFileEnvVar,
	)
	if err != nil || got["x-a"] != "1" {
		t.Fatalf("private file: got %v, %v", got, err)
	}
}

func TestOTLPHeadersAreSeparatePerSetting(t *testing.T) {
	t.Parallel()

	// The headers of the audit sink must not serve the log sink: each
	// setting reads its own variables.
	vars := envOf(map[string]string{otlpHeadersEnvVar: "X-A=audit"})

	audit, err := otlpHeadersFrom(vars, otlpHeadersEnvVar, otlpHeadersFileEnvVar)
	if err != nil || audit["x-a"] != "audit" {
		t.Fatalf("audit headers: got %v, %v", audit, err)
	}

	logHeaders, err := otlpHeadersFrom(vars, logOTLPHeadersEnvVar, logOTLPHeadersFileEnvVar)
	if err != nil || len(logHeaders) != 0 {
		t.Fatalf("log headers: got %v, %v, want none", logHeaders, err)
	}

	app, err := newTestAppLog(t, "siem=otlp:https://logs.example:4318", &bytes.Buffer{}, vars)
	if err != nil {
		t.Fatalf("new app log: %v", err)
	}

	if len(app.sinks) != 1 {
		t.Fatalf("sinks = %d, want 1", len(app.sinks))
	}
}
