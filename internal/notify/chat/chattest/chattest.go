// Package chattest is the conformance suite of chat.Renderer. An adapter
// for a chat platform runs RunRenderer with a harness that names the hosts, the
// hostile strings and the answers of its platform.
package chattest

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
)

// Answer is one answer of a chat tool to a request.
type Answer struct {
	Status int
	Body   []byte
}

// Harness holds the renderer under test and the facts of its tool.
type Harness struct {
	// Renderer is the adapter under test.
	Renderer chat.Renderer
	// Platform is the chat platform that Renderer serves.
	Platform chat.Platform
	// Host is a host that the platform uses for its webhooks.
	Host string
	// SingleHost is true for a platform with one public host. CheckHost must
	// refuse Host with a port, and any host with user information.
	SingleHost bool
	// OperatorHost is true for a platform that the operator hosts, so that
	// CheckHost takes any host that the egress rules take. The suite then does
	// not expect CheckHost to refuse a sub-domain of Host.
	OperatorHost bool
	// MaxBody is the largest request body that the platform takes, in bytes.
	// Zero means chat.MaxRequestBytes. A platform with a lower limit, for
	// example about 28 KB for Microsoft Teams, sets it.
	MaxBody int
	// Decode returns the texts of a request body that the platform parses as
	// markup, after the decoding of the wire format (for JSON, after the
	// decoding of the string escapes). A URL that the platform takes in a
	// field of its own is not one of these texts. The hostile checks run on
	// them, because the bytes of a JSON body hide a character such as < behind
	// an escape and a check on the bytes passes by mistake.
	Decode func(body []byte) ([]string, error)
	// RefusedHosts are hosts that CheckHost must refuse, for example the host
	// of another service. Defense in depth: the egress rules refuse a private
	// address too, and this check does not rely on them.
	RefusedHosts []string
	// Markup lists the characters or sequences that start markup in the
	// platform, for example < and > in Slack. No decoded text of a body may
	// hold one, whatever the input: the Renderer encodes each of them. A check
	// on whole hostile strings alone passes when the Renderer encodes only
	// part of a string, so this list closes that gap.
	Markup []string
	// Hostile are strings that do something in the platform, for example a
	// mention of the whole channel. Render must return nil for a Content that
	// holds one in any text. It encodes the string, and no decoded text of the
	// body holds it.
	Hostile []string
	// Accepted is an answer that means the platform took the message.
	Accepted Answer
	// Refusals are answers with a 2xx status that mean the platform refused the
	// message. Each body holds the marker Canary.
	Refusals []Answer
}

// Canary is the marker that a refusal body holds. An error text must not
// repeat it.
const Canary = "canary-9f3b7c1d"

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// content returns a valid Content with the given text in every field that
// holds text.
func content(text string) chat.Content {
	return chat.Content{
		NotificationID: "n-1",
		Product:        "Product " + text,
		Title:          "Title " + text,
		Summary:        "Summary " + text,
		Severity:       notify.SeverityWarning,
		Fields: []chat.Field{
			{Label: "Namespace", Value: "ns " + text},
			{Label: "Stack", Value: "stack " + text},
		},
		Links: []notify.Link{
			{Title: "Open " + text, URL: "https://console.example/units/1", Kind: notify.LinkConsole},
		},
	}
}

// maxContent returns the largest valid Content.
func maxContent() chat.Content {
	return sizedContent(func(n int) string { return strings.Repeat("x", n) })
}

// expandingContent returns the largest valid Content whose every text is
// made of characters that grow when a platform encodes them (< becomes
// &lt; or \u003c, and é takes two bytes), so that a Renderer must cut a
// text inside its limit.
func expandingContent() chat.Content {
	const unit = "<é&"

	return sizedContent(func(n int) string { return strings.Repeat(unit, n/len(unit)) })
}

// sizedContent builds a Content with every text at its bound. fill returns a
// text of at most the given length in bytes.
func sizedContent(fill func(n int) string) chat.Content {
	c := chat.Content{
		NotificationID: "n-max",
		Product:        fill(chat.MaxProductLen),
		Title:          fill(chat.MaxTitleLen),
		Summary:        fill(notify.MaxSummaryLen),
		Severity:       notify.SeverityCritical,
		Fields:         nil,
		Links:          nil,
	}

	for range chat.MaxFields {
		c.Fields = append(c.Fields, chat.Field{
			Label: fill(chat.MaxFieldLen),
			Value: fill(chat.MaxFieldLen),
		})
	}

	for range notify.MaxLinks {
		c.Links = append(c.Links, notify.Link{
			Title: fill(notify.MaxLinkTitleLen),
			URL: "https://console.example/" + strings.Repeat(
				"u",
				notify.MaxLinkURLLen-len("https://console.example/"),
			),
			Kind: notify.LinkConsole,
		})
	}

	return c
}

// maxBody returns the limit of a request body for the platform of h.
func (h Harness) maxBody() int {
	if h.MaxBody > 0 {
		return min(h.MaxBody, chat.MaxRequestBytes)
	}

	return chat.MaxRequestBytes
}

// texts decodes the body of a request into the texts that the platform
// parses as markup. It fails the test when the body does not decode.
func (h Harness) texts(t *testing.T, body []byte) []string {
	t.Helper()

	got, err := h.Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("Decode returned no text")
	}

	return got
}

// RunRenderer executes the conformance suite of chat.Renderer against
// harnesses from newHarness:
//
//	func TestRendererConformance(t *testing.T) {
//		t.Parallel()
//		chattest.RunRenderer(t, func(t *testing.T) chattest.Harness { return newHarness(t) })
//	}
func RunRenderer(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness)
	}{
		{"the platform is the platform of the harness and a known platform", testPlatform},
		{"the host of the platform passes CheckHost", testHostAccepted},
		{"a host of another service fails CheckHost with ErrInvalid", testHostRefused},
		{"a valid Content renders a request within the bounds", testRenderBounds},
		{"the largest valid Content renders nil within the limit of the platform", testRenderLargest},
		{"a Content that grows when encoded renders nil, cut on a character boundary", testRenderExpanding},
		{"the same Content renders the same request", testRenderDeterministic},
		{"the request shows the product name, the title, the fields and the severity", testRenderShowsContent},
		{"a hostile string in any text renders nil and does nothing in the decoded text", testRenderHostile},
		{"a hostile URL does nothing in the decoded text", testRenderHostileLink},
		{"a line feed in a text renders nil", testRenderLineFeed},
		{"a Content that breaks a bound is ErrInvalid and renders nothing", testRenderInvalid},
		{"a hostile character in a text is ErrInvalid and renders nothing", testRenderCharacters},
		{"an accepted answer is nil", testCheckAccepted},
		{"a refusal with a 2xx status is an error and the text repeats no body", testCheckRefusals},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

func testPlatform(t *testing.T, h Harness) {
	t.Helper()

	if got := h.Renderer.Platform(); got != h.Platform || !got.Valid() {
		t.Fatalf("Platform() = %q, want the known platform %q", got, h.Platform)
	}
}

func testHostAccepted(t *testing.T, h Harness) {
	t.Helper()

	if err := h.Renderer.CheckHost(h.Host); err != nil {
		t.Fatalf("CheckHost(%q) = %v, want nil", h.Host, err)
	}
}

func testHostRefused(t *testing.T, h Harness) {
	t.Helper()

	hosts := append([]string{"", "localhost", "169.254.169.254", "user@" + h.Host}, h.RefusedHosts...)
	if !h.OperatorHost {
		hosts = append(hosts, h.Host+".evil.example")
	}

	if h.SingleHost {
		hosts = append(hosts, h.Host+":8443", h.Host+":443")
	}

	for _, host := range hosts {
		if err := h.Renderer.CheckHost(host); !errors.Is(err, notify.ErrInvalid) {
			t.Errorf("CheckHost(%q) = %v, want ErrInvalid", host, err)
		}
	}
}

// render renders c and fails the test when it does not return a request
// within the limit of the platform. Every valid Content renders.
func render(t *testing.T, h Harness, c chat.Content) chat.Request {
	t.Helper()

	req, err := h.Renderer.Render(c)
	if err != nil {
		t.Fatalf("Render of a valid Content: %v", err)
	}

	if req.ContentType == "" || len(req.Body) == 0 || len(req.Body) > h.maxBody() {
		t.Fatalf("Render = content type %q, %d bytes; want a type and 1 to %d bytes", req.ContentType, len(req.Body),
			h.maxBody())
	}

	checkRequest(t, req, c)

	return req
}

// checkRequest fails the test when the method, the path suffix or the
// idempotency key of req breaks the contract of chat.Request.
func checkRequest(t *testing.T, req chat.Request, c chat.Content) {
	t.Helper()

	if req.Method != http.MethodPost && req.Method != http.MethodPut {
		t.Errorf("Request.Method = %q, want POST or PUT", req.Method)
	}

	if req.IdempotencyKey != c.NotificationID {
		t.Errorf("Request.IdempotencyKey = %q, want the notification ID %q", req.IdempotencyKey, c.NotificationID)
	}

	suffix := req.PathSuffix
	if suffix == "" {
		return
	}

	if len(suffix) > chat.MaxPathSuffixLen || suffix[0] != '/' || strings.HasPrefix(suffix, "//") ||
		strings.ContainsAny(suffix, "?#\\:") || strings.Contains(suffix, "..") ||
		strings.ContainsFunc(suffix, func(r rune) bool { return r < 0x21 || r == 0x7f }) {
		t.Errorf(
			"Request.PathSuffix = %q, want a relative path with no query, fragment, dot segment or control character",
			suffix,
		)
	}
}

func testRenderBounds(t *testing.T, h Harness) {
	t.Helper()

	render(t, h, content("plain"))
}

func testRenderLargest(t *testing.T, h Harness) {
	t.Helper()

	req := render(t, h, maxContent())

	h.checkDecoded(t, req)
	h.checkNoMarkup(t, "", req)
}

func testRenderExpanding(t *testing.T, h Harness) {
	t.Helper()

	req := render(t, h, expandingContent())

	h.checkDecoded(t, req)
	h.checkNoMarkup(t, "", req)
}

// checkDecoded fails the test when a decoded text of req is not valid UTF-8,
// which means that the Renderer cut a text inside a character. A cut inside
// an escape sequence leaves a text that does not decode, or a lone &, so the
// check on the decoding catches it too.
func (h Harness) checkDecoded(t *testing.T, req chat.Request) {
	t.Helper()

	for _, text := range h.texts(t, req.Body) {
		if !utf8.ValidString(text) {
			t.Error("a decoded text is not valid UTF-8: the Renderer cut inside a character")
		}

		if strings.Contains(text, "\ufffd") {
			t.Error("a decoded text holds the replacement character: the Renderer cut inside a character")
		}
	}
}

// checkNoMarkup fails the test when a decoded text of req holds the input
// unchanged or any sequence of Markup.
func (h Harness) checkNoMarkup(t *testing.T, input string, req chat.Request) {
	t.Helper()

	for _, text := range h.texts(t, req.Body) {
		if input != "" && strings.Contains(text, input) {
			t.Errorf("a decoded text of the body holds %q unchanged", input)
		}

		for _, m := range h.Markup {
			if strings.Contains(text, m) {
				t.Errorf("a decoded text of the body holds the markup %q (input %q)", m, input)
			}
		}
	}
}

func testRenderDeterministic(t *testing.T, h Harness) {
	t.Helper()

	first := render(t, h, content("same"))
	second := render(t, h, content("same"))

	if !bytes.Equal(first.Body, second.Body) || first.ContentType != second.ContentType ||
		first.Method != second.Method || first.PathSuffix != second.PathSuffix {
		t.Fatal("two renders of one Content differ")
	}
}

func testRenderShowsContent(t *testing.T, h Harness) {
	t.Helper()

	req := render(t, h, content("shown"))
	joined := strings.Join(h.texts(t, req.Body), "\n")

	for _, want := range []string{
		"Product shown", "Title shown", "Summary shown", "Open shown",
		"Namespace", "ns shown", "Stack", "stack shown",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the decoded text does not show %q", want)
		}
	}

	if !bytes.Contains(req.Body, []byte("https://console.example/units/1")) {
		t.Error("the body does not hold the link")
	}

	// The platform shows the severity in a word, a color or a symbol, so the
	// check compares two requests that differ only in the severity.
	info := content("shown")
	info.Severity = notify.SeverityInfo
	critical := content("shown")
	critical.Severity = notify.SeverityCritical

	if bytes.Equal(render(t, h, info).Body, render(t, h, critical).Body) {
		t.Error("the request does not show the severity")
	}
}

func testRenderHostile(t *testing.T, h Harness) {
	t.Helper()

	for _, hostile := range h.Hostile {
		// A product name, a title, a summary, a value and a link title carry
		// the string. The label of the first field carries it too.
		c := content(hostile)
		c.Fields[0].Label = hostile

		req, err := h.Renderer.Render(c)
		if err != nil {
			t.Errorf(
				"Render with %q = %v, want nil: the Renderer encodes a hostile string and does not refuse it",
				hostile,
				err,
			)

			continue
		}

		if len(req.Body) > h.maxBody() {
			t.Errorf("Render with %q = %d bytes, want at most %d", hostile, len(req.Body), h.maxBody())
		}

		h.checkNoMarkup(t, hostile, req)
	}
}

// hostileURLs returns links that are valid URLs and that carry the markup of
// a chat platform: a pipe that starts the label of a Slack link, and a
// mention of the whole channel.
func hostileURLs() []string {
	return []string{
		"https://console.example/x|<!channel>",
		"https://console.example/x>|<!here>",
		"https://console.example/x?a=<@U123ABC>&b=|",
	}
}

func testRenderHostileLink(t *testing.T, h Harness) {
	t.Helper()

	for _, hostile := range hostileURLs() {
		c := content("link")
		c.Links[0].URL = hostile

		req, err := h.Renderer.Render(c)
		if err != nil {
			t.Errorf("Render with the link %q = %v, want nil", hostile, err)

			continue
		}

		h.checkNoMarkup(t, hostile, req)

		for _, text := range h.texts(t, req.Body) {
			if strings.ContainsAny(text, "|<>") && strings.Contains(text, "console.example") {
				t.Errorf("a decoded text of the body holds the URL %q as markup", hostile)
			}
		}
	}
}

func testRenderLineFeed(t *testing.T, h Harness) {
	t.Helper()

	c := content("feed")
	c.Summary = "line one\nline two"

	render(t, h, c)
}

func testRenderInvalid(t *testing.T, h Harness) {
	t.Helper()

	tooMany := content("x")
	for range chat.MaxFields {
		tooMany.Fields = append(tooMany.Fields, chat.Field{Label: "l", Value: "v"})
	}

	cases := map[string]func(c *chat.Content){
		"no product":               func(c *chat.Content) { c.Product = "" },
		"no title":                 func(c *chat.Content) { c.Title = "" },
		"no notification ID":       func(c *chat.Content) { c.NotificationID = "" },
		"a notification ID with /": func(c *chat.Content) { c.NotificationID = "a/../b" },
		"unknown severity":         func(c *chat.Content) { c.Severity = 0 },
		"too many fields":          func(c *chat.Content) { c.Fields = tooMany.Fields },
		"a field with no key":      func(c *chat.Content) { c.Fields = []chat.Field{{Label: "", Value: "v"}} },
		"a javascript link":        func(c *chat.Content) { c.Links[0].URL = "javascript:alert(1)" },
		"a link with a user":       func(c *chat.Content) { c.Links[0].URL = "https://user:pw@console.example/" },
		"a link with no host":      func(c *chat.Content) { c.Links[0].URL = "https:///path" },
	}

	for name, mutate := range cases {
		c := content("x")
		mutate(&c)

		h.wantInvalid(t, name, c)
	}
}

func testRenderCharacters(t *testing.T, h Harness) {
	t.Helper()

	// Each string is a character that Validate refuses in a text: a carriage
	// return, a NUL, a right-to-left override, and bytes that are not UTF-8.
	for name, bad := range map[string]string{
		"CR":           "a\rb",
		"NUL":          "a\x00b",
		"RLO":          "a\u202eb",
		"invalid UTF8": "a\xffb",
		"DEL":          "a\x7fb",
	} {
		for field, mutate := range map[string]func(c *chat.Content){
			"product":    func(c *chat.Content) { c.Product = bad },
			"title":      func(c *chat.Content) { c.Title = bad },
			"summary":    func(c *chat.Content) { c.Summary = bad },
			"label":      func(c *chat.Content) { c.Fields[0].Label = bad },
			"value":      func(c *chat.Content) { c.Fields[0].Value = bad },
			"link title": func(c *chat.Content) { c.Links[0].Title = bad },
		} {
			c := content("x")
			mutate(&c)

			h.wantInvalid(t, name+" in the "+field, c)
		}
	}
}

// wantInvalid fails the test unless Render returns ErrInvalid and no body.
func (h Harness) wantInvalid(t *testing.T, name string, c chat.Content) {
	t.Helper()

	req, err := h.Renderer.Render(c)
	if !errors.Is(err, notify.ErrInvalid) {
		t.Errorf("%s: Render = %v, want ErrInvalid", name, err)
	}

	if len(req.Body) != 0 {
		t.Errorf("%s: Render returned a body with an error", name)
	}
}

func testCheckAccepted(t *testing.T, h Harness) {
	t.Helper()

	if err := h.Renderer.Check(h.Accepted.Status, h.Accepted.Body); err != nil {
		t.Fatalf("Check of an accepted answer = %v, want nil", err)
	}
}

func testCheckRefusals(t *testing.T, h Harness) {
	t.Helper()

	for _, a := range h.Refusals {
		err := h.Renderer.Check(a.Status, a.Body)
		if !errors.Is(err, notify.ErrPermanent) && !errors.Is(err, notify.ErrTemporary) {
			t.Errorf("Check(%d, refusal) = %v, want ErrPermanent or ErrTemporary", a.Status, err)

			continue
		}

		if strings.Contains(err.Error(), Canary) {
			t.Errorf("Check(%d, refusal): the error text repeats the body", a.Status)
		}
	}
}
