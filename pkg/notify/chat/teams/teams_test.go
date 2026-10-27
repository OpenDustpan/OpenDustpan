package teams_test

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
	"opendustpan.org/opendustpan/pkg/notify/chat/teams"
)

const zw = "\u200b"

// The wire types are the parts of a Teams body that the tests read.
type wireFact struct {
	Title string `json:"title"`
	Value string `json:"value"`
}

type wireElement struct {
	Type  string     `json:"type"`
	Text  string     `json:"text"`
	Facts []wireFact `json:"facts"`
}

type wireAction struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type wireCard struct {
	Schema  string         `json:"$schema"`
	Type    string         `json:"type"`
	Version string         `json:"version"`
	Body    []wireElement  `json:"body"`
	Actions []wireAction   `json:"actions"`
	MSTeams map[string]any `json:"msteams"`
}

type wireAttachment struct {
	ContentType string   `json:"contentType"`
	Content     wireCard `json:"content"`
}

type wireMessage struct {
	Type        string           `json:"type"`
	Attachments []wireAttachment `json:"attachments"`
}

func parse(body []byte) (wireMessage, error) {
	var msg wireMessage

	err := json.Unmarshal(body, &msg)

	return msg, err //nolint:wrapcheck // A test helper.
}

// decode returns the texts that Teams parses as Markdown: the text blocks,
// the facts and the titles of the actions. The URL of an action is not one.
func decode(body []byte) ([]string, error) {
	msg, err := parse(body)
	if err != nil {
		return nil, err
	}

	var out []string

	for i := range msg.Attachments {
		a := &msg.Attachments[i]

		for _, e := range a.Content.Body {
			if e.Text != "" {
				out = append(out, e.Text)
			}

			for _, f := range e.Facts {
				out = append(out, f.Title, f.Value)
			}
		}

		for _, act := range a.Content.Actions {
			out = append(out, act.Title)
		}
	}

	return out, nil
}

func harness(t *testing.T) chattest.Harness {
	t.Helper()

	return chattest.Harness{
		Renderer:     teams.New(),
		Platform:     chat.Teams,
		Host:         "prod-12.westus.logic.azure.com",
		SingleHost:   false,
		OperatorHost: false,
		MaxBody:      teams.MaxBody,
		Decode:       decode,
		RefusedHosts: []string{
			"prod-12.westus.logic.azure.com:8443", "prod-12.westus.logic.azure.com:80",
			"prod-12.westus.logic.azure.com:", "PROD-12.westus.logic.azure.com", "logic.azure.com", ".logic.azure.com",
			"xlogic.azure.com", "logic.azure.com.evil.example", "prod.logic.azure.co", "api.powerplatform.com",
			"evil.example", "hooks.slack.com", "outlook.office.com", "prod.webhook.office.com", "127.0.0.1",
			"prod.logic.azure.com@evil.example", "evil.example/.logic.azure.com", "evil.example#.logic.azure.com",
		},
		Markup: []string{"<at>", "**", "__", "](", "://", "@everyone", "@channel"},
		Hostile: []string{
			"<at>John Doe</at>", "<at>Everyone</at>", "@everyone", "@channel", "@here", "[click](https://evil.example)",
			"![x](https://evil.example/p.png)", "**bold**", "__bold__", "_it_", "https://evil.example",
			"www.evil.example", "ops@evil.example", "<b>x</b>", "<script>alert(1)</script>", "[x]", "<<at>x</at>>",
		},
		Accepted: chattest.Answer{Status: http.StatusAccepted, Body: nil},
		Refusals: []chattest.Answer{
			{Status: http.StatusCreated, Body: []byte(chattest.Canary)},
			{Status: http.StatusNonAuthoritativeInfo, Body: []byte(chattest.Canary)},
			{Status: http.StatusNoContent, Body: []byte(chattest.Canary)},
			{Status: http.StatusPartialContent, Body: nil},
		},
	}
}

func TestRendererConformance(t *testing.T) {
	t.Parallel()
	chattest.RunRenderer(t, harness)
}

func TestCheckHostAcceptsTheDocumentedHosts(t *testing.T) {
	t.Parallel()

	for _, host := range []string{
		"prod-12.westus.logic.azure.com",
		"prod-12.westus.logic.azure.com:443",
		"abc123.12.environment.api.powerplatform.com",
		"abc123.12.environment.api.powerplatform.com:443",
		"x.logic.azure.us", "x.api.gov.powerplatform.microsoft.us", "x.api.high.powerplatform.microsoft.us",
		"x.api.appsplatform.us", "x.logic.azure.cn", "x.api.powerplatform.partner.microsoftonline.cn",
	} {
		if err := teams.New().CheckHost(host); err != nil {
			t.Errorf("CheckHost(%q) = %v, want nil", host, err)
		}
	}
}

func TestCheckHostRuleNeverHoldsTheHost(t *testing.T) {
	t.Parallel()

	err := teams.New().CheckHost("secret-9f3b.evil.example")
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

func render(t *testing.T, c chat.Content) (chat.Request, wireMessage) {
	t.Helper()

	req, err := teams.New().Render(c)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	msg, err := parse(req.Body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	return req, msg
}

func TestRenderIsAnAdaptiveCardMessage(t *testing.T) {
	t.Parallel()

	req, msg := render(t, content(func(*chat.Content) {}))

	if req.Method != http.MethodPost || req.ContentType != "application/json" || req.PathSuffix != "" {
		t.Errorf("request = %s %q %q", req.Method, req.ContentType, req.PathSuffix)
	}

	if msg.Type != "message" || len(msg.Attachments) != 1 {
		t.Fatalf("message = %+v, want type message and one attachment", msg)
	}

	checkAttachment(t, msg.Attachments[0], req.Body)
}

func checkAttachment(t *testing.T, a wireAttachment, body []byte) {
	t.Helper()

	if a.ContentType != "application/vnd.microsoft.card.adaptive" ||
		!strings.Contains(string(body), `"contentUrl":null`) ||
		a.Content.Type != "AdaptiveCard" || a.Content.Schema != "https://adaptivecards.io/schemas/adaptive-card.json" ||
		a.Content.Version != "1.2" {
		t.Errorf("attachment = %+v, want the documented Adaptive Card attachment", a)
	}

	// The card has no mention entity, so no text can mention a person.
	if a.Content.MSTeams != nil || strings.Contains(string(body), "entities") {
		t.Error("the card holds a msteams object or an entity")
	}
}

func TestRenderEncodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"a plain name", "network", "network"},
		{"a mention tag", "<at>Everyone</at>", "<" + zw + "at>Everyone<" + zw + "/at>"},
		{"a mention", "@all", "@" + zw + "all"},
		{"bold", "**bold**", zw + "*" + zw + zw + "*" + zw + "bold" + zw + "*" + zw + zw + "*" + zw},
		{"italic at the edge of a word", "_it_", zw + "_" + zw + "it" + zw + "_" + zw},
		{"an underscore in a word stays", "my_stack_name", "my_stack_name"},
		{"a link", "[a](b)", "[" + zw + "a]" + zw + "(b)"},
		{"a scheme", "https://x", "https:" + zw + "//x"},
		{"a bare www host", "www.x", "www." + zw + "x"},
		{"a dot in a word", "v1.2", "v1.2"},
		{"a list item", "- a", zw + "- a"},
		{"a numbered item", "1. a", zw + "1. a"},
		{"a heading", "# a", zw + "# a"},
		{"a quote", "> a", zw + "> a"},
		{"a line feed", "a\nb", "a\n\nb"},
		{"a list item after a line feed", "a\n- b", "a\n\n" + zw + "- b"},
		{"an indented list item", "  - a", "  " + zw + "- a"},
		{"an indented numbered item", "   1. a", "   " + zw + "1. a"},
		{"an indented numbered item after a line feed", "a\n  2. b", "a\n\n  " + zw + "2. b"},
		{"a digit inside a line", "a 1. b", "a 1. b"},
		{"a name in another script", "名前 ñ", "名前 ñ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, msg := render(
				t,
				content(func(c *chat.Content) { c.Fields = []chat.Field{{Label: "Name", Value: tt.value}} }),
			)

			var got string

			for _, e := range msg.Attachments[0].Content.Body {
				for _, f := range e.Facts {
					got = f.Value
				}
			}

			if got != tt.want {
				t.Errorf("value %q rendered as %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRenderURLOnlyInActionURL(t *testing.T) {
	t.Parallel()

	req, msg := render(t, content(func(c *chat.Content) {
		c.Links[0].URL = "https://console.example/x)|[y](https://evil.example)"
		c.Links[0].Title = "[Open](https://evil.example)"
	}))

	acts := msg.Attachments[0].Content.Actions
	if len(acts) != 1 || acts[0].Type != "Action.OpenUrl" ||
		acts[0].URL != "https://console.example/x)|[y](https://evil.example)" {
		t.Fatalf("actions = %+v, want one Action.OpenUrl with the URL in url", acts)
	}

	texts, err := decode(req.Body)
	if err != nil {
		t.Fatal(err)
	}

	for _, text := range texts {
		if strings.Contains(text, "](") {
			t.Errorf("a text holds link markup: %q", text)
		}
	}
}

func TestRenderLimits(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]chat.Content{
		"a value of mentions": content(func(c *chat.Content) {
			c.Fields[0].Value = strings.Repeat("@", chat.MaxFieldLen)
		}),
		"a value of bold marks": content(func(c *chat.Content) {
			c.Fields[0].Value = strings.Repeat("*", chat.MaxFieldLen)
		}),
		"a summary of angle brackets": content(func(c *chat.Content) {
			c.Summary = strings.Repeat("<", notify.MaxSummaryLen)
		}),
		"a summary of line feeds": content(func(c *chat.Content) {
			c.Summary = strings.Repeat("\n", notify.MaxSummaryLen)
		}),
	} {
		req, _ := render(t, c)

		texts, err := decode(req.Body)
		if err != nil {
			t.Fatal(err)
		}

		for _, text := range texts {
			if !utf8.ValidString(text) || strings.Contains(text, "�") {
				t.Errorf("%s: a text is cut inside a character", name)
			}
		}

		if len(req.Body) > teams.MaxBody {
			t.Errorf("%s: body of %d bytes, want at most %d", name, len(req.Body), teams.MaxBody)
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

	// A quote takes two bytes in JSON, so the fixed parts are as large as they get.
	quotes := func(n int) string { return strings.Repeat(`"`, n) }

	req, msg := render(t, content(func(c *chat.Content) {
		c.Links = links
		c.Summary = quotes(notify.MaxSummaryLen)
		c.Fields = nil

		for range chat.MaxFields {
			c.Fields = append(c.Fields, chat.Field{Label: quotes(chat.MaxFieldLen), Value: quotes(chat.MaxFieldLen)})
		}
	}))

	if len(req.Body) > teams.MaxBody {
		t.Errorf("body of %d bytes, want at most %d", len(req.Body), teams.MaxBody)
	}

	shown := len(msg.Attachments[0].Content.Actions)
	if shown == 0 || shown == notify.MaxLinks {
		t.Fatalf("shown %d links, want some but not all", shown)
	}

	texts, err := decode(req.Body)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(strings.Join(texts, "\n"), "more links not shown") {
		t.Error("the card does not say that links are left out")
	}
}

func TestRenderSeverityAndEmptyParts(t *testing.T) {
	t.Parallel()

	req, _ := render(t, content(func(c *chat.Content) {
		c.Summary, c.Fields, c.Links = "", nil, nil
		c.Severity = notify.SeverityInfo
	}))

	texts, err := decode(req.Body)
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(texts, "\n")
	for _, want := range []string{"Info", "OpenDustpan", "Job failed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("texts %q do not hold %q", joined, want)
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
		{"accepted", 202, "", nil},
		{"accepted with a body", 202, `{"ok":true}`, nil},
		{"a response action", 200, "", nil},
		{"created", 201, chattest.Canary, notify.ErrPermanent},
		{"no content", 204, chattest.Canary, notify.ErrPermanent},
		{"partial content", 206, "", notify.ErrPermanent},
		{"a client error", 400, "", notify.ErrPermanent},
		{"a redirect", 302, "", notify.ErrPermanent},
	}

	for _, tt := range tests {
		err := teams.New().Check(tt.status, []byte(tt.body))
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Check = %v, want %v", tt.name, err, tt.want)
		}

		if err != nil && strings.Contains(err.Error(), chattest.Canary) {
			t.Errorf("%s: the error text repeats the body", tt.name)
		}
	}
}

// factValue renders s as the value of a field and returns the value as it is in
// the card.
func factValue(t *testing.T, s string) string {
	t.Helper()

	_, msg := render(t, content(func(c *chat.Content) { c.Fields = []chat.Field{{Label: "Name", Value: s}} }))

	for _, e := range msg.Attachments[0].Content.Body {
		for _, f := range e.Facts {
			return f.Value
		}
	}

	return ""
}

// TestRenderKeepsLinksAndMentionsBroken records what the zero-width spaces
// stop. Microsoft documents no escape character for the Markdown of a text
// block, so the renderer breaks tokens by position. A link, an image, a
// mention and an automatic link stay broken: the characters that join them are
// apart.
func TestRenderKeepsLinksAndMentionsBroken(t *testing.T) {
	t.Parallel()

	inline := []struct{ name, in, token string }{
		{"a link", "[t](https://evil.example)", "]("},
		{"an image", "![t](https://evil.example/p.png)", "]("},
		{"a reference link", "[t]: https://evil.example", "]:"},
		{"a scheme", "https://evil.example", "://"},
		{"a www link", "www.evil.example", "www.e"},
		{"a mention tag", "<at>Everyone</at>", "<at>"},
		{"a mention", "@everyone", "@e"},
	}

	for _, tt := range inline {
		if got := factValue(t, tt.in); strings.Contains(got, tt.token) {
			t.Errorf("%s: %q still holds %q, which joins the markup", tt.name, got, tt.token)
		}
	}
}

// TestRenderKeepsListsBroken checks that no line of a rendered value starts
// with a list or heading marker, also after spaces.
func TestRenderKeepsListsBroken(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"- a", " - a", "   + a", "* a", "1. a", "  12. a", "# a", "> a", "x\n  - a\n2. b"} {
		for l := range strings.SplitSeq(factValue(t, in), "\n") {
			first := strings.TrimLeft(l, " ")
			if first != "" && strings.ContainsRune("-+#>0123456789", rune(first[0])) {
				t.Errorf("value %q: the line %q starts with a list or heading marker", in, l)
			}
		}
	}
}

// TestRenderLeavesEmphasisPossible records the limit of the zero-width
// spaces: the marks of bold and italic stay in the text, with the spaces beside
// them, and a Markdown parser can still make the words bold or italic. That is
// formatting only, never a link or a mention. The renderer keeps the visible
// characters. A reviewer with a Teams flow checks what Teams does.
func TestRenderLeavesEmphasisPossible(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"*x*", "_x_", "**x**", "__x__"} {
		got := factValue(t, in)

		if strings.ReplaceAll(got, zw, "") != in {
			t.Errorf("value %q rendered as %q, want the same visible characters", in, got)
		}

		if !strings.Contains(got, zw) {
			t.Errorf("value %q rendered as %q, want zero-width spaces beside the marks", in, got)
		}
	}
}

func TestMaxBodyIsUnderTheLimitOfTeams(t *testing.T) {
	t.Parallel()

	// Microsoft names 28 KB and does not say how it counts the size.
	if teams.MaxBody >= 28000 {
		t.Errorf("MaxBody = %d, want less than 28,000 bytes", teams.MaxBody)
	}
}
