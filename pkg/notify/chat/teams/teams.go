// Package teams is the chat.Renderer of Microsoft Teams. It turns a
// chat.Content into the JSON document that a Workflows (Power Automate)
// webhook takes, and it reads the answer. The document is a message with one
// Adaptive Card. It does no input or output: chat.Sender posts the request.
//
// The card has a text block for the product name, a text block for the
// severity and the title, a text block for the summary, a fact set for the
// fields, and one Action.OpenUrl for each link. Every text of a Content is
// untrusted. Teams supports a subset of Markdown in a text block and in a
// fact, and it ignores HTML. Microsoft documents no escape character for
// that Markdown, so the renderer breaks each token with a zero-width space
// and does not rely on a backslash:
//
//   - a zero-width space on both sides of * and _ (an _ inside a word stays),
//     so that no text becomes bold or italic;
//   - a zero-width space after [ and ], so that [title](url) is no link;
//   - a zero-width space after < and @, so that <at>name</at> and @name start
//     no mention, and after : before //, so that no scheme starts a link;
//   - a zero-width space after the dot of a leading www, so that no automatic
//     link starts there;
//   - a zero-width space at the start of a line that starts with -, +, #, >
//     or a digit, so that no text becomes a list;
//   - a line feed becomes two line feeds, the line break of a text block.
//
// The card has no mention entity, so no text can mention a person or a
// channel. A URL goes only in the url field of an action. A text is cut on a
// character boundary, and never inside a zero-width space.
package teams

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
)

// MaxBody is the largest message that a Teams incoming webhook takes, in
// bytes: 28 KB.
const MaxBody = 28 * 1024

// Limits of the texts of the card, in bytes of encoded text. The fixed parts
// take at most about 11 KB before the JSON encoder doubles each quote, which
// leaves room for links inside MaxBody.
const (
	maxProduct     = 200
	maxTitle       = 600
	maxSummary     = 2000
	maxLabel       = 200
	maxValue       = 600
	maxActionTitle = 200
	// ellipsis ends a text that the renderer cut.
	ellipsis = "…"
	// zeroWidthSpace breaks the Markdown tokens of Teams.
	zeroWidthSpace = "\u200b"
	// The documented answers of a flow that took the request.
	statusAccepted = http.StatusAccepted
	// The card schema of the Adaptive Cards that Teams takes.
	cardSchema  = "https://adaptivecards.io/schemas/adaptive-card.json"
	cardVersion = "1.2"
	// contentTypeCard is the content type of an Adaptive Card attachment.
	contentTypeCard = "application/vnd.microsoft.card.adaptive"
	// The element and action types of the card.
	typeTextBlock = "TextBlock"
	typeFactSet   = "FactSet"
	typeOpenURL   = "Action.OpenUrl"
	// defaultPort is the one port that a Workflows URL names.
	defaultPort = ":443"
)

// hostSuffixes returns the domains that Microsoft lists for the trigger "When
// an HTTP request is received" of Power Automate, which the Workflows webhook
// of Teams uses: the commercial cloud, GCC, GCC High, DoD and the 21Vianet
// cloud of China. A webhook host has one or more labels before one of them.
func hostSuffixes() []string {
	return []string{
		".logic.azure.com",
		".api.powerplatform.com",
		".logic.azure.us",
		".api.gov.powerplatform.microsoft.us",
		".api.high.powerplatform.microsoft.us",
		".api.appsplatform.us",
		".logic.azure.cn",
		".api.powerplatform.partner.microsoftonline.cn",
	}
}

// Renderer is the chat.Renderer of Microsoft Teams. The zero value is ready
// to use.
type Renderer struct{}

var _ chat.Renderer = Renderer{}

// New returns the Renderer of Microsoft Teams.
func New() Renderer { return Renderer{} }

// Platform returns chat.Teams.
func (Renderer) Platform() chat.Platform { return chat.Teams }

// CheckHost returns a *chat.HostRuleError, which matches notify.ErrInvalid,
// for every host that is not a Workflows webhook host: one or more labels
// before one of the domains that Microsoft lists, in lower case, with no port
// or with the port 443, which the URLs of Workflows hold. The match is exact:
// another port, user information, a different case, a parent domain and a
// name that only ends in the same letters all fail.
func (Renderer) CheckHost(host string) error {
	// Defense in depth: the sink service and the sender check the URL with the
	// egress rules, and the dial guard checks the address of each connection.
	// This check stays so that the platform rule holds without them.
	got, err := notify.ChatHost("https://" + host + "/")
	if err == nil && got == host && webhookHost(strings.TrimSuffix(host, defaultPort)) {
		return nil
	}

	return &chat.HostRuleError{
		Rule: "The Teams URL must use the host of a Workflows webhook, for example " +
			"prod-00.westus.logic.azure.com.",
	}
}

// webhookHost reports whether host has at least one label before one of the
// domains of hostSuffixes.
func webhookHost(host string) bool {
	for _, suffix := range hostSuffixes() {
		if prefix, ok := strings.CutSuffix(host, suffix); ok && prefix != "" {
			return true
		}
	}

	return false
}

// Check reads the answer of the flow. A flow that took the request answers
// 202, or 200 when it has a response action. Microsoft documents neither the
// status nor a body, and the status says that the flow got the request, not
// that Teams showed the card. Any other 2xx status is a refusal, and the
// sender does not try it again, so a message that Teams maybe took is not
// posted twice. The body is never read, so the error text never holds it.
func (Renderer) Check(status int, _ []byte) error {
	if status == http.StatusOK || status == statusAccepted {
		return nil
	}

	return fmt.Errorf("%w: the Teams flow did not answer 200 or 202", notify.ErrPermanent)
}

// message is the body of the request.
type message struct {
	Type        string           `json:"type"`
	Attachments []map[string]any `json:"attachments"`
}

// card is the Adaptive Card.
type card struct {
	Schema  string    `json:"$schema"`
	Type    string    `json:"type"`
	Version string    `json:"version"`
	Body    []element `json:"body"`
	Actions []action  `json:"actions,omitempty"`
}

// element is a text block or a fact set. The fields that a type does not use
// stay empty and are left out of the JSON.
type element struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Wrap     bool   `json:"wrap,omitempty"`
	Weight   string `json:"weight,omitempty"`
	Size     string `json:"size,omitempty"`
	Color    string `json:"color,omitempty"`
	IsSubtle bool   `json:"isSubtle,omitempty"`
	Facts    []fact `json:"facts,omitempty"`
}

// fact is a title and a value of a fact set.
type fact struct {
	Title string `json:"title"`
	Value string `json:"value"`
}

// action is a button that opens a URL.
type action struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func textBlock(t, weight, size, color string, subtle bool) element {
	return element{
		Type: typeTextBlock, Text: t, Wrap: true, Weight: weight, Size: size, Color: color, IsSubtle: subtle,
		Facts: nil,
	}
}

// Render returns the request for c. See chat.Renderer.
func (Renderer) Render(c chat.Content) (chat.Request, error) {
	if err := c.Validate(); err != nil {
		return chat.Request{}, fmt.Errorf("render Teams message: %w", err)
	}

	// Drop links from the end until the body fits. The fixed parts always fit,
	// so the loop ends with a body.
	for shown := len(c.Links); shown >= 0; shown-- {
		body, err := encodeMessage(build(c, shown))
		if err != nil {
			return chat.Request{}, fmt.Errorf("render Teams message: %w", err)
		}

		if len(body) <= MaxBody {
			return chat.Request{
				Method: http.MethodPost, PathSuffix: "", IdempotencyKey: c.NotificationID,
				ContentType: "application/json", Body: body,
			}, nil
		}
	}

	return chat.Request{}, fmt.Errorf("%w: the Teams message is larger than %d bytes", notify.ErrInvalid, MaxBody)
}

// encodeMessage returns the JSON of m.
func encodeMessage(m message) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	// The texts hold no < and no >, and & is text for Teams.
	enc.SetEscapeHTML(false)

	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encode the message: %w", err)
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// build returns the message for c with the first shown links.
func build(c chat.Content, shown int) message {
	style := severityStyleOf(c.Severity)

	body := []element{
		textBlock(encode(c.Product, maxProduct), "", "small", "", true),
		textBlock(style.label+": "+encode(c.Title, maxTitle), "bolder", "large", style.color, false),
	}

	if c.Summary != "" {
		body = append(body, textBlock(encode(c.Summary, maxSummary), "", "", "", false))
	}

	if len(c.Fields) > 0 {
		facts := make([]fact, 0, len(c.Fields))
		for _, f := range c.Fields {
			facts = append(facts, fact{Title: encode(f.Label, maxLabel), Value: encode(f.Value, maxValue)})
		}

		body = append(body, element{
			Type: typeFactSet, Text: "", Wrap: false, Weight: "", Size: "", Color: "",
			IsSubtle: false, Facts: facts,
		})
	}

	actions := make([]action, 0, shown)
	for _, l := range c.Links[:shown] {
		actions = append(actions, action{Type: typeOpenURL, Title: encode(l.Title, maxActionTitle), URL: l.URL})
	}

	if left := len(c.Links) - shown; left > 0 {
		body = append(body, textBlock(fmt.Sprintf("%d more links not shown", left), "", "small", "", true))
	}

	return message{
		Type: "message",
		// A map writes the field names that Teams expects, contentType and
		// contentUrl, in a fixed order.
		Attachments: []map[string]any{
			{
				"contentType": contentTypeCard,
				"contentUrl":  nil,
				"content": card{
					Schema:  cardSchema,
					Type:    "AdaptiveCard",
					Version: cardVersion,
					Body:    body,
					Actions: actions,
				},
			},
		},
	}
}

// severityStyle is the word and the color that show a severity.
type severityStyle struct {
	label string
	color string
}

// severityStyleOf returns the style of s. An unknown severity has the style of
// Info: Content.Validate refuses it before the renderer gets here.
func severityStyleOf(s notify.Severity) severityStyle {
	if s == notify.SeverityCritical {
		return severityStyle{label: "Critical", color: "attention"}
	}

	if s == notify.SeverityWarning {
		return severityStyle{label: "Warning", color: "warning"}
	}

	return severityStyle{label: "Info", color: "accent"}
}

// encode encodes s for a text block of at most limit bytes. It cuts s, and
// ends it with an ellipsis, when the encoded text does not fit. It cuts
// between characters, and so never inside a character.
func encode(s string, limit int) string {
	rs := []rune(s)
	pieces := make([]string, len(rs))
	total := 0

	for i := range rs {
		pieces[i] = encodeRune(rs, i)
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

// encodeRune returns the form of rs[i] in a text block.
func encodeRune(rs []rune, i int) string {
	r := rs[i]

	switch r {
	case '\n':
		return "\n\n"
	case '*':
		return zeroWidthSpace + "*" + zeroWidthSpace
	case '_':
		// An underscore inside a word is text, as in my_stack_name.
		if i > 0 && i+1 < len(rs) && wordRune(rs[i-1]) && wordRune(rs[i+1]) {
			return "_"
		}

		return zeroWidthSpace + "_" + zeroWidthSpace
	default:
		return encodeOther(rs, i)
	}
}

// encodeOther returns the form of rs[i] for a character that is not a line
// feed, * or _.
func encodeOther(rs []rune, i int) string {
	r := rs[i]

	// A line that starts with a list or heading marker gets a zero-width space
	// in front, so that it is no list item.
	lead := ""
	if (i == 0 || rs[i-1] == '\n') && strings.ContainsRune("-+#>0123456789", r) {
		lead = zeroWidthSpace
	}

	switch {
	case strings.ContainsRune("[]<@", r):
		return lead + string(r) + zeroWidthSpace
	case r == ':' && i+1 < len(rs) && rs[i+1] == '/':
		return ":" + zeroWidthSpace
	case r == '.' && afterWWW(rs, i):
		return "." + zeroWidthSpace
	case !utf8.ValidRune(r):
		return "\ufffd"
	default:
		return lead + string(r)
	}
}

// afterWWW reports whether rs[i] is the dot of a www at the start of a word.
func afterWWW(rs []rune, i int) bool {
	const www = 3

	if i < www || !strings.EqualFold(string(rs[i-www:i]), "www") {
		return false
	}

	return i == www || !wordRune(rs[i-www-1])
}

func wordRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r >= 0x80
}
