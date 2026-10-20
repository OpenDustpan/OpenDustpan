// Package chattest is the conformance suite of chat.Renderer. An adapter
// for a chat tool runs RunRenderer with a harness that names the hosts, the
// hostile strings and the answers of its tool.
package chattest

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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
	// Tool is the chat tool that Renderer serves.
	Tool chat.Tool
	// Host is a host that the tool uses for its webhooks.
	Host string
	// RefusedHosts are hosts that CheckHost must refuse, for example the host
	// of another service. Defense in depth: the egress rules refuse a private
	// address too, and this check does not rely on them.
	RefusedHosts []string
	// Hostile are strings that do something in the tool, for example a
	// mention of the whole channel. A Content that holds one in any text must
	// not put it in the body unchanged.
	Hostile []string
	// Accepted is an answer that means the tool took the message.
	Accepted Answer
	// Refusals are answers with a 2xx status that mean the tool refused the
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
		Product:  "Product " + text,
		Title:    "Title " + text,
		Summary:  "Summary " + text,
		Severity: notify.SeverityWarning,
		Fields:   []chat.Field{{Label: "Namespace", Value: text}, {Label: "Stack", Value: text}},
		Links: []notify.Link{
			{Title: "Open " + text, URL: "https://console.example/units/1", Kind: notify.LinkConsole},
		},
	}
}

// maxContent returns the largest valid Content.
func maxContent() chat.Content {
	c := chat.Content{
		Product:  strings.Repeat("p", chat.MaxProductLen),
		Title:    strings.Repeat("t", chat.MaxTitleLen),
		Summary:  strings.Repeat("s", notify.MaxSummaryLen),
		Severity: notify.SeverityCritical,
		Fields:   nil,
		Links:    nil,
	}

	for range chat.MaxFields {
		c.Fields = append(c.Fields, chat.Field{
			Label: strings.Repeat("l", chat.MaxFieldLen),
			Value: strings.Repeat("v", chat.MaxFieldLen),
		})
	}

	for range notify.MaxLinks {
		c.Links = append(c.Links, notify.Link{
			Title: strings.Repeat("k", notify.MaxLinkTitleLen),
			URL: "https://console.example/" + strings.Repeat(
				"u",
				notify.MaxLinkURLLen-len("https://console.example/"),
			),
			Kind: notify.LinkConsole,
		})
	}

	return c
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
		{"the tool is the tool of the harness and a known tool", testTool},
		{"the host of the tool passes CheckHost", testHostAccepted},
		{"a host of another service fails CheckHost with ErrInvalid", testHostRefused},
		{"a valid Content renders a body within the bounds", testRenderBounds},
		{"the largest valid Content renders within the bounds or is ErrInvalid", testRenderLargest},
		{"the same Content renders the same body", testRenderDeterministic},
		{"the body shows the product name, the title and the fields", testRenderShowsContent},
		{"a hostile string in any text does nothing in the body", testRenderHostile},
		{"a Content that breaks a bound is ErrInvalid and renders nothing", testRenderInvalid},
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

func testTool(t *testing.T, h Harness) {
	t.Helper()

	if got := h.Renderer.Tool(); got != h.Tool || !got.Valid() {
		t.Fatalf("Tool() = %q, want the known tool %q", got, h.Tool)
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

	for _, host := range append([]string{"", "localhost", "169.254.169.254", h.Host + ".evil.example"}, h.RefusedHosts...) {
		if err := h.Renderer.CheckHost(host); !errors.Is(err, notify.ErrInvalid) {
			t.Errorf("CheckHost(%q) = %v, want ErrInvalid", host, err)
		}
	}
}

func testRenderBounds(t *testing.T, h Harness) {
	t.Helper()

	req, err := h.Renderer.Render(content("plain"))
	if err != nil {
		t.Fatalf("Render of a valid Content: %v", err)
	}

	if req.ContentType == "" || len(req.Body) == 0 || len(req.Body) > chat.MaxRequestBytes {
		t.Fatalf("Render = content type %q, %d bytes; want a type and 1 to %d bytes", req.ContentType, len(req.Body),
			chat.MaxRequestBytes)
	}
}

func testRenderLargest(t *testing.T, h Harness) {
	t.Helper()

	req, err := h.Renderer.Render(maxContent())
	if err != nil {
		if !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("Render of the largest Content = %v, want nil or ErrInvalid", err)
		}

		return
	}

	if len(req.Body) > chat.MaxRequestBytes {
		t.Fatalf("Render of the largest Content = %d bytes, want at most %d", len(req.Body), chat.MaxRequestBytes)
	}
}

func testRenderDeterministic(t *testing.T, h Harness) {
	t.Helper()

	first, err := h.Renderer.Render(content("same"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	second, err := h.Renderer.Render(content("same"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if !bytes.Equal(first.Body, second.Body) || first.ContentType != second.ContentType {
		t.Fatal("two renders of one Content differ")
	}
}

func testRenderShowsContent(t *testing.T, h Harness) {
	t.Helper()

	req, err := h.Renderer.Render(content("shown"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	for _, want := range []string{"Product shown", "Title shown", "Summary shown", "Namespace", "Stack", "https://console.example/units/1"} {
		if !bytes.Contains(req.Body, []byte(want)) {
			t.Errorf("the body does not show %q", want)
		}
	}
}

func testRenderHostile(t *testing.T, h Harness) {
	t.Helper()

	for _, hostile := range h.Hostile {
		c := content(hostile)
		// A product name, a title, a summary, a label, a value and a link
		// title can each carry the string.
		c.Fields[0].Label = hostile

		req, err := h.Renderer.Render(c)
		if err != nil {
			if !errors.Is(err, notify.ErrInvalid) {
				t.Errorf("Render with %q = %v, want nil or ErrInvalid", hostile, err)
			}

			continue
		}

		if bytes.Contains(req.Body, []byte(hostile)) {
			t.Errorf("the body holds the hostile string %q unchanged", hostile)
		}
	}
}

func testRenderInvalid(t *testing.T, h Harness) {
	t.Helper()

	tooMany := content("x")
	for range chat.MaxFields {
		tooMany.Fields = append(tooMany.Fields, chat.Field{Label: "l", Value: "v"})
	}

	cases := map[string]func(c *chat.Content){
		"no product":          func(c *chat.Content) { c.Product = "" },
		"no title":            func(c *chat.Content) { c.Title = "" },
		"unknown severity":    func(c *chat.Content) { c.Severity = 0 },
		"too many fields":     func(c *chat.Content) { c.Fields = tooMany.Fields },
		"a field with no key": func(c *chat.Content) { c.Fields = []chat.Field{{Label: "", Value: "v"}} },
		"a javascript link":   func(c *chat.Content) { c.Links[0].URL = "javascript:alert(1)" },
		"a link with a user":  func(c *chat.Content) { c.Links[0].URL = "https://user:pw@console.example/" },
		"a link with no host": func(c *chat.Content) { c.Links[0].URL = "https:///path" },
	}

	for name, mutate := range cases {
		c := content("x")
		mutate(&c)

		req, err := h.Renderer.Render(c)
		if !errors.Is(err, notify.ErrInvalid) {
			t.Errorf("%s: Render = %v, want ErrInvalid", name, err)
		}

		if len(req.Body) != 0 {
			t.Errorf("%s: Render returned a body with an error", name)
		}
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
