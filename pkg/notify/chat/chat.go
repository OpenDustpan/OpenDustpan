// Package chat holds the ports of the chat sinks: the tool-neutral content
// of a chat message and the Renderer that one chat tool implements.
//
// A chat sink (notify.SinkChat) posts a message to the incoming webhook URL
// of a chat tool. One notify.Sender serves the kind for every tool. It owns
// everything that touches the network: the egress rules, the timeout, the
// host check against the target of the sink, the read limit and the
// classes of error. A Renderer does no input or output. It turns a Content
// into the body of one request, and it reads the answer of its tool. So an
// adapter for a tool cannot skip a layer of the outbound rules.
//
// This package has no adapter yet. The suite chattest.RunRenderer holds
// the cases that each Renderer must pass.
package chat

import (
	"fmt"
	"net/url"
	"slices"

	"opendustpan.org/opendustpan/pkg/notify"
)

// Tool names a chat tool. The wire value is the lower-case string.
type Tool string

// The chat tools, in the order of their adapters.
const (
	Slack      Tool = "slack"
	Teams      Tool = "teams"
	Mattermost Tool = "mattermost"
	Matrix     Tool = "matrix"
)

// Tools returns every chat tool, in the order of their adapters.
func Tools() []Tool {
	return []Tool{Slack, Teams, Mattermost, Matrix}
}

// Valid reports whether t is one of the chat tools.
func (t Tool) Valid() bool {
	return slices.Contains(Tools(), t)
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
	// MaxRequestBytes caps the body of one request. A Renderer returns a body
	// within it for each valid Content, or an error that matches
	// notify.ErrInvalid.
	MaxRequestBytes = 64 * 1024
)

// Field is a label and a value in a message, for example "Namespace" and
// "shop". The label and the value are text of the viewer's language.
type Field struct {
	Label string
	Value string
}

// Content is one chat message, before the format of a tool. The sender
// builds it from a notification: the language of the text follows the
// localization of the server, and Product is the product name of the
// installation, not a fixed name. Every string is untrusted text: a name
// of a unit or a namespace comes from a person, so a Renderer encodes each
// one for its tool, and a mention or a markup in it never works.
//
// A Content holds no plan, no log, no value and no secret. A unit is named
// by its tool, its namespace and the name that the tool uses (a Field).
type Content struct {
	Product  string
	Title    string
	Summary  string
	Severity notify.Severity
	Fields   []Field
	// Links are the links of the notification. Each one is an http or https
	// URL. A message never carries a link that grants anything.
	Links []notify.Link
}

// Validate returns an error that matches notify.ErrInvalid for a Content
// with no product, no title or an unknown severity, for a text or a count
// above its bound, and for a link that is not an http or https URL with a
// host and no user information.
func (c Content) Validate() error {
	if err := c.validateText(); err != nil {
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

// Request is the body of one request to the webhook of a chat tool. The
// sender adds the method, the URL and the timeout.
type Request struct {
	ContentType string
	Body        []byte
}

// Renderer is the adapter of one chat tool. It is pure: it opens no
// connection, reads no clock and logs nothing. It is safe for concurrent
// use.
type Renderer interface {
	// Tool returns the chat tool that the renderer serves.
	Tool() Tool
	// CheckHost returns an error that matches notify.ErrInvalid for a host
	// that the tool does not use for its webhooks. A tool with one public
	// host accepts only that host. A tool that the operator hosts accepts any
	// host that the egress rules accept. The sender calls it for the target
	// of a sink when a person creates the sink, and again before each
	// request.
	CheckHost(host string) error
	// Render returns the request for c. It returns an error that matches
	// notify.ErrInvalid for a Content that Validate refuses, or that does not
	// fit MaxRequestBytes. It encodes every string of c for the tool, so that
	// a mention, a markup or a control character in a name does nothing.
	Render(c Content) (Request, error)
	// Check reads the answer of the tool to a request. The sender calls it
	// for an answer with a 2xx status, and the body is at most
	// egress.MaxBodyBytes. It returns nil when the tool took the message. It
	// returns an error that matches notify.ErrPermanent or notify.ErrTemporary
	// for a tool that answers 2xx with a refusal in the body. The error text
	// never holds the body.
	Check(status int, body []byte) error
}
