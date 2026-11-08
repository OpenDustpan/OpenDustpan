// Package slack is the chat.Renderer of Slack. It turns a chat.Content into
// the JSON document that an incoming webhook of Slack takes, and it reads the
// answer of Slack. It does no input or output: chat.Sender posts the request.
//
// The message has a fallback text for a notification on a phone, and blocks
// for the product name, the title with the severity, the summary, the fields
// and the links. Every text of a Content is untrusted, so the renderer writes
// each one as mrkdwn text with these steps:
//
//   - it replaces &, < and > with their entities, so a mention such as
//     <!channel> or <@U123>, and a link markup, does nothing;
//   - it puts a zero-width space after a character that starts a style
//     (*, ~, a backtick, and _ at the edge of a word), so a name cannot make
//     text bold, struck through, italic or code;
//   - it cuts a text on a character boundary, never inside an entity.
//
// Slack also parses a mrkdwn text on its own: it turns @here, #general, a URL
// and a bare host name into a link or a mention. So every mrkdwn object sets
// verbatim to true, and Slack shows the text as it is. The fallback text has
// no such switch, so the renderer puts a zero-width space after @, #, a dot
// and a colon in it, after their ideographic, fullwidth, halfwidth and small
// forms, after the one dot leader and after the fullwidth solidus, and no
// automatic link or mention starts there.
//
// A URL goes only in the url field of a button, and never in text. The button
// has a fixed label, so no text of a person is in a plain_text object, where
// the entity rules of Slack are not documented.
package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
)

// The hosts of the incoming webhooks of Slack: the public service and
// GovSlack.
const (
	HostPublic = "hooks.slack.com"
	HostGov    = "hooks.slack-gov.com"
)

// Limits of Slack for the text of a block, in bytes. Slack counts characters,
// so a limit in bytes is a lower limit.
const (
	maxSectionText = 3000
	maxFieldText   = 2000
	maxLinkText    = 3000
	maxFallback    = 1000
	// fieldLabelShare is the part of a field text that the label may take: one
	// half.
	fieldLabelShare = 2
	// fieldMarkup is the bytes that a field adds around its label and value: two
	// asterisks and a line feed.
	fieldMarkup = 3
	// The types of the objects of a message.
	typeMrkdwn  = "mrkdwn"
	typeSection = "section"
	// ellipsis ends a text that the renderer cut.
	ellipsis = "…"
	// zeroWidthSpace breaks the style markup of mrkdwn.
	zeroWidthSpace = "\u200b"
)

// Renderer is the chat.Renderer of Slack. The zero value is ready to use.
type Renderer struct{}

var _ chat.Renderer = Renderer{}

// New returns the Renderer of Slack.
func New() Renderer { return Renderer{} }

// Platform returns chat.Slack.
func (Renderer) Platform() chat.Platform { return chat.Slack }

// CheckHost returns a *chat.HostRuleError, which matches notify.ErrInvalid, for every host
// other than HostPublic and HostGov. The match is exact: a port, user
// information, a sub-domain and a different case all fail.
func (Renderer) CheckHost(host string) error {
	if host == HostPublic || host == HostGov {
		return nil
	}

	return &chat.HostRuleError{Rule: "The Slack URL must use " + HostPublic + " or a GovSlack host."}
}

// Check reads the answer of Slack. Slack answers a message that it took with
// the text ok. Any other body with a 2xx status is a refusal, and the sender
// does not try it again, so a message that Slack maybe took is not posted
// twice. The error text never holds the body.
func (Renderer) Check(status int, body []byte) error {
	if status >= http.StatusOK && status < http.StatusMultipleChoices && string(bytes.TrimSpace(body)) == "ok" {
		return nil
	}

	return fmt.Errorf("%w: Slack did not answer ok", notify.ErrPermanent)
}

// text is a text object. Verbatim is set on each mrkdwn object, and Slack
// accepts it on no other type, so the label of a button leaves it false.
type text struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Verbatim bool   `json:"verbatim,omitempty"`
}

// mrkdwnText returns a mrkdwn text object that Slack shows as it is, without
// the automatic links and mentions.
func mrkdwnText(t string) text { return text{Type: typeMrkdwn, Text: t, Verbatim: true} }

// button is a link button with a fixed label.
type button struct {
	Type     string `json:"type"`
	Text     text   `json:"text"`
	URL      string `json:"url"`
	ActionID string `json:"action_id"` //nolint:tagliatelle // Slack names the field action_id.
}

// block is one block of the message. The fields that a type does not use
// stay empty and are left out of the JSON.
type block struct {
	Type      string `json:"type"`
	Text      *text  `json:"text,omitempty"`
	Fields    []text `json:"fields,omitempty"`
	Elements  []text `json:"elements,omitempty"`
	Accessory any    `json:"accessory,omitempty"`
}

// message is the body of the request.
type message struct {
	Text   string  `json:"text"`
	Blocks []block `json:"blocks"`
}

// Render returns the request for c. See chat.Renderer.
func (Renderer) Render(c chat.Content) (chat.Request, error) {
	if err := c.Validate(); err != nil {
		return chat.Request{}, fmt.Errorf("render Slack message: %w", err)
	}

	style := severityStyleOf(c.Severity)
	label := style.label
	title := fmt.Sprintf("%s *%s*", style.emoji, mrkdwn(c.Title, maxSectionText))

	msg := message{
		Text: fallbackText(label+": "+c.Title, maxFallback),
		Blocks: []block{
			contextBlock(mrkdwn(c.Product, maxFieldText)),
			sectionBlock(title+"\n"+label, nil),
		},
	}

	if c.Summary != "" {
		msg.Blocks = append(msg.Blocks, sectionBlock(mrkdwn(c.Summary, maxSectionText), nil))
	}

	if len(c.Fields) > 0 {
		fields := make([]text, 0, len(c.Fields))
		for _, f := range c.Fields {
			fields = append(fields, mrkdwnText(field(f)))
		}

		msg.Blocks = append(
			msg.Blocks,
			block{Type: typeSection, Text: nil, Fields: fields, Elements: nil, Accessory: nil},
		)
	}

	for i, l := range c.Links {
		msg.Blocks = append(msg.Blocks, sectionBlock(mrkdwn(l.Title, maxLinkText), button{
			Type: "button", Text: text{Type: "plain_text", Text: "Open", Verbatim: false}, URL: l.URL,
			ActionID: fmt.Sprintf("open-%d", i+1),
		}))
	}

	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	// The entities of the text hold &, which the encoder of JSON writes as an
	// escape of six bytes by default. The text has no < and no >.
	enc.SetEscapeHTML(false)

	if err := enc.Encode(msg); err != nil {
		return chat.Request{}, fmt.Errorf("render Slack message: %w", err)
	}

	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(body) > chat.MaxRequestBytes {
		return chat.Request{}, fmt.Errorf("%w: the Slack message is larger than %d bytes", notify.ErrInvalid,
			chat.MaxRequestBytes)
	}

	return chat.Request{
		Method: http.MethodPost, PathSuffix: "", IdempotencyKey: c.NotificationID,
		ContentType: "application/json", Body: body,
	}, nil
}

// contextBlock returns a context block with one mrkdwn text.
func contextBlock(t string) block {
	return block{Type: "context", Text: nil, Fields: nil, Elements: []text{mrkdwnText(t)}, Accessory: nil}
}

// sectionBlock returns a section block with a mrkdwn text, and the accessory
// when it is not nil.
func sectionBlock(t string, accessory any) block {
	t0 := mrkdwnText(t)

	return block{
		Type:      typeSection,
		Text:      &t0,
		Fields:    nil,
		Elements:  nil,
		Accessory: accessory,
	}
}

// field returns the mrkdwn text of a field: the label in bold, and the value
// on the next line.
func field(f chat.Field) string {
	label := mrkdwn(f.Label, maxFieldText/fieldLabelShare)
	if f.Value == "" {
		return "*" + label + "*"
	}

	return "*" + label + "*\n" + mrkdwn(f.Value, maxFieldText-len(label)-fieldMarkup)
}

// severityStyle is the word and the emoji that show a severity.
type severityStyle struct {
	label string
	emoji string
}

// severityStyleOf returns the style of s. An unknown severity has the style of
// Info: Content.Validate refuses it before the renderer gets here.
func severityStyleOf(s notify.Severity) severityStyle {
	if s == notify.SeverityCritical {
		return severityStyle{label: "Critical", emoji: ":rotating_light:"}
	}

	if s == notify.SeverityWarning {
		return severityStyle{label: "Warning", emoji: ":warning:"}
	}

	return severityStyle{label: "Info", emoji: ":information_source:"}
}

// mrkdwn encodes s for mrkdwn text of at most limit bytes. It cuts s, and ends
// it with an ellipsis, when the encoded text does not fit. It cuts between
// characters, and so never inside an entity or a character.
func mrkdwn(s string, limit int) string { return encode(s, limit, encodeRune) }

// fallbackText encodes s like mrkdwn, and also breaks the characters that
// start an automatic link or mention. The top-level text of a message has no
// verbatim switch, and a phone shows it in a notification.
func fallbackText(s string, limit int) string { return encode(s, limit, encodeFallbackRune) }

// encode is mrkdwn with the encoder of one character as a parameter.
func encode(s string, limit int, encodeOne func(r rune, rs []rune, i int) string) string {
	rs := []rune(s)
	pieces := make([]string, len(rs))
	total := 0

	for i, r := range rs {
		pieces[i] = encodeOne(r, rs, i)
		total += len(pieces[i])
	}

	if total <= limit {
		return strings.Join(pieces, "")
	}

	var b strings.Builder

	for _, p := range pieces {
		if b.Len()+len(p) > limit-len(ellipsis) {
			break
		}

		b.WriteString(p)
	}

	b.WriteString(ellipsis)

	return b.String()
}

// encodeFallbackRune returns the form of rs[i] in the fallback text: the
// mrkdwn form, and a zero-width space after @, #, a dot and a colon, after
// their ideographic, fullwidth, halfwidth and small forms, after the one dot
// leader and after the fullwidth solidus, so that @here, #general,
// evil.example and https://evil.example are not links or mentions.
func encodeFallbackRune(r rune, rs []rune, i int) string {
	switch r {
	case '@', '#', '.', ':',
		'\u3002', // ideographic full stop
		'\uFF0E', // fullwidth full stop
		'\uFF61', // halfwidth ideographic full stop
		'\uFF20', // fullwidth commercial at
		'\uFF03', // fullwidth number sign
		'\uFE52', // small full stop
		'\u2024', // one dot leader
		'\uFE6B', // small commercial at
		'\uFE5F', // small number sign
		'\uFF1A', // fullwidth colon
		'\uFF0F': // fullwidth solidus
		return string(r) + zeroWidthSpace
	default:
		return encodeRune(r, rs, i)
	}
}

// encodeRune returns the mrkdwn form of rs[i].
func encodeRune(r rune, rs []rune, i int) string {
	switch r {
	case '&':
		return "&amp;"
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	case '*', '~', '`':
		return string(r) + zeroWidthSpace
	case '_':
		// An underscore inside a word is text in mrkdwn, as in a name such as
		// my_stack_name. At the edge of a word it starts italics.
		if i > 0 && i+1 < len(rs) && wordRune(rs[i-1]) && wordRune(rs[i+1]) {
			return "_"
		}

		return "_" + zeroWidthSpace
	default:
		if !utf8.ValidRune(r) {
			return "\ufffd"
		}

		return string(r)
	}
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
