// Package matrix is the chat.Renderer of Matrix. It turns a chat.Content into
// the JSON document that the client-server API takes for a message in a room,
// and it reads the answer of the homeserver. It does no input or output:
// chat.Sender sends the request.
//
// The URL of the sink is the room: https://<homeserver>/_matrix/client/v3/rooms/<room ID>,
// with the room ID percent-encoded (! is %21 and : is %3A). The renderer asks
// for PUT <URL>/send/m.room.message/<transaction ID>. The transaction ID is
// the notification ID, so a retry of the delivery does not post twice: the
// homeserver answers a repeat of a transaction with the first event. The sink
// has the access token of the sending account as its second secret item, and
// the sender adds it as the header "Authorization: Bearer <token>". The
// renderer declares that with AuthSecret and never sees the token.
//
// The message is an m.text event. Its body is plain text for a client that
// shows no HTML, and its formatted_body is a small HTML document. Every text
// of a Content is untrusted, so the renderer writes each one with these steps:
//
//   - in the HTML it replaces &, <, >, " and ' with their entities, so that a
//     name never makes a tag or an attribute;
//   - in both forms it puts a zero-width space after @, # and !, so that a
//     user ID, a room alias, a room ID or a keyword such as @room starts no
//     mention, pill or link;
//   - in both forms it puts a zero-width space after a colon before //, and
//     after the dot of a leading www, so that no automatic link starts;
//   - the event has an empty m.mentions object, so that the homeserver and the
//     clients notify no user and no room from the text;
//   - it cuts a text on a character boundary, never inside an entity.
//
// A URL goes only in the href of a link and, as plain text, in the body. The
// renderer percent-encodes each character in it that can end the attribute or
// start markup.
package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
)

// MaxBody is the largest request body that the renderer makes, in bytes. A
// Matrix event, with its signatures, has at most 64 KiB, and the body of the
// request is the content of the event.
const MaxBody = 32 * 1024

// Limits of the renderer, in bytes of an encoded text. Each text is in the
// body and in the formatted body, and a limit holds for both.
const (
	maxProduct   = 150
	maxTitle     = 400
	maxSummary   = 1500
	maxLabel     = 150
	maxValue     = 400
	maxLinkTitle = 200
	// ellipsis ends a text that the renderer cut.
	ellipsis = "…"
	// zeroWidthSpace breaks the tokens that a client turns into a mention or a link.
	zeroWidthSpace = "\u200b"
	// sendPath is the path after the URL of the room, before the transaction ID.
	sendPath = "/send/m.room.message/"
	// eventIDKey is the field of the answer that names the event.
	eventIDKey = "event_id"
)

// Renderer is the chat.Renderer of Matrix. The zero value is ready to use.
type Renderer struct{}

var (
	_ chat.Renderer     = Renderer{}
	_ chat.AuthRenderer = Renderer{}
)

// New returns the Renderer of Matrix.
func New() Renderer { return Renderer{} }

// Platform returns chat.Matrix.
func (Renderer) Platform() chat.Platform { return chat.Matrix }

// AuthSecret returns true: the client-server API needs the access token of an
// account. See chat.AuthRenderer.
func (Renderer) AuthSecret() bool { return true }

// CheckHost returns a *chat.HostRuleError, which matches notify.ErrInvalid,
// for a host that notify.ChatHost refuses. The operator hosts the homeserver,
// so any host that the egress rules accept is a host of the platform: a name or
// an address that is public, with an optional port, in lower case. A private
// homeserver needs a change of the egress allowlist first.
func (Renderer) CheckHost(host string) error {
	// Defense in depth: the sink service and the sender check the URL with the
	// egress rules, and the dial guard checks the address of each connection.
	// This check stays so that the platform rule holds without them.
	got, err := notify.ChatHost("https://" + host + "/")
	if err != nil || got != host {
		return &chat.HostRuleError{
			Rule: "The Matrix URL must use a public host name or address that the server may send to.",
		}
	}

	return nil
}

// Check reads the answer of the homeserver. A homeserver that took the event
// answers 200 with a JSON object that has the field event_id. Any other body
// with a 2xx status is a refusal, and the sender does not try it again, so an
// event that the homeserver maybe took is not posted twice. The error text
// never holds the body.
func (Renderer) Check(status int, body []byte) error {
	if status == http.StatusOK && hasEventID(body) {
		return nil
	}

	return fmt.Errorf("%w: the Matrix homeserver did not answer with an event ID", notify.ErrPermanent)
}

// hasEventID reports whether body is a JSON object with a non-empty string
// field event_id.
func hasEventID(body []byte) bool {
	var answer map[string]json.RawMessage
	if json.Unmarshal(body, &answer) != nil {
		return false
	}

	var id string

	return json.Unmarshal(answer[eventIDKey], &id) == nil && id != ""
}

// Render returns the request for c. See chat.Renderer. It also returns an
// error that matches notify.ErrInvalid for a notification ID with two dots in
// a row, which the path of the request cannot hold.
func (Renderer) Render(c chat.Content) (chat.Request, error) {
	if err := c.Validate(); err != nil {
		return chat.Request{}, fmt.Errorf("render Matrix message: %w", err)
	}

	if strings.Contains(c.NotificationID, "..") {
		return chat.Request{}, fmt.Errorf("%w: the notification ID has two dots in a row", notify.ErrInvalid)
	}

	// Drop links from the end until the body fits. The fixed parts always fit,
	// so the loop ends with a body.
	for shown := len(c.Links); shown >= 0; shown-- {
		body, err := encodeEvent(build(c, shown))
		if err != nil {
			return chat.Request{}, fmt.Errorf("render Matrix message: %w", err)
		}

		if len(body) <= MaxBody {
			return chat.Request{
				Method: http.MethodPut, PathSuffix: sendPath + c.NotificationID, IdempotencyKey: c.NotificationID,
				ContentType: "application/json", Body: body,
			}, nil
		}
	}

	return chat.Request{}, fmt.Errorf("%w: the Matrix message is larger than %d bytes", notify.ErrInvalid, MaxBody)
}

// encodeEvent returns the JSON of the content of an event.
func encodeEvent(content map[string]any) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	// The texts hold no < and no >, and & is text for Matrix.
	enc.SetEscapeHTML(false)

	if err := enc.Encode(content); err != nil {
		return nil, fmt.Errorf("encode the event: %w", err)
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// text is a text in the two forms of the event: plain text for the body and
// HTML for the formatted body.
type text struct {
	plain string
	html  string
}

// build returns the content of the event for c with the first shown links.
func build(c chat.Content, shown int) map[string]any {
	style := severityLabel(c.Severity)

	var (
		plain []string
		page  []string
	)

	add := func(p, h string) {
		plain = append(plain, p)
		page = append(page, h)
	}

	title := encodeLine(c.Title, maxTitle)
	add(style+": "+title.plain, "<b>"+style+": "+title.html+"</b>")

	product := encodeLine(c.Product, maxProduct)
	add(product.plain, "<i>"+product.html+"</i>")

	if c.Summary != "" {
		summary := encode(c.Summary, maxSummary)
		add("\n"+summary.plain, "<br/>"+lineBreaks(summary.html))
	}

	for _, f := range c.Fields {
		label := encodeLine(f.Label, maxLabel)
		if f.Value == "" {
			add(label.plain, "<b>"+label.html+"</b>")

			continue
		}

		value := encode(f.Value, maxValue)
		add(label.plain+": "+value.plain, "<b>"+label.html+"</b>: "+lineBreaks(value.html))
	}

	for _, l := range c.Links[:shown] {
		linkTitle := encodeLine(l.Title, maxLinkTitle)
		url := target(l.URL)
		add(linkTitle.plain+": "+url, `<a href="`+html.EscapeString(url)+`">`+linkTitle.html+"</a>")
	}

	if left := len(c.Links) - shown; left > 0 {
		note := fmt.Sprintf("%d more links not shown", left)
		add(note, note)
	}

	return map[string]any{
		"msgtype":        "m.text",
		"body":           strings.Join(plain, "\n"),
		"format":         "org.matrix.custom.html",
		"formatted_body": strings.Join(page, "<br/>"),
		// An empty object turns off the matching of the text against the
		// names and the keywords of the users, so no text notifies a user or a
		// room.
		"m.mentions": map[string]any{},
	}
}

// lineBreaks replaces each line feed of an HTML text with a break tag.
func lineBreaks(h string) string { return strings.ReplaceAll(h, "\n", "<br/>") }

// severityLabel returns the word that shows a severity.
func severityLabel(s notify.Severity) string {
	if s == notify.SeverityCritical {
		return "Critical"
	}

	if s == notify.SeverityWarning {
		return "Warning"
	}

	return "Info"
}

// target percent-encodes the characters of a URL that end an attribute or
// start markup, and each byte that is not printable ASCII. The URL stays
// valid, and no character of it can leave the attribute that holds it.
func target(raw string) string {
	var b strings.Builder

	for i := range len(raw) {
		ch := raw[i]
		if ch <= ' ' || ch >= 0x7f || strings.IndexByte("\"'<>\\^`{|}", ch) >= 0 {
			fmt.Fprintf(&b, "%%%02X", ch)

			continue
		}

		b.WriteByte(ch)
	}

	return b.String()
}

// encodeLine is encode for a text that stays on its line: a line feed becomes
// a space.
func encodeLine(s string, limit int) text {
	return encode(strings.ReplaceAll(s, "\n", " "), limit)
}

// encode encodes s in both forms of the event. Each form has at most limit
// bytes. It cuts s, and ends it with an ellipsis, when an encoded form does not
// fit. It cuts between characters, and so never inside an entity or a
// character.
func encode(s string, limit int) text {
	rs := []rune(s)
	pieces := make([]text, len(rs))
	sumPlain, sumHTML := 0, 0

	for i := range rs {
		pieces[i] = encodeRune(rs, i)
		sumPlain += len(pieces[i].plain)
		sumHTML += len(pieces[i].html)
	}

	if sumPlain <= limit && sumHTML <= limit {
		return joinPieces(pieces)
	}

	var cut []text

	p, h := 0, 0

	for _, piece := range pieces {
		if p+len(piece.plain) > limit-len(ellipsis) || h+len(piece.html) > limit-len(ellipsis) {
			break
		}

		p += len(piece.plain)
		h += len(piece.html)

		cut = append(cut, piece)
	}

	return joinPieces(append(cut, text{plain: ellipsis, html: ellipsis}))
}

// joinPieces joins the pieces of each form.
func joinPieces(pieces []text) text {
	var p, h strings.Builder

	for _, piece := range pieces {
		p.WriteString(piece.plain)
		h.WriteString(piece.html)
	}

	return text{plain: p.String(), html: h.String()}
}

// encodeRune returns the plain form and the HTML form of rs[i].
func encodeRune(rs []rune, i int) text {
	r := rs[i]

	switch {
	case !utf8.ValidRune(r):
		return text{plain: "\ufffd", html: "\ufffd"}
	case strings.ContainsRune("@#!", r):
		return text{plain: string(r) + zeroWidthSpace, html: string(r) + zeroWidthSpace}
	case r == ':' && i+1 < len(rs) && rs[i+1] == '/':
		return text{plain: ":" + zeroWidthSpace, html: ":" + zeroWidthSpace}
	case r == '.' && afterWWW(rs, i):
		return text{plain: "." + zeroWidthSpace, html: "." + zeroWidthSpace}
	default:
		return text{plain: string(r), html: html.EscapeString(string(r))}
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
