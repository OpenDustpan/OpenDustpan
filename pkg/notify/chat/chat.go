// Package chat holds the ports of the chat sinks: the platform-neutral content
// of a chat message and the Renderer that one chat platform implements.
//
// A chat sink (notify.SinkChat) posts a message to the incoming webhook URL
// of a chat platform. One notify.Sender serves the kind for every platform. It owns
// everything that touches the network: the egress rules, the timeout, the
// host check against the target of the sink, the read limit and the
// classes of error. A Renderer does no input or output. It turns a Content
// into the body of one request, and it reads the answer of its platform. So an
// adapter for a platform cannot skip a layer of the outbound rules.
//
// This package has no adapter yet. The suite chattest.RunRenderer holds
// the cases that each Renderer must pass.
package chat

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/notify"
)

// Platform names a chat service. The wire value is the lower-case string.
// The chatops package takes this type later for its Request.Platform.
type Platform string

// The chat platforms, in the order of their adapters.
const (
	Slack      Platform = "slack"
	Teams      Platform = "teams"
	Mattermost Platform = "mattermost"
	Matrix     Platform = "matrix"
)

// Platforms returns every chat platform, in the order of their adapters.
func Platforms() []Platform {
	return []Platform{Slack, Teams, Mattermost, Matrix}
}

// Valid reports whether p is one of the chat platforms.
func (p Platform) Valid() bool {
	return slices.Contains(Platforms(), p)
}

// Bounds of a Content and of a request.
const (
	// MaxProductLen caps the product name of a Content, in bytes.
	MaxProductLen = 100
	// MaxTitleLen caps the title of a Content, in bytes.
	MaxTitleLen = 200
	// MaxFields caps the fields of a Content.
	MaxFields = 10
	// MaxFieldLen caps the label and the value of a field, in bytes each.
	MaxFieldLen = notify.MaxAttributeLen
	// MaxRequestBytes caps the body of one request for every platform. A
	// platform can have a lower limit, and its Renderer keeps to that.
	MaxRequestBytes = 64 * 1024
	// MaxIDLen caps the notification ID of a Content, in bytes.
	MaxIDLen = 100
	// MaxPathSuffixLen caps the path suffix of a Request, in bytes.
	MaxPathSuffixLen = 512
)

// Field is a label and a value in a message, for example "Namespace" and
// "shop". The label and the value are text of the viewer's language.
type Field struct {
	Label string
	Value string
}

// Content is one chat message, before the format of a platform. The sender
// builds it from a notification. The text is in the language of the
// installation, and Product is the product name of the installation, not a
// fixed name. Every string is untrusted text: a name of a unit or a
// namespace comes from a person, so a Renderer encodes each one for its
// platform, and a mention or a markup in it never works. Validate refuses
// a string that is not valid UTF-8 or that holds a control or bidirectional
// control character, so a Renderer never sees one.
//
// A Content holds no plan, no log, no value and no secret. A unit is named
// by its tool, its namespace and the name that the tool uses (a Field).
type Content struct {
	// NotificationID is the ID of the notification. A Renderer uses it as the
	// idempotency key of the request, so a retry does not post twice.
	NotificationID string
	Product        string
	Title          string
	Summary        string
	Severity       notify.Severity
	Fields         []Field
	// Links are the links of the notification. Each one is an http or https
	// URL. A message never carries a link that grants anything.
	Links []notify.Link
}

// Validate returns an error that matches notify.ErrInvalid for a Content
// with no product, no title, no valid notification ID or an unknown
// severity, for a text or a count above its bound, for a text that is not
// valid UTF-8 or holds a control or bidirectional control character (a line
// feed is the one allowed control character), and for a link that is not an
// http or https URL with a host and no user information.
func (c Content) Validate() error {
	if err := c.validateText(); err != nil {
		return err
	}

	if err := c.validateCharacters(); err != nil {
		return err
	}

	if len(c.Fields) > MaxFields || len(c.Links) > notify.MaxLinks {
		return fmt.Errorf("%w: a chat message has at most %d fields and %d links", notify.ErrInvalid, MaxFields,
			notify.MaxLinks)
	}

	for _, f := range c.Fields {
		if f.Label == "" || len(f.Label) > MaxFieldLen || len(f.Value) > MaxFieldLen {
			return fmt.Errorf("%w: a field has a label and a value of at most %d bytes", notify.ErrInvalid, MaxFieldLen)
		}
	}

	for _, l := range c.Links {
		if err := checkLink(l); err != nil {
			return err
		}
	}

	return nil
}

// validateText returns an error that matches notify.ErrInvalid for a
// product, a title, a summary or a severity that breaks its bound.
func (c Content) validateText() error {
	switch {
	case !validID(c.NotificationID):
		return fmt.Errorf("%w: a chat message needs a notification ID of 1 to %d safe characters", notify.ErrInvalid,
			MaxIDLen)
	case c.Product == "" || len(c.Product) > MaxProductLen:
		return fmt.Errorf("%w: a chat message needs a product name of 1 to %d bytes", notify.ErrInvalid, MaxProductLen)
	case c.Title == "" || len(c.Title) > MaxTitleLen:
		return fmt.Errorf("%w: a chat message needs a title of 1 to %d bytes", notify.ErrInvalid, MaxTitleLen)
	case len(c.Summary) > notify.MaxSummaryLen:
		return fmt.Errorf("%w: the summary of a chat message has at most %d bytes", notify.ErrInvalid,
			notify.MaxSummaryLen)
	case c.Severity < notify.SeverityInfo || c.Severity > notify.SeverityCritical:
		return fmt.Errorf("%w: a chat message needs a known severity", notify.ErrInvalid)
	default:
		return nil
	}
}

// validateCharacters returns an error that matches notify.ErrInvalid for a
// text of the Content that holds a character that a person cannot read as
// text. Defense in depth: the notification cleans its texts too; this check
// stays so that a Content from another source cannot carry a carriage
// return, a NUL or a right-to-left override to a Renderer.
func (c Content) validateCharacters() error {
	texts := make([]string, 0, 3+2*len(c.Fields)+len(c.Links))
	texts = append(texts, c.Product, c.Title, c.Summary)

	for _, f := range c.Fields {
		texts = append(texts, f.Label, f.Value)
	}

	for _, l := range c.Links {
		texts = append(texts, l.Title)
	}

	for _, s := range texts {
		if !utf8.ValidString(s) || strings.ContainsFunc(s, forbiddenRune) {
			return fmt.Errorf("%w: a chat message holds only valid text with no control character", notify.ErrInvalid)
		}
	}

	return nil
}

// forbiddenRune reports whether r is a control character other than a line
// feed, or a bidirectional control character.
func forbiddenRune(r rune) bool {
	switch {
	case r == '\n':
		return false
	case unicode.IsControl(r):
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	default:
		return false
	}
}

// validID reports whether id is a notification ID that is safe to use in a
// header and in a path: letters, digits, dots, dashes and underscores.
func validID(id string) bool {
	if id == "" || len(id) > MaxIDLen {
		return false
	}

	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return false
		}
	}

	return true
}

// checkLink returns an error that matches notify.ErrInvalid for a link that
// has no title, is too long, or is not an http or https URL with a host and
// no user information. Defense in depth: the notification checks its links
// too; this check stays so that a Content from another source cannot carry
// a link such as javascript: to a Renderer.
func checkLink(l notify.Link) error {
	if l.Title == "" || len(l.Title) > notify.MaxLinkTitleLen || len(l.URL) > notify.MaxLinkURLLen {
		return fmt.Errorf("%w: a link has a title and a URL within their bounds", notify.ErrInvalid)
	}

	u, err := url.Parse(l.URL)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return fmt.Errorf("%w: a link is an http or https URL with a host", notify.ErrInvalid)
	}

	return nil
}

// Request is one request to a chat platform. The sender adds the base URL
// and the timeout. It adds any authentication header from a named secret
// item too, so a Renderer never sees a secret and cannot set a header.
type Request struct {
	// Method is POST or PUT.
	Method string
	// PathSuffix is a relative path that the sender appends to the path of
	// the URL of the sink. It is empty or it starts with a slash. It has no
	// dot segment, no query, no fragment and no control character.
	PathSuffix string
	// IdempotencyKey is the notification ID. A platform that has a key for
	// this (for example a transaction ID) gets it here, so a retry of the
	// delivery does not post twice.
	IdempotencyKey string
	ContentType    string
	Body           []byte
}

// Renderer is the adapter of one chat platform. It is pure: it opens no
// connection, reads no clock and logs nothing. It is safe for concurrent
// use.
type Renderer interface {
	// Platform returns the chat platform that the renderer serves.
	Platform() Platform
	// CheckHost returns an error that matches notify.ErrInvalid for a host
	// that the platform does not use for its webhooks. A platform with one public
	// host accepts only that host. A platform that the operator hosts accepts any
	// host that the egress rules accept. The sender calls it for the target
	// of a sink when a person creates the sink, and again before each
	// request.
	CheckHost(host string) error
	// Render returns the request for c. It returns an error that matches
	// notify.ErrInvalid for a Content that Validate refuses, and nil for every
	// other Content. It encodes every string of c for the platform, so that a
	// mention, a markup or a link markup in a name does nothing. It cuts a text
	// that does not fit the limit of the platform on a character boundary, and
	// never inside an escape sequence. It puts a URL only in a field that the
	// platform takes as a URL, or percent-encodes the characters |, < and >.
	Render(c Content) (Request, error)
	// Check reads the answer of the platform to a request. The sender calls it
	// for an answer with a 2xx status, and the body is at most
	// egress.MaxBodyBytes. It returns nil when the platform took the message. It
	// returns an error that matches notify.ErrPermanent or notify.ErrTemporary
	// for a platform that answers 2xx with a refusal in the body. The error text
	// never holds the body.
	Check(status int, body []byte) error
}
