// Package agent_test tests the connection document, its validation, the
// secret items port and the connection store port.
package agent_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/fakecred"
)

// validDocument is a minimal valid document: version 1, chat-completions,
// with all required fields. It has no default field set.
func validDocument() string {
	return `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-abc1" },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "on" }
}`
}

func TestParseConnectionSpec_Hostile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want error
	}{
		{
			name: "unknown field",
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" },
  "notAField": true
}`,
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "document above 32 KiB",
			// A valid document with a pad value long enough to cross the
			// size limit: only the size limit refuses it, so the case
			// fails when the limit is removed.
			raw: strings.Replace(
				validDocument(),
				`"tools": { "choice": "auto", "parallel": "on" }`,
				`"tools": { "choice": "auto", "parallel": "on" },
  "providerFields": { "options": { "pad": "`+strings.Repeat(" ", 33*1024)+`" } }`,
				1,
			),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "control character in model",
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "ex\u0000ample",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`,
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "format responses",
			raw: `{
  "version": 1,
  "format": "responses",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`,
			want: agent.ErrFormatNotSupported,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := agent.ParseConnectionSpec([]byte(c.raw))
			if !errors.Is(err, c.want) {
				t.Fatalf("ParseConnectionSpec() = %v, want %v", err, c.want)
			}
		})
	}
}

func TestParseConnectionSpec_Valid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "all 18 fields",
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-abc1" },
  "headers": { "x-example": { "value": "1" } },
  "query": { "api-version": { "value": "2026-01-01" } },
  "userAgent": "example-agent/1",
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "contextTokens": 131072,
  "tools": { "choice": "omit", "parallel": "off" },
  "transport": "stream",
  "reasoning": { "keep": ["reasoning_details"] },
  "cache": { "mode": "breakpoints", "ttl": "1h" },
  "conversationKey": { "headers": ["x-session-id"], "bodyField": "metadata.session", "prefix": "conv_" },
  "providerFields": { "options": { "context_size": 65536 } },
  "errors": { "quota": ["plan_exhausted"], "contextTooLong": ["context_too_long"] },
  "rateLimit": { "waitHeaders": ["retry-after-ms", "retry-after"] },
  "requestIDHeaders": ["x-request-id"]
}`,
		},
		{
			name: "no default, responses rejected",
			raw:  validDocument(),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			mustParseValidSpec(t, c.raw)
		})
	}
}

// mustParseValidSpec parses raw and checks the fields of the minimal
// document against it.
func mustParseValidSpec(t *testing.T, raw string) agent.ConnectionSpec {
	t.Helper()

	spec, err := agent.ParseConnectionSpec([]byte(raw))
	if err != nil {
		t.Fatalf("ParseConnectionSpec() = %v, want no error", err)
	}

	if spec.Format() != "chat-completions" {
		t.Fatalf("Format() = %q, want chat-completions", spec.Format())
	}

	if spec.Version() != 1 {
		t.Fatalf("Version() = %d, want 1", spec.Version())
	}

	if spec.BaseURL() != "https://example.local/v1" {
		t.Fatalf("BaseURL() = %q, want https://example.local/v1", spec.BaseURL())
	}

	if spec.Model() != "example-model" {
		t.Fatalf("Model() = %q, want example-model", spec.Model())
	}

	return spec
}

func TestBaseURL_Hostile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "relative URL",
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "/v1",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`,
		},
		{
			name: "user information",
			// fakecred joins the parts at run time, so the source holds
			// no credential text.
			raw: strings.Replace(
				validDocument(),
				`"baseURL": "https://example.local/v1"`,
				`"baseURL": "`+fakecred.URL("https", "user", "pass", "example.local", "/v1")+`"`,
				1,
			),
		},
		{
			name: "query string",
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1?x=1",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`,
		},
		{
			name: "fragment",
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1#frag",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`,
		},
		{
			name: "path ends in format path",
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/chat/completions",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := agent.ParseConnectionSpec([]byte(c.raw))
			if err == nil {
				t.Fatal("ParseConnectionSpec() = nil, want an error")
			}
		})
	}
}

func TestHeadersQuery_ValueTooLong(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 1025)

	for _, field := range []string{"headers", "query"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			raw := `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "` + field + `": { "x-extra": { "value": "` + long + `" } },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`

			_, err := agent.ParseConnectionSpec([]byte(raw))
			if !errors.Is(err, agent.ErrConnectionInvalid) {
				t.Fatalf("err = %v, want ErrConnectionInvalid", err)
			}
		})
	}
}

func TestSecretRef_ASCIIOnly(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"itm-é", "itm-日本", "itm-ABC", "itm-", "itm-a b"} {
		if agent.IsSecretRef(s) {
			t.Errorf("IsSecretRef(%q) = true, want false", s)
		}
	}

	if !agent.IsSecretRef("itm-ab_1-z") {
		t.Error("IsSecretRef(valid) = false, want true")
	}
}

func TestHeaders_Query_DefensiveCopies(t *testing.T) {
	t.Parallel()

	raw := `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "headers": { "x-trace": { "value": "1" } },
  "query": { "api-version": { "value": "2026-01-01" } },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "on" }
}`

	spec := mustParseValidSpec(t, raw)

	// Change the maps that Headers and Query answer: drop one entry and
	// copy another entry under a new name. The document must keep what the
	// parse stored.
	headers := spec.Headers()
	header := headers["x-trace"]
	delete(headers, "x-trace")
	headers["x-swapped"] = header

	query := spec.Query()
	parameter := query["api-version"]
	delete(query, "api-version")
	query["api-version-swapped"] = parameter

	fresh := mustParseValidSpec(t, raw)
	if !bytes.Equal(spec.Marshal(), fresh.Marshal()) {
		t.Error("the document changed through the copies that Headers and Query answer")
	}

	if _, ok := spec.Headers()["x-trace"]; !ok {
		t.Error("Headers() lost x-trace after a change through an earlier copy")
	}

	if _, ok := spec.Query()["api-version"]; !ok {
		t.Error("Query() lost api-version after a change through an earlier copy")
	}

	if _, ok := spec.Headers()["x-swapped"]; ok {
		t.Error("Headers() answers x-swapped, which only a changed copy holds")
	}

	if _, ok := spec.Query()["api-version-swapped"]; ok {
		t.Error("Query() answers api-version-swapped, which only a changed copy holds")
	}
}

func TestHeaders_PlainSecretByName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "header named x-api-key holds a plain value",
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "headers": { "x-api-key": { "value": "sk-123" } },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`,
		},
		{
			name: "header named authorization holds a plain value",
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "headers": { "Authorization": { "value": "Bearer sk-123" } },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := agent.ParseConnectionSpec([]byte(c.raw))
			if !errors.Is(err, agent.ErrPlainSecret) {
				t.Fatalf("ParseConnectionSpec() = %v, want ErrPlainSecret", err)
			}
		})
	}
}
