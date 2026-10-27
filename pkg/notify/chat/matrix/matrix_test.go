package matrix_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/notify/chat/chattest"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
	"opendustpan.org/opendustpan/pkg/notify/chat/matrix"
)

const zw = "\u200b"

// event is the content of a Matrix message, as the tests read it.
type event struct {
	Msgtype       string
	Body          string
	Format        string
	FormattedBody string
	Mentions      map[string]any
}

// parseEvent reads the fields of an event. The names of the fields are those
// of the Matrix specification, which a struct tag cannot write in the style of
// the linter.
func parseEvent(raw []byte) (event, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return event{}, fmt.Errorf("parse an event: %w", err)
	}

	var ev event

	for key, dst := range map[string]any{
		"msgtype": &ev.Msgtype, "body": &ev.Body, "format": &ev.Format, "formatted_body": &ev.FormattedBody,
		"m.mentions": &ev.Mentions,
	} {
		if v, ok := fields[key]; ok {
			if err := json.Unmarshal(v, dst); err != nil {
				return event{}, fmt.Errorf("parse an event: %w", err)
			}
		}
	}

	return ev, nil
}

var tag = regexp.MustCompile(`<[^>]*>`)

// decode returns the text that a client parses as markup: the formatted body
// without its tags and with its entities as they are. The plain body is text
// for a client that shows no HTML, and the tests below read it on its own.
func decode(body []byte) ([]string, error) {
	ev, err := parseEvent(body)
	if err != nil {
		return nil, fmt.Errorf("decode the body: %w", err)
	}

	return []string{tag.ReplaceAllString(ev.FormattedBody, " ")}, nil
}

func harness(t *testing.T) chattest.Harness {
	t.Helper()

	return chattest.Harness{
		Renderer:     matrix.New(),
		Platform:     chat.Matrix,
		Host:         "matrix.example.com",
		SingleHost:   false,
		OperatorHost: true,
		MaxBody:      matrix.MaxBody,
		Decode:       decode,
		RefusedHosts: []string{
			"127.0.0.1", "10.0.0.5", "[::1]", "matrix.example.com:0", "matrix.example.com:99999",
			"MATRIX.example.com", "matrix example.com", "matrix.example.com/path", "matrix.example.com?x=1",
			"matrix.example.com#x", "名前.example",
		},
		Markup: []string{"<", ">", "@room", "@user:", "#room:", "!room:"},
		Hostile: []string{
			"@room", "@user:example.org", "#room:example.org", "!abc:example.org", "<b>bold</b>",
			"<script>alert(1)</script>", `"><img src=x onerror=alert(1)>`, "' onmouseover='alert(1)",
			"<a href=\"https://evil.example\">x</a>", "https://evil.example", "www.evil.example",
			"https://matrix.to/#/@user:example.org", "&lt;b&gt;", "&#64;room", "<<b>>",
		},
		Accepted: chattest.Answer{Status: http.StatusOK, Body: []byte(`{"event_id":"$abc:example.org"}`)},
		Refusals: []chattest.Answer{
			{Status: http.StatusOK, Body: []byte(`{"errcode":"M_FORBIDDEN","error":"` + chattest.Canary + `"}`)},
			{Status: http.StatusOK, Body: []byte(chattest.Canary)},
			{Status: http.StatusOK, Body: nil},
			{Status: http.StatusOK, Body: []byte(`{"event_id":"` + `"}` + chattest.Canary)},
			{Status: http.StatusNoContent, Body: []byte(chattest.Canary)},
		},
	}
}

func TestRendererConformance(t *testing.T) {
	t.Parallel()
	chattest.RunRenderer(t, harness)
}

func TestMatrixNeedsAnAccessToken(t *testing.T) {
	t.Parallel()

	var r chat.Renderer = matrix.New()

	a, ok := r.(chat.AuthRenderer)
	if !ok || !a.AuthSecret() {
		t.Fatal("the Matrix renderer does not declare an access token")
	}
}

func TestCheckHostAcceptsAHostOfTheOperator(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"matrix.example.com", "matrix.example.com:8448", "8.8.8.8"} {
		if err := matrix.New().CheckHost(host); err != nil {
			t.Errorf("CheckHost(%q) = %v, want nil", host, err)
		}
	}
}

func TestCheckHostRuleNeverHoldsTheHost(t *testing.T) {
	t.Parallel()

	err := matrix.New().CheckHost("user:secret-9f3b@matrix.example.com")
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

func render(t *testing.T, c chat.Content) (chat.Request, event) {
	t.Helper()

	req, err := matrix.New().Render(c)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	ev, err := parseEvent(req.Body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	return req, ev
}

func TestRenderIsAPutWithTheNotificationIDAsTheTransactionID(t *testing.T) {
	t.Parallel()

	req, ev := render(t, content(func(*chat.Content) {}))

	if req.Method != http.MethodPut || req.PathSuffix != "/send/m.room.message/ntf-1" ||
		req.IdempotencyKey != "ntf-1" || req.ContentType != "application/json" {
		t.Errorf("request = %s %q key %q type %q", req.Method, req.PathSuffix, req.IdempotencyKey, req.ContentType)
	}

	if ev.Msgtype != "m.text" || ev.Format != "org.matrix.custom.html" || ev.Body == "" || ev.FormattedBody == "" {
		t.Errorf("event = %+v, want an m.text event with a body and an HTML body", ev)
	}

	// An empty m.mentions object turns off the matching of the text against
	// the keywords of the users, so @room in a name notifies no one.
	if ev.Mentions == nil || len(ev.Mentions) != 0 {
		t.Errorf("m.mentions = %v, want an empty object", ev.Mentions)
	}
}

func TestRenderRefusesATransactionIDWithTwoDots(t *testing.T) {
	t.Parallel()

	_, err := matrix.New().Render(content(func(c *chat.Content) { c.NotificationID = "a..b" }))
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("Render = %v, want ErrInvalid: the path of the request cannot hold two dots", err)
	}

	for _, id := range []string{"a.b", "a-b_c", "N.1.2"} {
		req, err := matrix.New().Render(content(func(c *chat.Content) { c.NotificationID = id }))
		if err != nil || req.PathSuffix != "/send/m.room.message/"+id {
			t.Errorf("Render with the ID %q = %q, %v; want the ID in the path", id, req.PathSuffix, err)
		}
	}
}

func TestRenderEncodesBothForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		plain string
		html  string
	}{
		{"a plain name", "network", "network", "network"},
		{"a mention of the room", "@room", "@" + zw + "room", "@" + zw + "room"},
		{"a user ID", "@bob:example.org", "@" + zw + "bob:example.org", "@" + zw + "bob:example.org"},
		{"a room alias", "#ops:example.org", "#" + zw + "ops:example.org", "#" + zw + "ops:example.org"},
		{"a room ID", "!abc:example.org", "!" + zw + "abc:example.org", "!" + zw + "abc:example.org"},
		{"a scheme", "https://x", "https:" + zw + "//x", "https:" + zw + "//x"},
		{"a bare www host", "www.x", "www." + zw + "x", "www." + zw + "x"},
		{"a tag", "<b>&", "<b>&", "&lt;b&gt;&amp;"},
		{"an attribute", `"x" 'y'`, `"x" 'y'`, "&#34;x&#34; &#39;y&#39;"},
		{"an entity that the person typed", "&lt;", "&lt;", "&amp;lt;"},
		{"a name in another script", "名前 ñ", "名前 ñ", "名前 ñ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, ev := render(
				t,
				content(func(c *chat.Content) { c.Fields = []chat.Field{{Label: "Name", Value: tt.value}} }),
			)

			if !strings.Contains(ev.Body, "\nName: "+tt.plain) {
				t.Errorf("body %q does not hold the plain form %q", ev.Body, tt.plain)
			}

			if !strings.Contains(ev.FormattedBody, "<b>Name</b>: "+tt.html) {
				t.Errorf("formatted body %q does not hold the HTML form %q", ev.FormattedBody, tt.html)
			}
		})
	}
}

func TestRenderURLOnlyInLinks(t *testing.T) {
	t.Parallel()

	_, ev := render(t, content(func(c *chat.Content) {
		c.Links[0].URL = `https://console.example/a b"><img src=x>|'`
		c.Links[0].Title = `"><b>Open</b>`
	}))

	const want = "https://console.example/a%20b%22%3E%3Cimg%20src=x%3E%7C%27"

	if !strings.Contains(ev.FormattedBody, `<a href="`+want+`">`) {
		t.Errorf("formatted body %q does not hold the encoded link", ev.FormattedBody)
	}

	if !strings.Contains(ev.Body, ": "+want) {
		t.Errorf("body %q does not hold the encoded URL", ev.Body)
	}

	// The title of the link stays text in the anchor.
	if strings.Count(ev.FormattedBody, "<a ") != 1 || strings.Contains(ev.FormattedBody, "<img") ||
		strings.Contains(ev.FormattedBody, "<b>Open") {
		t.Errorf("formatted body %q holds a tag that came from a name", ev.FormattedBody)
	}
}

func TestRenderTitleStaysOnItsLine(t *testing.T) {
	t.Parallel()

	_, ev := render(t, content(func(c *chat.Content) {
		c.Title = "a\nb"
		c.Product = "p\nq"
		c.Fields = []chat.Field{{Label: "l\nm", Value: "v\nw"}}
	}))

	lines := strings.Split(ev.Body, "\n")
	if lines[0] != "Critical: a b" || lines[1] != "p q" {
		t.Errorf("body lines = %q, want the title and the product on one line each", lines[:2])
	}

	if !strings.Contains(ev.FormattedBody, "<b>l m</b>: v<br/>w") {
		t.Errorf("formatted body %q: want the label on one line and the value with a break", ev.FormattedBody)
	}
}

func TestRenderLimits(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]chat.Content{
		"a value of entities": content(func(c *chat.Content) {
			c.Fields[0].Value = strings.Repeat(`"`, chat.MaxFieldLen)
		}),
		"a value of mentions": content(func(c *chat.Content) {
			c.Fields[0].Value = strings.Repeat("@", chat.MaxFieldLen)
		}),
		"a summary of angle brackets": content(func(c *chat.Content) {
			c.Summary = strings.Repeat("<", notify.MaxSummaryLen)
		}),
	} {
		req, ev := render(t, c)

		checkNotCut(t, name, ev)

		if len(req.Body) > matrix.MaxBody {
			t.Errorf("%s: body of %d bytes, want at most %d", name, len(req.Body), matrix.MaxBody)
		}

		if html.UnescapeString(tag.ReplaceAllString(ev.FormattedBody, "")) == "" {
			t.Errorf("%s: no text", name)
		}
	}
}

// checkNotCut checks that no text of ev is cut inside a character or an entity.
func checkNotCut(t *testing.T, name string, ev event) {
	t.Helper()

	for _, text := range []string{ev.Body, ev.FormattedBody} {
		if !utf8.ValidString(text) || strings.Contains(text, "\ufffd") {
			t.Errorf("%s: a text is cut inside a character", name)
		}
	}

	// A cut inside an entity leaves a lone & in the HTML.
	for part := range strings.SplitSeq(tag.ReplaceAllString(ev.FormattedBody, "\n"), "\n") {
		if i := strings.LastIndexByte(part, '&'); i >= 0 && !strings.Contains(part[i:], ";") {
			t.Errorf("%s: a text is cut inside an entity: %q", name, part)
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

	req, ev := render(t, content(func(c *chat.Content) { c.Links = links }))

	if len(req.Body) > matrix.MaxBody {
		t.Errorf("body of %d bytes, want at most %d", len(req.Body), matrix.MaxBody)
	}

	shown := strings.Count(ev.FormattedBody, "<a ")
	if shown == 0 || shown == notify.MaxLinks {
		t.Fatalf("shown %d links, want some but not all", shown)
	}

	if !strings.Contains(ev.Body, "more links not shown") ||
		!strings.Contains(ev.FormattedBody, "more links not shown") {
		t.Error("the event does not say that links are left out")
	}
}

func TestRenderSeverityAndEmptyParts(t *testing.T) {
	t.Parallel()

	_, ev := render(t, content(func(c *chat.Content) {
		c.Summary, c.Fields, c.Links = "", nil, nil
		c.Severity = notify.SeverityInfo
	}))

	for _, want := range []string{"Info", "OpenDustpan", "Job failed"} {
		if !strings.Contains(ev.Body, want) || !strings.Contains(ev.FormattedBody, want) {
			t.Errorf("event %+v does not hold %q in both forms", ev, want)
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
		{"an event", 200, `{"event_id":"$abc:example.org"}`, nil},
		{"an event with more fields", 200, `{"event_id":"$x","extra":1}`, nil},
		{"an error object", 200, `{"errcode":"M_FORBIDDEN","error":"` + chattest.Canary + `"}`, notify.ErrPermanent},
		{"an empty event ID", 200, `{"event_id":""}`, notify.ErrPermanent},
		{"an event ID that is no string", 200, `{"event_id":7}`, notify.ErrPermanent},
		{"no body", 200, "", notify.ErrPermanent},
		{"a body that is no JSON", 200, "ok " + chattest.Canary, notify.ErrPermanent},
		{"an array", 200, `[{"event_id":"$x"}]`, notify.ErrPermanent},
		{"an event with a 202 status", 202, `{"event_id":"$x"}`, notify.ErrPermanent},
		{"an event with a 4xx status", 403, `{"event_id":"$x"}`, notify.ErrPermanent},
	}

	for _, tt := range tests {
		err := matrix.New().Check(tt.status, []byte(tt.body))
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Check = %v, want %v", tt.name, err, tt.want)
		}

		if err != nil && strings.Contains(err.Error(), chattest.Canary) {
			t.Errorf("%s: the error text repeats the body", tt.name)
		}
	}
}
