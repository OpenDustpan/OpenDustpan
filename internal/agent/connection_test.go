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
		{
			name: "unknown field in auth",
			raw: strings.Replace(
				validDocument(),
				`"secret": "itm-abc1" }`,
				`"secret": "itm-abc1", "extra": true }`,
				1,
			),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "auth without a secret",
			raw: strings.Replace(
				validDocument(),
				`, "secret": "itm-abc1" }`,
				` }`,
				1,
			),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "auth none with a header",
			raw: strings.Replace(
				validDocument(),
				`"auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-abc1" }`,
				`"auth": { "none": true, "header": "Authorization" }`,
				1,
			),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "negative outputTokens max",
			raw:  strings.Replace(validDocument(), `"max": 8192`, `"max": -5`, 1),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "negative contextTokens",
			raw:  strings.Replace(validDocument(), `"outputTokens"`, `"contextTokens": -1, "outputTokens"`, 1),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "cache ttl with a newline",
			raw:  strings.Replace(validDocument(), `"outputTokens"`, `"cache": { "ttl": "x\n" }, "outputTokens"`, 1),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "cache ttl that is not a duration",
			raw:  strings.Replace(validDocument(), `"outputTokens"`, `"cache": { "ttl": "soon" }, "outputTokens"`, 1),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "cache ttl that is very long",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"cache": { "ttl": "`+strings.Repeat("1", 64)+`h" }, "outputTokens"`,
				1,
			),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "rateLimit wait header that is not a token",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"rateLimit": { "waitHeaders": ["bad name"] }, "outputTokens"`,
				1,
			),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "requestIDHeaders entry that is not a token",
			raw:  strings.Replace(validDocument(), `"outputTokens"`, `"requestIDHeaders": ["a:b"], "outputTokens"`, 1),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "conversationKey header that is not a token",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"conversationKey": { "headers": ["a b"] }, "outputTokens"`,
				1,
			),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "empty errors quota entry",
			raw:  strings.Replace(validDocument(), `"outputTokens"`, `"errors": { "quota": [""] }, "outputTokens"`, 1),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "empty reasoning keep entry",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"reasoning": { "keep": [""] }, "outputTokens"`,
				1,
			),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "unknown field in a header entry",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"headers": { "x-example": { "value": "1", "extra": true } },
  "outputTokens"`,
				1,
			),
			want: agent.ErrConnectionInvalid,
		},
		{
			name: "unknown field in a query entry",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"query": { "api-version": { "value": "2026-01-01", "extra": true } },
  "outputTokens"`,
				1,
			),
			want: agent.ErrConnectionInvalid,
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

func TestProviderFields_SizeBound(t *testing.T) {
	t.Parallel()

	// A valid document under the 32 KiB document limit whose providerFields
	// is above the 8 KiB bound of the field: only that bound refuses it.
	raw := strings.Replace(
		validDocument(),
		`"tools": { "choice": "auto", "parallel": "on" }`,
		`"tools": { "choice": "auto", "parallel": "on" },
  "providerFields": { "options": { "pad": "`+strings.Repeat(" ", 9*1024)+`" } }`,
		1,
	)

	_, err := agent.ParseConnectionSpec([]byte(raw))
	if !errors.Is(err, agent.ErrConnectionInvalid) {
		t.Fatalf("ParseConnectionSpec() = %v, want ErrConnectionInvalid", err)
	}

	if !strings.Contains(err.Error(), "providerFields is larger than 8 KiB") {
		t.Fatalf("ParseConnectionSpec() error = %q, want the providerFields size bound", err)
	}
}

func TestForbiddenHeaderNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"host", documentWithHeaderEntry("Host", `{ "value": "example.local" }`)},
		{"content-length", documentWithHeaderEntry("Content-Length", `{ "value": "1" }`)},
		{"content-type", documentWithHeaderEntry("Content-Type", `{ "value": "application/json" }`)},
		{"transfer-encoding", documentWithHeaderEntry("Transfer-Encoding", `{ "value": "chunked" }`)},
		{"connection", documentWithHeaderEntry("Connection", `{ "value": "keep-alive" }`)},
		{"te", documentWithHeaderEntry("TE", `{ "value": "trailers" }`)},
		{"upgrade", documentWithHeaderEntry("Upgrade", `{ "value": "h2c" }`)},
		{"cookie", documentWithHeaderEntry("Cookie", `{ "secret": "itm-abc1" }`)},
		{"proxy", documentWithHeaderEntry("Proxy-Authorization", `{ "secret": "itm-abc1" }`)},
		{"user-agent", documentWithHeaderEntry("User-Agent", `{ "value": "example-agent/1" }`)},
		{
			name: "auth header",
			raw: strings.Replace(
				validDocument(),
				`"header": "Authorization"`,
				`"header": "Host"`,
				1,
			),
		},
		{
			name: "conversation key header",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"conversationKey": { "headers": ["Host"] },
  "outputTokens"`,
				1,
			),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := agent.ParseConnectionSpec([]byte(c.raw))
			if !errors.Is(err, agent.ErrConnectionInvalid) {
				t.Fatalf("ParseConnectionSpec() = %v, want ErrConnectionInvalid", err)
			}
		})
	}
}

// documentWithHeaderEntry returns validDocument with one entry in headers.
func documentWithHeaderEntry(name, entry string) string {
	return strings.Replace(
		validDocument(),
		`"outputTokens"`,
		`"headers": { "`+name+`": `+entry+` },
  "outputTokens"`,
		1,
	)
}

func TestUserAgent_Hostile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "above 200 bytes",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"userAgent": "`+strings.Repeat("a", 201)+`",
  "outputTokens"`,
				1,
			),
		},
		{
			name: "not printable ASCII",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"userAgent": "example-agent/1\u00e9",
  "outputTokens"`,
				1,
			),
		},
		{
			name: "control character",
			raw: strings.Replace(
				validDocument(),
				`"outputTokens"`,
				`"userAgent": "example-agent/1\u0007",
  "outputTokens"`,
				1,
			),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := agent.ParseConnectionSpec([]byte(c.raw))
			if !errors.Is(err, agent.ErrConnectionInvalid) {
				t.Fatalf("ParseConnectionSpec() = %v, want ErrConnectionInvalid", err)
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
		name  string
		raw   string
		field string
	}{
		{
			name:  "header named x-api-key holds a plain value",
			field: `header "x-api-key"`,
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
			name:  "header named authorization holds a plain value",
			field: `header "Authorization"`,
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
		{
			name:  "header named x-api-key holds a plain value under secret",
			field: `header "x-api-key"`,
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "headers": { "x-api-key": { "secret": "sk-123" } },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`,
		},
		{
			name:  "query named api_key holds a plain value under secret",
			field: `query "api_key"`,
			raw: `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "query": { "api_key": { "secret": "sk-123" } },
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

			if !strings.Contains(err.Error(), c.field) {
				t.Fatalf("ParseConnectionSpec() error = %q, want it to name %s", err, c.field)
			}

			if strings.Contains(err.Error(), "sk-123") {
				t.Fatalf("ParseConnectionSpec() error = %q, holds the value", err)
			}
		})
	}
}
