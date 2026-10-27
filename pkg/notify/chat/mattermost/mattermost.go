// Package mattermost is the chat.Renderer of Mattermost. It turns a
// chat.Content into the JSON document that an incoming webhook of Mattermost
// takes, and it reads the answer of Mattermost. It does no input or output:
// chat.Sender posts the request.
//
// The message is one markdown text: the product name, a heading with the
// severity and the title, the summary, the fields and the links. Every text
// of a Content is untrusted, so the renderer writes each one as markdown text
// with these steps:
//
//   - it replaces &, < and > with their entities, so HTML does nothing;
//   - it puts a backslash before each character that starts markdown: a
//     backslash, a backtick, *, _, brackets, parentheses, #, +, -, a dot, !,
//     a pipe, ~, =, $ and braces;
//   - it puts a zero-width space after @, #, ~ and a colon, so that a
//     mention such as @channel, a hashtag, a channel link such as ~town-square,
//     an emoji such as :smile: and a scheme such as https:// start nothing;
//   - it puts a zero-width space after the dot of a leading www, so that no
//     automatic link starts there;
//   - it replaces a space or a tab at the start of a line with a no-break
//     space, so that no text becomes a block of code;
//   - it cuts a text on a character boundary, never inside an escape.
//
// A URL goes only in the target of a link, and the renderer percent-encodes
// each character in it that can end the target or start markup. The title of
// a link is text and is encoded as above.
package mattermost

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

// Limits of the renderer, in bytes of encoded text. Mattermost limits a post
// to 16383 characters, and a byte is never less than a character, so a limit
// in bytes is a lower limit. The parts that always show (product, title,
// summary and fields) take at most about 11 KB, and the links take the rest.
const (
	maxProduct   = 200
	maxTitle     = 600
	maxSummary   = 2000
	maxLabel     = 200
	maxValue     = 600
	maxLinkTitle = 400
	maxText      = 15000
	// moreReserve is the room that the line about links that did not fit takes.
	moreReserve = 80
	// ellipsis ends a text that the renderer cut.
	ellipsis = "…"
	// zeroWidthSpace breaks the markup of mentions, hashtags and schemes.
	zeroWidthSpace = "\u200b"
	// noBreakSpace replaces a space at the start of a line.
	noBreakSpace = "\u00a0"
	// accepted is the body that Mattermost answers for a message that it took.
	accepted = "ok"
	// escaped lists the characters that get a backslash.
	escaped = "\\`*_[]()+-!|=${}"
)

// Renderer is the chat.Renderer of Mattermost. The zero value is ready to
// use.
type Renderer struct{}

var _ chat.Renderer = Renderer{}

// New returns the Renderer of Mattermost.
func New() Renderer { return Renderer{} }

// Platform returns chat.Mattermost.
func (Renderer) Platform() chat.Platform { return chat.Mattermost }

// CheckHost returns a *chat.HostRuleError, which matches notify.ErrInvalid,
// for a host that notify.ChatHost refuses. The operator hosts Mattermost, so
// any host that the egress rules accept is a host of the platform: a name or
// an address that is public, with an optional port, in lower case. A private
// server needs a change of the egress allowlist first.
func (Renderer) CheckHost(host string) error {
	// Defense in depth: the sink service and the sender check the URL with the
	// egress rules, and the dial guard checks the address of each connection.
	// This check stays so that the platform rule holds without them.
	got, err := notify.ChatHost("https://" + host + "/")
	if err != nil || got != host {
		return &chat.HostRuleError{
			Rule: "The Mattermost URL must use a public host name or address that the server may send to.",
		}
	}

	return nil
}

// Check reads the answer of Mattermost. Mattermost answers a message that it
// took with the text ok. Any other body with a 2xx status is a refusal, and
// the sender does not try it again, so a message that Mattermost maybe took
// is not posted twice. The error text never holds the body.
func (Renderer) Check(status int, body []byte) error {
	if status >= http.StatusOK && status < http.StatusMultipleChoices && string(bytes.TrimSpace(body)) == accepted {
		return nil
	}

	return fmt.Errorf("%w: Mattermost did not answer ok", notify.ErrPermanent)
}

// message is the body of the request.
type message struct {
	Text string `json:"text"`
}

// Render returns the request for c. See chat.Renderer.
func (Renderer) Render(c chat.Content) (chat.Request, error) {
	if err := c.Validate(); err != nil {
		return chat.Request{}, fmt.Errorf("render Mattermost message: %w", err)
	}

	style := severityStyleOf(c.Severity)

	sections := []string{
		"*" + line(c.Product, maxProduct) + "*",
		"#### " + style.emoji + " " + style.label + ": " + line(c.Title, maxTitle),
	}

	if c.Summary != "" {
		sections = append(sections, encode(c.Summary, maxSummary))
	}

	if len(c.Fields) > 0 {
		lines := make([]string, 0, len(c.Fields))
		for _, f := range c.Fields {
			lines = append(lines, field(f))
		}

		sections = append(sections, strings.Join(lines, "\n"))
	}

	text := strings.Join(sections, "\n\n")
	if links := linkSection(c.Links, maxText-len(text)-len("\n\n")-moreReserve); links != "" {
		text += "\n\n" + links
	}

	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	// The entities of the text hold &, which the encoder of JSON writes as an
	// escape of six bytes by default. The text has no < and no >.
	enc.SetEscapeHTML(false)

	if err := enc.Encode(message{Text: text}); err != nil {
		return chat.Request{}, fmt.Errorf("render Mattermost message: %w", err)
	}

	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(body) > chat.MaxRequestBytes {
		return chat.Request{}, fmt.Errorf("%w: the Mattermost message is larger than %d bytes", notify.ErrInvalid,
			chat.MaxRequestBytes)
	}

	return chat.Request{
		Method: http.MethodPost, PathSuffix: "", IdempotencyKey: c.NotificationID,
		ContentType: "application/json", Body: body,
	}, nil
}

// linkSection returns one line for each link that fits in budget bytes, and
// a line that counts the links that did not fit. The message always says
// that it left out links.
func linkSection(links []notify.Link, budget int) string {
	var lines []string

	used := 0
	left := 0

	for _, l := range links {
		entry := ":link: [" + line(l.Title, maxLinkTitle) + "](" + target(l.URL) + ")"
		if left > 0 || used+len(entry)+1 > budget {
			left++

			continue
		}

		used += len(entry) + 1

		lines = append(lines, entry)
	}

	if left > 0 {
		lines = append(lines, fmt.Sprintf("%d more links not shown", left))
	}

	return strings.Join(lines, "\n")
}

// field returns the line of a field: the label in bold, and the value after
// it.
func field(f chat.Field) string {
	label := line(f.Label, maxLabel)
	if f.Value == "" {
		return "**" + label + "**"
	}

	return "**" + label + "**: " + encode(f.Value, maxValue)
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

// target percent-encodes the characters of a URL that end the target of a
// link or start markup, and each byte that is not printable ASCII. The URL
// stays valid, and no character of it can leave the target of the link.
func target(raw string) string {
	var b strings.Builder

	for i := range len(raw) {
		ch := raw[i]
		if ch <= ' ' || ch >= 0x7f || strings.IndexByte("\"<>\\^`{|}()[]", ch) >= 0 {
			fmt.Fprintf(&b, "%%%02X", ch)

			continue
		}

		b.WriteByte(ch)
	}

	return b.String()
}

// line encodes s like encode, and first replaces each line feed with a space,
// so that a name stays on its line.
func line(s string, limit int) string {
	return encode(strings.ReplaceAll(s, "\n", " "), limit)
}

// encode encodes s for markdown text of at most limit bytes. It cuts s, and
// ends it with an ellipsis, when the encoded text does not fit. It cuts
// between characters, and so never inside an escape or a character.
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

// encodeRune returns the markdown form of rs[i].
func encodeRune(rs []rune, i int) string {
	r := rs[i]

	switch r {
	case '&':
		return "&amp;"
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	case '@', ':':
		return string(r) + zeroWidthSpace
	default:
		return encodeOther(rs, i)
	}
}

// encodeOther returns the markdown form of rs[i] for a character that is not
// an entity, a mention or a scheme character.
func encodeOther(rs []rune, i int) string {
	r := rs[i]

	switch {
	case r == '#' || r == '~':
		return `\` + string(r) + zeroWidthSpace
	case r == '.' && afterWWW(rs, i):
		return `\.` + zeroWidthSpace
	case (r == ' ' || r == '\t') && atLineStart(rs, i):
		return noBreakSpace
	case strings.ContainsRune(escaped, r) || r == '.':
		return `\` + string(r)
	case !utf8.ValidRune(r):
		return "\ufffd"
	default:
		return string(r)
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

// atLineStart reports whether only spaces and tabs come before rs[i] on its
// line.
func atLineStart(rs []rune, i int) bool {
	j := i - 1
	for j >= 0 && (rs[j] == ' ' || rs[j] == '\t') {
		j--
	}

	return j < 0 || rs[j] == '\n'
}
