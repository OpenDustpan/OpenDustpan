package chattest_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/notify/chat/chattest"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
)

// fakeHost is the one host that the fake platform uses for its webhooks.
const fakeHost = "hooks.fake.example"

// fakeMaxBody is the limit of a request body of the fake platform. It is
// lower than chat.MaxRequestBytes, as the limit of some real platforms is.
const fakeMaxBody = 28 * 1024

// fakeTextBudget is the room for all texts together, after encoding. The
// links hold URLs that are not cut, so the budget leaves room for them.
const fakeTextBudget = 4 * 1024

// fakeRenderer is a Renderer for a fake platform that parses & < > as markup.
// It escapes them, and it cuts a text on a character boundary when the
// budget of the message ends. Platform returns chat.Slack because the
// platform of a Renderer must be one of the known platforms, and the fake
// has no platform of its own.
type fakeRenderer struct{}

func (fakeRenderer) Platform() chat.Platform { return chat.Slack }

func (fakeRenderer) CheckHost(host string) error {
	if host != fakeHost {
		return fmt.Errorf("%w: the fake platform uses one host", notify.ErrInvalid)
	}

	return nil
}

// budget holds the room that is left for the texts of one message.
type budget struct{ left int }

// text escapes s and cuts it before the first character that does not fit.
// It works on whole characters and on whole escapes, so a cut never splits
// either one.
func (b *budget) text(s string) string {
	var out strings.Builder

	for _, r := range s {
		e := escapeRune(r)
		if len(e) > b.left {
			break
		}

		out.WriteString(e)

		b.left -= len(e)
	}

	return out.String()
}

func escapeRune(r rune) string {
	switch r {
	case '&':
		return "&amp;"
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	default:
		return string(r)
	}
}

type fakeField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type fakeLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type fakeMessage struct {
	Product  string      `json:"product"`
	Title    string      `json:"title"`
	Summary  string      `json:"summary"`
	Severity string      `json:"severity"`
	Fields   []fakeField `json:"fields"`
	Links    []fakeLink  `json:"links"`
}

func (fakeRenderer) Render(c chat.Content) (chat.Request, error) {
	if err := c.Validate(); err != nil {
		return chat.Request{}, err
	}

	b := &budget{left: fakeTextBudget}
	m := fakeMessage{
		Product:  b.text(c.Product),
		Title:    b.text(c.Title),
		Summary:  b.text(c.Summary),
		Severity: c.Severity.String(),
		Fields:   nil,
		Links:    nil,
	}

	for _, f := range c.Fields {
		m.Fields = append(m.Fields, fakeField{Label: b.text(f.Label), Value: b.text(f.Value)})
	}

	for _, l := range c.Links {
		// The URL goes in a field of its own that the fake platform does not
		// parse as markup. Only the title is markup.
		m.Links = append(m.Links, fakeLink{Title: b.text(l.Title), URL: l.URL})
	}

	// The encoder keeps & < > as they are. The default encoder writes them as
	// \u0026 and so on, which would grow the body past the budget.
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(m); err != nil {
		return chat.Request{}, fmt.Errorf("%w: %w", notify.ErrInvalid, err)
	}

	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))

	return chat.Request{
		Method:         http.MethodPost,
		PathSuffix:     "",
		IdempotencyKey: c.NotificationID,
		ContentType:    "application/json",
		Body:           body,
	}, nil
}

// decodeFake returns the texts that the fake platform parses as markup.
func decodeFake(body []byte) ([]string, error) {
	var m fakeMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("decode the fake body: %w", err)
	}

	texts := []string{m.Product, m.Title, m.Summary, m.Severity}
	for _, f := range m.Fields {
		texts = append(texts, f.Label, f.Value)
	}

	for _, l := range m.Links {
		texts = append(texts, l.Title)
	}

	return texts, nil
}

func (fakeRenderer) Check(_ int, body []byte) error {
	if strings.HasPrefix(string(body), "error:") {
		return fmt.Errorf("%w: the fake tool refused the message", notify.ErrPermanent)
	}

	return nil
}

func TestRendererConformance(t *testing.T) {
	t.Parallel()

	chattest.RunRenderer(t, func(*testing.T) chattest.Harness {
		return chattest.Harness{
			Renderer:     fakeRenderer{},
			Platform:     chat.Slack,
			Host:         fakeHost,
			SingleHost:   true,
			OperatorHost: false,
			MaxBody:      fakeMaxBody,
			Decode:       decodeFake,
			Markup:       []string{"<", ">"},
			RefusedHosts: []string{"hooks.other.example"},
			Hostile: []string{
				"<!channel>",
				"<!here>",
				"<@U123ABC>",
				"<https://evil.example|click>",
				"&lt;!channel&gt;",
			},
			Accepted: chattest.Answer{Status: 200, Body: []byte("ok")},
			Refusals: []chattest.Answer{{Status: 200, Body: []byte("error: " + chattest.Canary)}},
		}
	})
}
