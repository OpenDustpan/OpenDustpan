package slack_test

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
	"opendustpan.org/opendustpan/pkg/notify/chat/slack"
)

// wireText, wireButton, wireBlock and wireMessage are the parts of a Slack
// body that the tests read.
type wireText struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Verbatim bool   `json:"verbatim"`
}

type wireButton struct {
	Type string   `json:"type"`
	URL  string   `json:"url"`
	Text wireText `json:"text"`
}

type wireBlock struct {
	Text      *wireText   `json:"text"`
	Fields    []wireText  `json:"fields"`
	Elements  []wireText  `json:"elements"`
	Accessory *wireButton `json:"accessory"`
}

type wireMessage struct {
	Text   string      `json:"text"`
	Blocks []wireBlock `json:"blocks"`
}

// decode returns the mrkdwn texts of a Slack body, after the decoding of the
// JSON string escapes: the fallback text, the text of each block and each
// field. The label of a button and the URL are not markup.
func decode(body []byte) ([]string, error) {
	var msg wireMessage

	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err //nolint:wrapcheck // A test helper.
	}

	out := []string{msg.Text}

	for _, b := range msg.Blocks {
		if b.Text != nil {
			out = append(out, b.Text.Text)
		}

		for _, f := range b.Fields {
			out = append(out, f.Text)
		}

		for _, e := range b.Elements {
			out = append(out, e.Text)
		}
	}

	return out, nil
}

func harness(t *testing.T) chattest.Harness {
	t.Helper()

	return chattest.Harness{
		Renderer:     slack.New(),
		Platform:     chat.Slack,
		Host:         slack.HostPublic,
		SingleHost:   true,
		OperatorHost: false,
		MaxBody:      0,
		Decode:       decode,
		RefusedHosts: []string{
			"hooks.slack.com.evil.example",
			"slack.com",
			"hooks.slack-gov.com:8443",
			"HOOKS.SLACK.COM",
			"hooks.slack.com.",
		},
		Markup: []string{"<", ">"},
		Hostile: []string{
			"<!channel>", "<!here>", "<!everyone>", "<@U123ABC>", "<@U123ABC|admin>", "<!subteam^S123|@ops>",
			"<#C123|general>", "<https://evil.example|click here>", "<@U123>", "<https://evil|x>",
			"a<b>c", "x&lt;!channel&gt;", "<<!channel>>",
		},
		Accepted: chattest.Answer{Status: http.StatusOK, Body: []byte("ok")},
		Refusals: []chattest.Answer{
			{Status: http.StatusOK, Body: []byte("invalid_token " + chattest.Canary)},
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

// TestCheckHostAcceptsPublicAndGovHosts checks that CheckHost accepts the
// public Slack host and the Slack host for government use.
func TestCheckHostAcceptsPublicAndGovHosts(t *testing.T) {
	t.Parallel()

	for _, host := range []string{slack.HostPublic, slack.HostGov} {
		if err := slack.New().CheckHost(host); err != nil {
			t.Errorf("CheckHost(%q) = %v, want nil", host, err)
		}
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

func render(t *testing.T, c chat.Content) (string, []string) {
	t.Helper()

	req, err := slack.New().Render(c)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	texts, err := decode(req.Body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	return string(req.Body), texts
}

func TestRenderEncodes(t *testing.T) {
	t.Parallel()

	const zw = "\u200b"

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"a plain name", "network", "network"},
		{"a mention of the channel", "<!channel>", "&lt;!channel&gt;"},
		{"an ampersand", "a&b", "a&amp;b"},
		{"an entity that the person typed", "&lt;", "&amp;lt;"},
		{"bold", "*bold*", "*" + zw + "bold*" + zw},
		{"strike through", "~gone~", "~" + zw + "gone~" + zw},
		{"code", "`code`", "`" + zw + "code`" + zw},
		{"italic at the edge of a word", "_it_", "_" + zw + "it_" + zw},
		{"an underscore in a word stays", "my_stack_name", "my_stack_name"},
		{"a quote at the start of a line", "> quote", "&gt; quote"},
		{"a name in another script", "名前 ñ", "名前 ñ"},
		{"a line feed", "a\nb", "a\nb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, texts := render(
				t,
				content(func(c *chat.Content) { c.Fields = []chat.Field{{Label: "Name", Value: tt.value}} }),
			)

			var got string

			for _, text := range texts {
				if rest, ok := strings.CutPrefix(text, "*Name*\n"); ok {
					got = rest
				}
			}

			if got != tt.want {
				t.Errorf("value %q rendered as %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRenderNoMarkupInBytes(t *testing.T) {
	t.Parallel()

	// The body holds no < and no >, also not as an escape that the decoder of
	// JSON turns back into one, and no &-escape of the encoder of JSON.
	body, _ := render(t, content(func(c *chat.Content) {
		c.Title = "<!channel> <@U1>"
		c.Summary = "<https://evil.example|x> & more"
	}))

	for _, bad := range []string{"<", ">", "\\u003c", "\\u003e", "\\u0026"} {
		if strings.Contains(body, bad) {
			t.Errorf("the body holds %q", bad)
		}
	}
}

func TestRenderURLOnlyInButton(t *testing.T) {
	t.Parallel()

	body, texts := render(t, content(func(c *chat.Content) {
		c.Links[0].URL = "https://console.example/x|<!channel>"
		c.Links[0].Title = "https://console.example/jobs/1"
	}))

	for _, text := range texts {
		if strings.Contains(text, "|") && strings.Contains(text, "console.example/x") {
			t.Errorf("a text holds the URL: %q", text)
		}
	}

	var msg wireMessage

	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatal(err)
	}

	var buttons int

	for _, b := range msg.Blocks {
		if b.Accessory == nil {
			continue
		}

		buttons++

		if b.Accessory.URL != "https://console.example/x|<!channel>" || b.Accessory.Text.Type != "plain_text" ||
			b.Accessory.Text.Text != "Open" {
			t.Errorf("button = %+v, want the URL in url and the fixed label", b.Accessory)
		}
	}

	if buttons != 1 {
		t.Errorf("buttons = %d, want 1", buttons)
	}
}

func TestRenderLimits(t *testing.T) {
	t.Parallel()

	// Each text is cut on a character boundary inside the limit of its block,
	// and the cut never splits an entity.
	long := strings.Repeat("<", 400) // 1600 bytes as entities for one field value
	big := strings.Repeat("é<&", chat.MaxFieldLen)

	for name, c := range map[string]chat.Content{
		"a value of entities": content(func(c *chat.Content) { c.Fields[0].Value = long[:chat.MaxFieldLen] }),
		"a value of mixed characters": content(func(c *chat.Content) {
			c.Fields[0].Value = big[:chat.MaxFieldLen-1]
		}),
		"a summary of entities": content(func(c *chat.Content) {
			c.Summary = strings.Repeat("<", notify.MaxSummaryLen)
		}),
	} {
		_, texts := render(t, c)

		for _, text := range texts {
			if !utf8.ValidString(text) || strings.Contains(text, "\ufffd") {
				t.Errorf("%s: a text is cut inside a character", name)
			}

			// A text that ends in an entity that is cut leaves & without ;.
			for i := strings.LastIndexByte(text, '&'); i >= 0; {
				if !strings.Contains(text[i:], ";") {
					t.Errorf("%s: a text is cut inside an entity: %q", name, text[max(0, len(text)-20):])
				}

				break
			}
		}
	}
}

func TestRenderSeverityAndEmptyParts(t *testing.T) {
	t.Parallel()

	_, texts := render(t, content(func(c *chat.Content) {
		c.Summary, c.Fields, c.Links = "", nil, nil
		c.Severity = notify.SeverityInfo
	}))

	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "Info") || !strings.Contains(joined, "OpenDustpan") ||
		!strings.Contains(joined, "Job failed") {
		t.Errorf("texts = %q, want the severity, the product and the title", joined)
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
		{"another word", 200, "no_service", notify.ErrPermanent},
		{"empty", 200, "", notify.ErrPermanent},
		{"a 204 with ok", 204, "ok", nil},
	}

	for _, tt := range tests {
		err := slack.New().Check(tt.status, []byte(tt.body))
		if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
			t.Errorf("%s: Check = %v, want %v", tt.name, err, tt.want)
		}
	}
}

// plainHostile returns strings that Slack acts on in a plain text: they have
// no markup that the renderer can encode, so only verbatim stops them. The
// fallback text has no verbatim, so it breaks their separators instead.
func plainHostile() []string {
	return []string{
		"@here", "@channel", "#general", "https://evil.example", "evil.example",
		"<!channel>", "<@U123>", "<https://evil|x>", "@everyone", "ops@evil.example",
		"example。com", "example．com", "example｡com", "example＠com", "example＃com",
		"user。example.com", "user．example.com", "user｡example.com", "user＠example.com",
		"user＃example.com",
		"。general", "．general", "｡general", "＠here", "＃general",
	}
}

// fallbackSeparators are the characters that the fallback text must break:
// @, #, a dot and a colon, their ideographic, fullwidth, halfwidth and small
// forms, the one dot leader and the fullwidth solidus.
const fallbackSeparators = "@#.:\u3002\uFF0E\uFF61\uFF20\uFF03\uFE52\u2024\uFE6B\uFE5F\uFF1A\uFF0F"

func TestRenderShowsHostileAsPlainText(t *testing.T) {
	t.Parallel()

	for _, hostile := range plainHostile() {
		t.Run(hostile, func(t *testing.T) {
			t.Parallel()

			req, err := slack.New().Render(content(func(c *chat.Content) {
				c.Product, c.Title, c.Summary = hostile, hostile, hostile
				c.Fields = []chat.Field{{Label: hostile, Value: hostile}}
				c.Links[0].Title = hostile
			}))
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			var msg wireMessage
			if err := json.Unmarshal(req.Body, &msg); err != nil {
				t.Fatal(err)
			}

			checkVerbatim(t, msg)

			// The text of a block shows the string, entity-encoded, and with
			// no link or mention markup.
			want := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(hostile)
			if got := msg.Blocks[2].Text.Text; got != want {
				t.Errorf("summary = %q, want the plain text %q", got, want)
			}

			checkFallback(t, msg.Text, hostile, want)
		})
	}
}

// TestFallbackBreaksEachSeparator checks each ideographic, fullwidth,
// halfwidth and small separator alone, so a change that drops one rune names
// it.
func TestFallbackBreaksEachSeparator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sep  rune
	}{
		{"ideographic full stop", '。'},
		{"fullwidth full stop", '．'},
		{"halfwidth ideographic full stop", '｡'},
		{"fullwidth commercial at", '＠'},
		{"fullwidth number sign", '＃'},
		{"small full stop", '﹒'},
		{"one dot leader", '․'},
		{"small commercial at", '﹫'},
		{"small number sign", '﹟'},
		{"fullwidth colon", '：'},
		{"fullwidth solidus", '／'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hostile := "x" + string(tt.sep) + "y"

			req, err := slack.New().Render(content(func(c *chat.Content) {
				c.Product, c.Title, c.Summary = hostile, hostile, hostile
			}))
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			var msg wireMessage
			if err := json.Unmarshal(req.Body, &msg); err != nil {
				t.Fatal(err)
			}

			want := "x" + string(tt.sep) + "\u200by"
			if !strings.Contains(msg.Text, want) {
				t.Errorf("fallback %q: %U is not followed by a zero-width space", msg.Text, tt.sep)
			}
		})
	}
}

// checkVerbatim checks that every mrkdwn object of msg tells Slack to show
// the text as it is, and that the label of a button is plain text.
func checkVerbatim(t *testing.T, msg wireMessage) {
	t.Helper()

	var objects []*wireText

	for _, b := range msg.Blocks {
		objects = append(objects, b.Text)

		for _, group := range [][]wireText{b.Fields, b.Elements} {
			for i := range group {
				objects = append(objects, &group[i])
			}
		}

		if b.Accessory != nil {
			checkButtonLabel(t, b.Accessory.Text)
		}
	}

	var mrkdwn int

	for _, o := range objects {
		if o == nil {
			continue
		}

		mrkdwn++

		if o.Type != "mrkdwn" || !o.Verbatim {
			t.Errorf("text object %+v, want a mrkdwn object with verbatim true", *o)
		}
	}

	if mrkdwn == 0 {
		t.Fatal("the body has no mrkdwn object")
	}
}

// checkButtonLabel checks that the label is plain text with no verbatim, which
// Slack accepts on mrkdwn only.
func checkButtonLabel(t *testing.T, label wireText) {
	t.Helper()

	if label.Type != "plain_text" || label.Verbatim {
		t.Errorf("button label = %+v, want plain_text with no verbatim", label)
	}
}

// checkFallback checks the top-level text: it cannot set verbatim, so no
// automatic link or mention may start in it, and it still shows the string.
func checkFallback(t *testing.T, fallback, hostile, want string) {
	t.Helper()

	if strings.ContainsAny(fallback, "<>") {
		t.Errorf("fallback %q holds link or mention markup", fallback)
	}

	for _, trigger := range []string{"@here", "@channel", "@everyone", "@U123", "#general", "://", "evil.example", hostile} {
		if strings.Contains(fallback, trigger) {
			t.Errorf("fallback %q holds the trigger %q", fallback, trigger)
		}
	}

	rs := []rune(fallback)
	for i, r := range rs {
		if !strings.ContainsRune(fallbackSeparators, r) {
			continue
		}

		if i+1 >= len(rs) || rs[i+1] != '\u200b' {
			t.Errorf("fallback %q: %q is not followed by a zero-width space", fallback, r)
		}
	}

	if shown := strings.ReplaceAll(fallback, "\u200b", ""); !strings.Contains(shown, want) {
		t.Errorf("fallback %q does not show %q as text", shown, want)
	}
}
