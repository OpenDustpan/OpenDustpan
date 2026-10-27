package mattermost_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/notify/chat/chattest"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
	"opendustpan.org/opendustpan/pkg/notify/chat/mattermost"
)

const zw = "\u200b"

// decode returns the markdown text of a Mattermost body, after the decoding
// of the JSON string escapes.
func decode(body []byte) ([]string, error) {
	var msg struct {
		Text string `json:"text"`
	}

	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err //nolint:wrapcheck // A test helper.
	}

	return []string{msg.Text}, nil
}

func harness(t *testing.T) chattest.Harness {
	t.Helper()

	return chattest.Harness{
		Renderer:     mattermost.New(),
		Platform:     chat.Mattermost,
		Host:         "chat.example.com",
		SingleHost:   false,
		OperatorHost: true,
		MaxBody:      0,
		Decode:       decode,
		RefusedHosts: []string{
			"127.0.0.1", "10.0.0.5", "[::1]", "chat.example.com:0", "chat.example.com:99999", "CHAT.example.com",
			"chat example.com", "chat.example.com/path", "chat.example.com?x=1", "chat.example.com#x", "名前.example",
		},
		Markup: []string{"<", ">", "@channel", "@here", "@all"},
		Hostile: []string{
			"@channel", "@here", "@all", "@admin", "~town-square", "#hashtag", "[click](https://evil.example)",
			"![x](https://evil.example/p.png)", "<script>alert(1)</script>", "`code`", "**bold**", "__bold__",
			"~~strike~~", "[x]: https://evil.example", "https://evil.example", "www.evil.example", ":smile:", ":+1:",
			"| a | b |", "<https://evil.example>", "&lt;b&gt;", "```\ncode\n```", "---", "<<@channel>>",
		},
		Accepted: chattest.Answer{Status: http.StatusOK, Body: []byte("ok")},
		Refusals: []chattest.Answer{
			{
				Status: http.StatusOK,
				Body:   []byte(`{"id":"web.incoming_webhook.invalid.app_error","message":"` + chattest.Canary + `"}`),
			},
			{Status: http.StatusOK, Body: []byte(chattest.Canary)},
			{Status: http.StatusOK, Body: nil},
			{Status: http.StatusOK, Body: []byte("okay " + chattest.Canary)},
			{Status: http.StatusNoContent, Body: []byte(chattest.Canary)},
		},
	}
}

func TestRendererConformance(t *testing.T) {
	t.Parallel()
	chattest.RunRenderer(t, harness)
}

func TestCheckHostAcceptsAHostOfTheOperator(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"chat.example.com", "chat.example.com:8065", "8.8.8.8", "a.b.c.example.org"} {
		if err := mattermost.New().CheckHost(host); err != nil {
			t.Errorf("CheckHost(%q) = %v, want nil", host, err)
		}
	}
}

func TestCheckHostRuleNeverHoldsTheHost(t *testing.T) {
	t.Parallel()

	err := mattermost.New().CheckHost("user:secret-9f3b@chat.example.com")
	if err == nil || !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("CheckHost = %v, want ErrInvalid", err)
	}

	if strings.Contains(err.Error(), "secret-9f3b") {
		t.Errorf("the error text repeats the host: %v", err)
	}
}

func content(mutate func(c *chat.Content)) chat.Content {
	c := chat.Content{
		NotificationID: "ntf-1", Product: "OpenDustpan", Title: "Job failed", Summary: "The apply failed.",
		Severity: notify.SeverityCritical,
		Fields:   []chat.Field{{Label: "Namespace", Value: "shop"}},
		Links:    []notify.Link{{Title: "Job", URL: "https://console.example/jobs/1", Kind: notify.LinkConsole}},
	}
	mutate(&c)

	return c
}

func render(t *testing.T, c chat.Content) (chat.Request, string) {
	t.Helper()

	req, err := mattermost.New().Render(c)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	texts, err := decode(req.Body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	return req, texts[0]
}

func TestRenderEncodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"a plain name", "network", "network"},
		{"a mention", "@channel", "@" + zw + "channel"},
		{"a channel link", "~town-square", `\~` + zw + `town\-square`},
		{"a hashtag", "#tag", `\#` + zw + "tag"},
		{"an emoji", ":smile:", ":" + zw + "smile:" + zw},
		{"a scheme", "https://x", "https:" + zw + "//x"},
		{"bold", "**bold**", `\*\*bold\*\*`},
		{"a link", "[a](b)", `\[a\]\(b\)`},
		{"code", "`code`", "\\`code\\`"},
		{"a backslash", `a\b`, `a\\b`},
		{"HTML", "<b>&", "&lt;b&gt;&amp;"},
		{"an entity that the person typed", "&lt;", "&amp;lt;"},
		{"a table", "|a|", `\|a\|`},
		{"a bare www host", "www.x", `www\.` + zw + "x"},
		{"a dot in a word", "v1.2", `v1\.2`},
		{"a quote", "> q", "&gt; q"},
		{"a list", "- a\n1. b", `\- a` + "\n" + `1\. b`},
		{"an indented line", "a\n    b", "a\n\u00a0\u00a0\u00a0\u00a0b"},
		{"a name in another script", "名前 ñ", "名前 ñ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, text := render(
				t,
				content(func(c *chat.Content) { c.Fields = []chat.Field{{Label: "Name", Value: tt.value}} }),
			)

			_, got, ok := strings.Cut(text, "**Name**: ")
			if !ok {
				t.Fatalf("no field in %q", text)
			}

			got, _, _ = strings.Cut(got, "\n\n")
			if got != tt.want {
				t.Errorf("value %q rendered as %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRenderNoHTMLInBytes(t *testing.T) {
	t.Parallel()

	// The body holds no < and no >, also not as an escape that the decoder of
	// JSON turns back into one, and no escape of & from the encoder of JSON.
	req, _ := render(t, content(func(c *chat.Content) {
		c.Title = "<!channel> <@U1>"
		c.Summary = "<https://evil.example|x> & more"
		c.Links[0].URL = "https://console.example/x|<b>"
	}))

	for _, bad := range []string{"<", ">", "\\u003c", "\\u003e", "\\u0026"} {
		if strings.Contains(string(req.Body), bad) {
			t.Errorf("the body holds %q", bad)
		}
	}
}

func TestRenderURLOnlyInLinkTarget(t *testing.T) {
	t.Parallel()

	_, text := render(t, content(func(c *chat.Content) {
		c.Links[0].URL = "https://console.example/a b)(x)|<y>[z]\\`é"
		c.Links[0].Title = "https://console.example/jobs/1](https://evil.example"
	}))

	want := "](https://console.example/a%20b%29%28x%29%7C%3Cy%3E%5Bz%5D%5C%60%C3%A9)"
	if !strings.Contains(text, want) {
		t.Errorf("text %q does not hold the encoded target %q", text, want)
	}

	if strings.Count(text, "](") != 1 {
		t.Errorf("text %q: the title of the link opens a second target", text)
	}
}

func TestRenderTitleStaysOnItsLine(t *testing.T) {
	t.Parallel()

	_, text := render(t, content(func(c *chat.Content) {
		c.Title = "a\n# heading"
		c.Product = "p\n- item"
		c.Fields = []chat.Field{{Label: "l\n> quote", Value: ""}}
		c.Links[0].Title = "t\n[x](y)"
	}))

	for _, bad := range []string{"\n#", "\n- ", "\n>", "\n["} {
		if strings.Contains(text, bad) && !strings.Contains(text, "\n\n"+bad[1:]) {
			t.Errorf("text %q holds a line that starts with %q", text, bad)
		}
	}

	for l := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "> ") || strings.HasPrefix(l, "[") {
			t.Errorf("a line starts with markup: %q", l)
		}
	}
}

func TestRenderLimits(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]chat.Content{
		"a value of entities": content(func(c *chat.Content) {
			c.Fields[0].Value = strings.Repeat("<", chat.MaxFieldLen)
		}),
		"a value of escapes": content(func(c *chat.Content) {
			c.Fields[0].Value = strings.Repeat(`*_[`, chat.MaxFieldLen/3)
		}),
		"a summary of entities": content(func(c *chat.Content) {
			c.Summary = strings.Repeat("<", notify.MaxSummaryLen)
		}),
		"a summary of mentions": content(func(c *chat.Content) {
			c.Summary = strings.Repeat("@", notify.MaxSummaryLen)
		}),
	} {
		_, text := render(t, c)

		if !utf8.ValidString(text) || strings.Contains(text, "�") {
			t.Errorf("%s: the text is cut inside a character", name)
		}

		// A cut inside an escape leaves a backslash at the end of a part.
		for part := range strings.SplitSeq(text, "\n") {
			if strings.HasSuffix(strings.TrimSuffix(part, "…"), `\`) && !strings.HasSuffix(part, `\\`) {
				t.Errorf("%s: a text is cut after a backslash: %q", name, part)
			}

			if i := strings.LastIndexByte(part, '&'); i >= 0 && !strings.Contains(part[i:], ";") {
				t.Errorf("%s: a text is cut inside an entity: %q", name, part)
			}
		}
	}
}

func TestRenderBodyLimitDropsLinksAndSaysSo(t *testing.T) {
	t.Parallel()

	long := "https://console.example/" + strings.Repeat("u", notify.MaxLinkURLLen-len("https://console.example/"))
	links := make([]notify.Link, 0, notify.MaxLinks)

	for range notify.MaxLinks {
		links = append(links, notify.Link{Title: "Job", URL: long, Kind: notify.LinkConsole})
	}

	req, text := render(t, content(func(c *chat.Content) { c.Links = links }))

	// Mattermost takes a post of 16383 characters; the renderer keeps to 15000.
	if len(text) > 15000 || len(req.Body) > chat.MaxRequestBytes {
		t.Errorf("text %d bytes, body %d bytes; want at most 15000 and %d", len(text), len(req.Body),
			chat.MaxRequestBytes)
	}

	shown := strings.Count(text, ":link:")
	if shown == 0 || shown == notify.MaxLinks {
		t.Fatalf("shown %d links, want some but not all", shown)
	}

	want := "more links not shown"
	if !strings.Contains(text, want) {
		t.Error("text does not say that links are left out")
	}
}

func TestRenderSeverityAndEmptyParts(t *testing.T) {
	t.Parallel()

	_, text := render(t, content(func(c *chat.Content) {
		c.Summary, c.Fields, c.Links = "", nil, nil
		c.Severity = notify.SeverityInfo
	}))

	for _, want := range []string{"Info", "OpenDustpan", "Job failed"} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q does not hold %q", text, want)
		}
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"ok", 200, "ok", nil},
		{"ok with a line feed", 200, "ok\n", nil},
		{"a JSON error", 200, `{"message":"` + chattest.Canary + `"}`, notify.ErrPermanent},
		{"empty", 200, "", notify.ErrPermanent},
		{"ok in another case", 200, "OK", notify.ErrPermanent},
		{"ok with a 4xx status", 400, "ok", notify.ErrPermanent},
		{"ok with a 3xx status", 302, "ok", notify.ErrPermanent},
	}

	for _, tt := range tests {
		err := mattermost.New().Check(tt.status, []byte(tt.body))
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Check = %v, want %v", tt.name, err, tt.want)
		}

		if err != nil && strings.Contains(err.Error(), chattest.Canary) {
			t.Errorf("%s: the error text repeats the body", tt.name)
		}
	}
}
