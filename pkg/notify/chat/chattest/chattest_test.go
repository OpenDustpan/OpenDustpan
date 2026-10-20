package chattest_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
	"opendustpan.org/opendustpan/pkg/notify/chat/chattest"
)

// fakeHost is the one host that the fake tool uses for its webhooks.
const fakeHost = "hooks.fake.example"

// fakeRenderer is a Renderer for a fake tool. Its JSON encoder escapes the
// characters that start a mention in the tool.
type fakeRenderer struct{}

func (fakeRenderer) Tool() chat.Tool { return chat.Slack }

func (fakeRenderer) CheckHost(host string) error {
	if host != fakeHost {
		return fmt.Errorf("%w: the fake tool uses one host", notify.ErrInvalid)
	}

	return nil
}

func (fakeRenderer) Render(c chat.Content) (chat.Request, error) {
	if err := c.Validate(); err != nil {
		return chat.Request{}, err
	}

	body, err := json.Marshal(map[string]any{
		"product": c.Product, "title": c.Title, "summary": c.Summary, "severity": c.Severity.String(),
		"fields": c.Fields, "links": c.Links,
	})
	if err != nil {
		return chat.Request{}, fmt.Errorf("%w: %w", notify.ErrInvalid, err)
	}

	if len(body) > chat.MaxRequestBytes {
		return chat.Request{}, fmt.Errorf("%w: the message is too large", notify.ErrInvalid)
	}

	return chat.Request{ContentType: "application/json", Body: body}, nil
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
			Tool:         chat.Slack,
			Host:         fakeHost,
			RefusedHosts: []string{"hooks.other.example"},
			Hostile:      []string{"<!channel>", "<!here>", "<@U123ABC>"},
			Accepted:     chattest.Answer{Status: 200, Body: []byte("ok")},
			Refusals:     []chattest.Answer{{Status: 200, Body: []byte("error: " + chattest.Canary)}},
		}
	})
}
