package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
)

// The largest connection document the server accepts, before the JSON
// decoder reads it.
const maxConnectionBytes = 32 * 1024

// maxURLBytes bounds baseURL. It matches the URL bound of pkg/egress so the
// URL check and the document check agree.
const maxURLBytes = 2048

// The wire formats of a connection document of version 1.
const (
	formatChatCompletions = "chat-completions"
	formatMessages        = "messages"
	formatResponses       = "responses"
)

// jsonKeySecret is the JSON key of a secret reference in the wire form of
// auth, headers and query.
const jsonKeySecret = "secret"

// The bounds of the fields of a connection document.
const (
	// maxRequestIDHeaders is the largest count of requestIDHeaders.
	maxRequestIDHeaders = 4
	// maxHeadersEntries is the largest count of the entries of headers.
	maxHeadersEntries = 16
	// maxQueryEntries is the largest count of the entries of query.
	maxQueryEntries = 16
	// maxEntryValueBytes is the largest length of the plain value of one
	// header or query entry, in bytes.
	maxEntryValueBytes = 1024
	// maxReasoningKeep is the largest count of the entries of
	// reasoning.keep.
	maxReasoningKeep = 8
	// maxConversationKeyHeaders is the largest count of the headers of
	// conversationKey.
	maxConversationKeyHeaders = 4
	// maxConversationKeyPrefix is the largest length of the prefix of
	// conversationKey, in bytes.
	maxConversationKeyPrefix = 16
	// maxErrorsQuota is the largest count of the entries of errors.quota.
	maxErrorsQuota = 16
	// maxErrorsContextTooLong is the largest count of the entries of
	// errors.contextTooLong.
	maxErrorsContextTooLong = 16
	// maxRateLimitWaitHeaders is the largest count of the entries of
	// rateLimit.waitHeaders.
	maxRateLimitWaitHeaders = 8
	// maxUserAgentBytes is the largest length of userAgent, in bytes.
	maxUserAgentBytes = 200
	// firstPrintableRune is the first rune above the ASCII control
	// characters. A rune below it is a control character.
	firstPrintableRune = 0x20
	// lastPrintableASCII is the last byte of the printable ASCII range.
	lastPrintableASCII = 0x7e
)

// ErrFormatNotSupported reports a format the connection document names that
// no adapter implements yet.
var ErrFormatNotSupported = errors.New("agent: format not supported")

// ErrPlainSecret reports a header or query parameter that holds a plain
// value with a secret-like name.
var ErrPlainSecret = errors.New("agent: plain secret in header or query")

// ErrConnectionInvalid reports a connection document that fails validation.
var ErrConnectionInvalid = errors.New("agent: connection document invalid")

// SecretRef is a stable reference to a secret item of the scope @server. The
// form is itm- and then lowercase ASCII letters, digits, "-" and "_".
type SecretRef string

// IsSecretRef reports whether s has the form of a secret reference.
func IsSecretRef(s string) bool {
	return strings.HasPrefix(s, "itm-") && len(s) > 4 && secretRefOK(s[4:])
}

func secretRefOK(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}

	return true
}

// ConnectionSpec is one model connection of an installation. Every field is
// unexported: a value exists only after ParseConnectionSpec validated it,
// and the type is immutable.
//
// The wire form is one JSON object with version 1. Marshal writes the canonical form, which the store keeps.
type ConnectionSpec struct {
	// version is the wire version of the document. Only version 1 exists.
	version int
	// format is the wire format of each model call: chat-completions,
	// messages or responses.
	format string
	// baseURL is the absolute address before the path of the format.
	baseURL *url.URL
	// model is the model name the request sends, as is.
	model string
	// auth names the header, the prefix and the secret reference of the
	// credential, or none.
	auth authSpec
	// headers are the extra headers of each call.
	headers map[string]headerEntry
	// query are the extra query parameters of each call.
	query map[string]queryEntry
	// userAgent is the User-Agent header of each call. Empty takes the
	// default of the release.
	userAgent string
	// outputTokens is the field name and the limit of the output tokens of
	// each call.
	outputTokens outputTokensSpec
	// contextTokens is the context window of the model, or 0 for none.
	contextTokens int64
	// tools holds choice and parallel.
	tools toolsSpec
	// transport is whole or stream.
	transport string
	// reasoning holds the reasoning parts to keep.
	reasoning reasoningSpec
	// cache is the caching mode.
	cache cacheSpec
	// conversationKey holds where the conversation key goes.
	conversationKey conversationKeySpec
	// providerFields are extra fields of each request body.
	providerFields map[string]any
	// errors are the error types and codes for a quota and for a long
	// context.
	errors errorsSpec
	// rateLimit are the headers that hold a wait.
	rateLimit rateLimitSpec
	// requestIDHeaders are the headers that hold the request ID of the
	// server.
	requestIDHeaders []string
}

// authSpec is the authentication of a connection: a header, a prefix and a
// secret reference, or none.
type authSpec struct {
	// none reports whether the model takes no key.
	none bool
	// header is the header name of the credential.
	header string
	// prefix is the value before the secret value, for example "Bearer ".
	prefix string
	// secret is the reference of the secret item, or empty for none.
	secret SecretRef
}

// headerEntry is one extra header of each call.
type headerEntry struct {
	// value is the plain value, or nil when the entry holds a secret.
	value *string
	// secret is the reference of the secret item, or empty for a plain
	// value.
	secret SecretRef
}

// withCopiedValue returns a copy of the entry whose value pointer is its
// own. Defense in depth: a caller outside the package cannot reach the
// field, and this copy stays so that even a caller inside the package
// cannot change the value that the stored document holds through the map
// that Headers answers.
func (e headerEntry) withCopiedValue() headerEntry {
	if e.value == nil {
		return e
	}

	v := *e.value

	e.value = &v

	return e
}

// queryEntry is one extra query parameter of each call.
type queryEntry struct {
	// value is the plain value, or nil when the entry holds a secret.
	value *string
	// secret is the reference of the secret item, or empty for a plain
	// value.
	secret SecretRef
}

// withCopiedValue returns a copy of the entry whose value pointer is its
// own. Defense in depth: a caller outside the package cannot reach the
// field, and this copy stays so that even a caller inside the package
// cannot change the value that the stored document holds through the map
// that Query answers.
func (e queryEntry) withCopiedValue() queryEntry {
	if e.value == nil {
		return e
	}

	v := *e.value

	e.value = &v

	return e
}

// outputTokensSpec is the output token limit of each call.
type outputTokensSpec struct {
	// field is the body field of the limit, for example max_tokens.
	field string
	// max is the limit for each call, or 0 for none.
	max int64
}

// toolsSpec holds choice and parallel.
type toolsSpec struct {
	// choice is omit or auto.
	choice string
	// parallel is omit, on or off.
	parallel string
}

// reasoningSpec holds the reasoning parts to keep.
type reasoningSpec struct {
	// keep is the list of paths or block types to keep.
	keep []string
}

// cacheSpec is the caching mode.
type cacheSpec struct {
	// mode is none, automatic or breakpoints.
	mode string
	// ttl is empty or a duration such as 5m or 1h.
	ttl string
}

// conversationKeySpec holds where the conversation key goes.
type conversationKeySpec struct {
	// headers are up to 4 header names.
	headers []string
	// bodyField is one path in the request body, or empty.
	bodyField string
	// prefix is text before the key, up to 16 bytes.
	prefix string
}

// errorsSpec holds the error types and codes for a quota and for a long
// context.
type errorsSpec struct {
	// quota lists the types and codes that report a used-up plan.
	quota []string
	// contextTooLong lists the types and codes that report a long context.
	contextTooLong []string
}

// rateLimitSpec are the headers that hold a wait.
type rateLimitSpec struct {
	// waitHeaders lists up to 8 header names.
	waitHeaders []string
}

// ParseConnectionSpec parses and validates raw as a connection document. It
// refuses a document above maxConnectionBytes, an unknown field, a control
// character in a string, and the format responses.
func ParseConnectionSpec(raw []byte) (ConnectionSpec, error) {
	doc, err := decodeConnectionDoc(raw)
	if err != nil {
		return ConnectionSpec{}, err
	}

	c, err := newConnectionSpec(doc)
	if err != nil {
		return ConnectionSpec{}, err
	}

	if err := setBodyFields(&c, doc); err != nil {
		return ConnectionSpec{}, err
	}

	if err := setTailFields(&c, doc); err != nil {
		return ConnectionSpec{}, err
	}

	if err := c.validateNames(); err != nil {
		return ConnectionSpec{}, err
	}

	return c, nil
}

// decodeConnectionDoc checks the size and the text of raw and decodes it as
// one JSON document of version 1 with no unknown field and no control
// character.
func decodeConnectionDoc(raw []byte) (connectionDoc, error) {
	var doc connectionDoc

	if len(raw) > maxConnectionBytes {
		return doc, fmt.Errorf("%w: the document is larger than %d bytes", ErrConnectionInvalid, maxConnectionBytes)
	}

	if textbodyRefused(raw) {
		return doc, fmt.Errorf("%w: the document holds text that a JSON decode would change", ErrConnectionInvalid)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&doc); err != nil {
		return doc, fmt.Errorf("%w: the document does not parse: %w", ErrConnectionInvalid, err)
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return doc, fmt.Errorf("%w: the document holds more than one JSON value", ErrConnectionInvalid)
	}

	if err := checkControlChars(doc); err != nil {
		return doc, err
	}

	return doc, nil
}

// newConnectionSpec checks and copies the required fields of doc.
func newConnectionSpec(doc connectionDoc) (ConnectionSpec, error) {
	var c ConnectionSpec

	if doc.Version != 1 {
		return c, fmt.Errorf("%w: version must be 1", ErrConnectionInvalid)
	}

	switch doc.Format {
	case formatChatCompletions, formatMessages, formatResponses:
	default:
		return c, fmt.Errorf(
			"%w: the format %q is not valid: use chat-completions or messages",
			ErrConnectionInvalid,
			doc.Format,
		)
	}

	if doc.Format == formatResponses {
		return c, fmt.Errorf("%w: the format responses is not supported yet", ErrFormatNotSupported)
	}

	if doc.BaseURL == "" {
		return c, fmt.Errorf("%w: baseURL is required", ErrConnectionInvalid)
	}

	if err := checkBaseURL(doc.BaseURL, doc.Format); err != nil {
		return c, err
	}

	if doc.Model == "" {
		return c, fmt.Errorf("%w: model is required", ErrConnectionInvalid)
	}

	auth, err := parseAuth(doc.Auth)
	if err != nil {
		return c, err
	}

	c.version = 1
	c.format = doc.Format
	c.baseURL = mustParseURL(doc.BaseURL)
	c.model = doc.Model
	c.auth = auth

	return c, nil
}

// setBodyFields copies headers, query, userAgent, outputTokens,
// contextTokens, tools, transport and reasoning from doc.
func setBodyFields(c *ConnectionSpec, doc connectionDoc) error {
	if doc.Headers != nil {
		h, err := parseHeaders(doc.Headers)
		if err != nil {
			return err
		}

		c.headers = h
	}

	if doc.Query != nil {
		q, err := parseQuery(doc.Query)
		if err != nil {
			return err
		}

		c.query = q
	}

	if err := checkUserAgent(doc.UserAgent); err != nil {
		return err
	}

	c.userAgent = doc.UserAgent

	ot, err := parseOutputTokens(doc.OutputTokens)
	if err != nil {
		return err
	}

	c.outputTokens = ot
	c.contextTokens = doc.ContextTokens

	tools, err := parseTools(doc.Tools)
	if err != nil {
		return err
	}

	c.tools = tools

	switch doc.Transport {
	case "", "whole", "stream":
	default:
		return fmt.Errorf("%w: the transport %q is not valid: use whole or stream", ErrConnectionInvalid, doc.Transport)
	}

	c.transport = doc.Transport

	reasoning, err := parseReasoning(doc.Reasoning)
	if err != nil {
		return err
	}

	c.reasoning = reasoning

	return nil
}

// setTailFields copies cache, conversationKey, providerFields, errors,
// rateLimit and requestIDHeaders from doc.
func setTailFields(c *ConnectionSpec, doc connectionDoc) error {
	cache, err := parseCache(doc.Cache)
	if err != nil {
		return err
	}

	c.cache = cache

	ck, err := parseConversationKey(doc.ConversationKey)
	if err != nil {
		return err
	}

	c.conversationKey = ck

	pf, err := parseProviderFields(doc.ProviderFields)
	if err != nil {
		return err
	}

	c.providerFields = pf

	es, err := parseErrors(doc.Errors)
	if err != nil {
		return err
	}

	c.errors = es

	rl, err := parseRateLimit(doc.RateLimit)
	if err != nil {
		return err
	}

	c.rateLimit = rl

	if len(doc.RequestIDHeaders) > maxRequestIDHeaders {
		return fmt.Errorf("%w: requestIDHeaders holds at most %d entries", ErrConnectionInvalid, maxRequestIDHeaders)
	}

	if slices.Contains(doc.RequestIDHeaders, "") {
		return fmt.Errorf("%w: requestIDHeaders holds an empty name", ErrConnectionInvalid)
	}

	c.requestIDHeaders = slicesClone(doc.RequestIDHeaders)

	return nil
}

// textbodyRefused reports whether body holds text that a JSON decode would
// change: a byte that is not valid UTF-8, or a lone surrogate escape.
func textbodyRefused(body []byte) bool {
	return textbody.Refused(body)
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}

	return u
}

func slicesClone[S ~[]E, E any](s S) S {
	if s == nil {
		return nil
	}

	out := make(S, len(s))
	copy(out, s)

	return out
}

func checkBaseURL(raw, format string) error {
	if len(raw) > maxURLBytes {
		return fmt.Errorf("%w: baseURL is longer than %d bytes", ErrConnectionInvalid, maxURLBytes)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: baseURL does not parse: %w", ErrConnectionInvalid, err)
	}

	if err := checkBaseURLShape(u); err != nil {
		return err
	}

	return checkBaseURLPath(u.Path, format)
}

// checkBaseURLShape refuses a URL with a scheme other than https or http,
// user information, a query string or a fragment.
func checkBaseURLShape(u *url.URL) error {
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("%w: baseURL has the scheme %q: use https", ErrConnectionInvalid, u.Scheme)
	}

	if u.User != nil {
		return fmt.Errorf("%w: baseURL holds user information", ErrConnectionInvalid)
	}

	if u.RawQuery != "" {
		return fmt.Errorf("%w: baseURL holds a query string: put query parameters in query", ErrConnectionInvalid)
	}

	if u.Fragment != "" {
		return fmt.Errorf("%w: baseURL holds a fragment", ErrConnectionInvalid)
	}

	return nil
}

// formatBasePaths maps each format to the path of its calls. A baseURL must
// not end in that path: the caller gives the address before the path.
func formatBasePaths() map[string]string {
	return map[string]string{
		formatChatCompletions: "/chat/completions",
		formatMessages:        "/messages",
		formatResponses:       "/responses",
	}
}

// checkBaseURLPath refuses a path that already holds the path of the
// format.
func checkBaseURLPath(path, format string) error {
	p, ok := formatBasePaths()[format]
	if !ok {
		return nil
	}

	if path == "" {
		path = "/"
	}

	if strings.HasSuffix(path, p) {
		return fmt.Errorf("%w: baseURL ends in the path of the format %s", ErrConnectionInvalid, format)
	}

	return nil
}

// Marshal writes the canonical form of c.
func (c ConnectionSpec) Marshal() []byte {
	out, err := json.Marshal(c.marshalValue())
	if err != nil {
		panic(err)
	}

	return out
}

func (a authSpec) marshal() any {
	if a.none {
		return map[string]any{"none": true}
	}

	return map[string]any{
		"header":      a.header,
		"prefix":      a.prefix,
		jsonKeySecret: string(a.secret),
	}
}

func headersMarshal(m map[string]headerEntry) any {
	out := make(map[string]any, len(m))

	for k, e := range m {
		if e.value != nil {
			out[k] = map[string]any{"value": *e.value}
		} else {
			out[k] = map[string]any{jsonKeySecret: string(e.secret)}
		}
	}

	return out
}

func queryMarshal(m map[string]queryEntry) any {
	out := make(map[string]any, len(m))

	for k, e := range m {
		if e.value != nil {
			out[k] = map[string]any{"value": *e.value}
		} else {
			out[k] = map[string]any{jsonKeySecret: string(e.secret)}
		}
	}

	return out
}

func (o outputTokensSpec) marshal() any {
	return map[string]any{
		"field": o.field,
		"max":   o.max,
	}
}

func (t toolsSpec) marshal() any {
	return map[string]any{
		"choice":   t.choice,
		"parallel": t.parallel,
	}
}

func (r reasoningSpec) marshal() any {
	return map[string]any{"keep": r.keep}
}

func (c cacheSpec) marshal() any {
	return map[string]any{"mode": c.mode, "ttl": c.ttl}
}

func (k conversationKeySpec) marshal() any {
	return map[string]any{
		"headers":   k.headers,
		"bodyField": k.bodyField,
		"prefix":    k.prefix,
	}
}

func (e errorsSpec) marshal() any {
	return map[string]any{
		"quota":          e.quota,
		"contextTooLong": e.contextTooLong,
	}
}

func (r rateLimitSpec) marshal() any {
	return map[string]any{"waitHeaders": r.waitHeaders}
}

// Version returns the wire version of the document.
func (c ConnectionSpec) Version() int { return c.version }

// Format returns the wire format of each model call.
func (c ConnectionSpec) Format() string { return c.format }

// BaseURL returns the absolute address before the path of the format.
func (c ConnectionSpec) BaseURL() string { return c.baseURL.String() }

// Model returns the model name the request sends.
func (c ConnectionSpec) Model() string { return c.model }

// Auth returns the authentication of the connection.
func (c ConnectionSpec) Auth() authSpec { return c.auth }

// Headers returns the extra headers of each call. The map and the value of
// each entry are copies, so that no caller can change the document that the
// installation stores.
func (c ConnectionSpec) Headers() map[string]headerEntry {
	out := maps.Clone(c.headers)
	for name, entry := range out {
		out[name] = entry.withCopiedValue()
	}

	return out
}

// Query returns the extra query parameters of each call. The map and the
// value of each entry are copies, so that no caller can change the document
// that the installation stores.
func (c ConnectionSpec) Query() map[string]queryEntry {
	out := maps.Clone(c.query)
	for name, entry := range out {
		out[name] = entry.withCopiedValue()
	}

	return out
}

// UserAgent returns the User-Agent header of each call.
func (c ConnectionSpec) UserAgent() string { return c.userAgent }

// OutputTokens returns the output token limit of each call.
func (c ConnectionSpec) OutputTokens() outputTokensSpec { return c.outputTokens }

// ContextTokens returns the context window of the model, or 0 for none.
func (c ConnectionSpec) ContextTokens() int64 { return c.contextTokens }

// Tools returns the tool fields of each call.
func (c ConnectionSpec) Tools() toolsSpec { return c.tools }

// Transport returns whole or stream.
func (c ConnectionSpec) Transport() string { return c.transport }

// Reasoning returns the reasoning parts to keep.
func (c ConnectionSpec) Reasoning() reasoningSpec {
	return reasoningSpec{keep: slicesClone(c.reasoning.keep)}
}

// Cache returns the caching mode.
func (c ConnectionSpec) Cache() cacheSpec { return c.cache }

// ConversationKey returns where the conversation key goes.
func (c ConnectionSpec) ConversationKey() conversationKeySpec {
	k := c.conversationKey
	k.headers = slicesClone(k.headers)

	return k
}

// ProviderFields returns the extra fields of each request body.
func (c ConnectionSpec) ProviderFields() map[string]any {
	if c.providerFields == nil {
		return nil
	}

	out := make(map[string]any, len(c.providerFields))
	for k, v := range c.providerFields {
		out[k] = cloneJSONValue(v)
	}

	return out
}

// cloneJSONValue copies a decoded JSON value, so that a caller cannot change
// the value that a parsed document holds.
func cloneJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = cloneJSONValue(e)
		}

		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneJSONValue(e)
		}

		return out
	default:
		return v
	}
}

// Errors returns the error types and codes for a quota and for a long
// context.
func (c ConnectionSpec) Errors() errorsSpec {
	return errorsSpec{
		quota:          slicesClone(c.errors.quota),
		contextTooLong: slicesClone(c.errors.contextTooLong),
	}
}

// RateLimit returns the headers that hold a wait.
func (c ConnectionSpec) RateLimit() rateLimitSpec {
	return rateLimitSpec{waitHeaders: slicesClone(c.rateLimit.waitHeaders)}
}

// RequestIDHeaders returns the headers that hold the request ID of the
// server.
func (c ConnectionSpec) RequestIDHeaders() []string { return slicesClone(c.requestIDHeaders) }

// SecretRefs returns the secret references of c in sorted order, each one
// time: the secret of auth, of headers and of query. It returns
// ErrConnectionInvalid for a reference that does not have the form itm-....
func (c ConnectionSpec) SecretRefs() ([]SecretRef, error) {
	refs := make([]SecretRef, 0, 1+len(c.headers)+len(c.query))

	if c.auth.secret != "" {
		refs = append(refs, c.auth.secret)
	}

	for _, h := range c.headers {
		if h.secret != "" {
			refs = append(refs, h.secret)
		}
	}

	for _, q := range c.query {
		if q.secret != "" {
			refs = append(refs, q.secret)
		}
	}

	for _, r := range refs {
		// Defense in depth: the parse of auth checks the form too; this
		// check stays so that a reference of headers or query cannot reach
		// the store in another form.
		if !IsSecretRef(string(r)) {
			return nil, fmt.Errorf("%w: a secret reference is not valid", ErrConnectionInvalid)
		}
	}

	slices.Sort(refs)

	return slices.Compact(refs), nil
}

// marshalValue builds the canonical map that Marshal writes. The map holds
// each field that the document set; a field that the document did not set
// stays out.
func (c ConnectionSpec) marshalValue() any {
	v := map[string]any{
		"version": c.version,
		"format":  c.format,
		"baseURL": c.baseURL.String(),
		"model":   c.model,
		"auth":    c.auth.marshal(),
	}

	if c.headers != nil {
		v["headers"] = headersMarshal(c.headers)
	}

	if c.query != nil {
		v["query"] = queryMarshal(c.query)
	}

	if c.userAgent != "" {
		v["userAgent"] = c.userAgent
	}

	v["outputTokens"] = c.outputTokens.marshal()

	if c.contextTokens != 0 {
		v["contextTokens"] = c.contextTokens
	}

	v["tools"] = c.tools.marshal()

	if c.transport != "" {
		v["transport"] = c.transport
	}

	v["reasoning"] = c.reasoning.marshal()
	v["cache"] = c.cache.marshal()
	v["conversationKey"] = c.conversationKey.marshal()

	if c.providerFields != nil {
		v["providerFields"] = c.providerFields
	}

	v["errors"] = c.errors.marshal()
	v["rateLimit"] = c.rateLimit.marshal()

	if c.requestIDHeaders != nil {
		v["requestIDHeaders"] = c.requestIDHeaders
	}

	return v
}

// connectionDoc is the wire shape of a connection document. Its fields stay
// in the name order of the spec, and it carries every field of version 1.
type connectionDoc struct {
	Version          int                        `json:"version"`
	Format           string                     `json:"format"`
	BaseURL          string                     `json:"baseURL"`
	Model            string                     `json:"model"`
	Auth             json.RawMessage            `json:"auth"`
	Headers          map[string]json.RawMessage `json:"headers"`
	Query            map[string]json.RawMessage `json:"query"`
	UserAgent        string                     `json:"userAgent"`
	OutputTokens     json.RawMessage            `json:"outputTokens"`
	ContextTokens    int64                      `json:"contextTokens"`
	Tools            json.RawMessage            `json:"tools"`
	Transport        string                     `json:"transport"`
	Reasoning        json.RawMessage            `json:"reasoning"`
	Cache            json.RawMessage            `json:"cache"`
	ConversationKey  json.RawMessage            `json:"conversationKey"`
	ProviderFields   json.RawMessage            `json:"providerFields"`
	Errors           json.RawMessage            `json:"errors"`
	RateLimit        json.RawMessage            `json:"rateLimit"`
	RequestIDHeaders []string                   `json:"requestIDHeaders"`
}

func parseAuth(raw json.RawMessage) (authSpec, error) {
	var a authSpec

	if len(raw) == 0 {
		return a, fmt.Errorf("%w: auth is required", ErrConnectionInvalid)
	}

	var formed struct {
		None   bool   `json:"none"`
		Header string `json:"header"`
		Prefix string `json:"prefix"`
		Secret string `json:"secret"`
	}

	if err := decodeEntry(raw, &formed); err != nil {
		return a, fmt.Errorf("%w: auth does not parse: %w", ErrConnectionInvalid, err)
	}

	if formed.None {
		if formed.Header != "" || formed.Prefix != "" || formed.Secret != "" {
			return a, fmt.Errorf(
				"%w: auth.none cannot be set with header, prefix or secret",
				ErrConnectionInvalid,
			)
		}

		a.none = true

		return a, nil
	}

	if formed.Header == "" {
		return a, fmt.Errorf("%w: auth needs a header", ErrConnectionInvalid)
	}

	if !isHeaderName(formed.Header) {
		return a, fmt.Errorf(
			"%w: the header %q is not a token: use letters, digits and !#$%%&'*+-.^_`|~",
			ErrConnectionInvalid,
			formed.Header,
		)
	}

	if formed.Secret == "" {
		return a, fmt.Errorf("%w: auth needs a secret or none: true", ErrConnectionInvalid)
	}

	if !IsSecretRef(formed.Secret) {
		return a, fmt.Errorf(
			"%w: auth.secret is not a reference: store the value with POST /api/agent/secrets and send the reference",
			ErrPlainSecret,
		)
	}

	a.header = formed.Header
	a.prefix = formed.Prefix
	a.secret = SecretRef(formed.Secret)

	return a, nil
}

func parseHeaders(raw map[string]json.RawMessage) (map[string]headerEntry, error) {
	if len(raw) > maxHeadersEntries {
		return nil, fmt.Errorf("%w: headers holds more than %d entries", ErrConnectionInvalid, maxHeadersEntries)
	}

	out := make(map[string]headerEntry, len(raw))

	for name, value := range raw {
		if !isHeaderName(name) {
			return nil, fmt.Errorf("%w: the header name %q is not a token", ErrConnectionInvalid, name)
		}

		v, secret, err := headerQueryEntry(name, value, "header")
		if err != nil {
			return nil, err
		}

		out[name] = headerEntry{value: v, secret: secret}
	}

	return out, nil
}

func parseQuery(raw map[string]json.RawMessage) (map[string]queryEntry, error) {
	if len(raw) > maxQueryEntries {
		return nil, fmt.Errorf("%w: query holds more than %d entries", ErrConnectionInvalid, maxQueryEntries)
	}

	out := make(map[string]queryEntry, len(raw))

	for name, value := range raw {
		if !isQueryName(name) {
			return nil, fmt.Errorf(
				"%w: the query name %q holds letters, digits and -._~ only",
				ErrConnectionInvalid,
				name,
			)
		}

		v, secret, err := headerQueryEntry(name, value, "query")
		if err != nil {
			return nil, err
		}

		out[name] = queryEntry{value: v, secret: secret}
	}

	return out, nil
}

// decodeEntry decodes one entry of auth, headers or query with no unknown
// field.
func decodeEntry(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode entry: %w", err)
	}

	return nil
}

// headerQueryEntry parses the value of one entry of headers or of query: an
// object with a plain value, or with a secret reference when the name is
// secret-like. kind names the field in the error text.
func headerQueryEntry(name string, value json.RawMessage, kind string) (*string, SecretRef, error) {
	if isSecretLikeName(strings.ToLower(name)) {
		var entry struct {
			Secret string `json:"secret"`
		}

		if err := decodeEntry(value, &entry); err != nil || entry.Secret == "" {
			return nil, "", fmt.Errorf(
				"%w: the %s %q has a plain value: store the value with POST /api/agent/secrets and send the reference",
				ErrPlainSecret,
				kind,
				name,
			)
		}

		if !IsSecretRef(entry.Secret) {
			return nil, "", fmt.Errorf(
				"%w: the %s %q does not hold a secret reference: store the value with POST /api/agent/secrets and send the reference",
				ErrPlainSecret,
				kind,
				name,
			)
		}

		return nil, SecretRef(entry.Secret), nil
	}

	var plain struct {
		Value string `json:"value"`
	}

	if err := decodeEntry(value, &plain); err != nil || plain.Value == "" {
		return nil, "", fmt.Errorf("%w: the %s %q needs a value", ErrConnectionInvalid, kind, name)
	}

	if len(plain.Value) > maxEntryValueBytes {
		return nil, "", fmt.Errorf(
			"%w: the %s %q holds a value above %d bytes",
			ErrConnectionInvalid,
			kind,
			name,
			maxEntryValueBytes,
		)
	}

	return &plain.Value, "", nil
}

func parseOutputTokens(raw json.RawMessage) (outputTokensSpec, error) {
	var ot outputTokensSpec

	if len(raw) == 0 {
		return ot, fmt.Errorf("%w: outputTokens is required", ErrConnectionInvalid)
	}

	var formed struct {
		Field string `json:"field"`
		Max   int64  `json:"max"`
	}

	if err := json.Unmarshal(raw, &formed); err != nil {
		return ot, fmt.Errorf("%w: outputTokens does not parse: %w", ErrConnectionInvalid, err)
	}

	if formed.Field == "" {
		return ot, fmt.Errorf("%w: outputTokens needs a field", ErrConnectionInvalid)
	}

	ot.field = formed.Field
	ot.max = formed.Max

	return ot, nil
}

func parseTools(raw json.RawMessage) (toolsSpec, error) {
	var t toolsSpec

	if len(raw) == 0 {
		return t, fmt.Errorf("%w: tools is required", ErrConnectionInvalid)
	}

	var formed struct {
		Choice   string `json:"choice"`
		Parallel string `json:"parallel"`
	}

	if err := json.Unmarshal(raw, &formed); err != nil {
		return t, fmt.Errorf("%w: tools does not parse: %w", ErrConnectionInvalid, err)
	}

	switch formed.Choice {
	case "omit", "auto":
	default:
		return t, fmt.Errorf("%w: tools.choice must be omit or auto", ErrConnectionInvalid)
	}

	switch formed.Parallel {
	case "omit", "on", "off":
	default:
		return t, fmt.Errorf("%w: tools.parallel must be omit, on or off", ErrConnectionInvalid)
	}

	t.choice = formed.Choice
	t.parallel = formed.Parallel

	return t, nil
}

func parseReasoning(raw json.RawMessage) (reasoningSpec, error) {
	var r reasoningSpec

	if len(raw) == 0 {
		return r, nil
	}

	var formed struct {
		Keep []string `json:"keep"`
	}

	if err := json.Unmarshal(raw, &formed); err != nil {
		return r, fmt.Errorf("%w: reasoning does not parse: %w", ErrConnectionInvalid, err)
	}

	if len(formed.Keep) > maxReasoningKeep {
		return r, fmt.Errorf("%w: reasoning.keep holds more than %d entries", ErrConnectionInvalid, maxReasoningKeep)
	}

	r.keep = formed.Keep

	return r, nil
}

func parseCache(raw json.RawMessage) (cacheSpec, error) {
	var c cacheSpec

	if len(raw) == 0 {
		return c, nil
	}

	var formed struct {
		Mode string `json:"mode"`
		TTL  string `json:"ttl"`
	}

	if err := json.Unmarshal(raw, &formed); err != nil {
		return c, fmt.Errorf("%w: cache does not parse: %w", ErrConnectionInvalid, err)
	}

	switch formed.Mode {
	case "", "none", "automatic", "breakpoints":
	default:
		return c, fmt.Errorf("%w: cache.mode must be none, automatic or breakpoints", ErrConnectionInvalid)
	}

	c.mode = formed.Mode
	c.ttl = formed.TTL

	return c, nil
}

func parseConversationKey(raw json.RawMessage) (conversationKeySpec, error) {
	var ck conversationKeySpec

	if len(raw) == 0 {
		return ck, nil
	}

	var formed struct {
		Headers   []string `json:"headers"`
		BodyField string   `json:"bodyField"`
		Prefix    string   `json:"prefix"`
	}

	if err := json.Unmarshal(raw, &formed); err != nil {
		return ck, fmt.Errorf("%w: conversationKey does not parse: %w", ErrConnectionInvalid, err)
	}

	if len(formed.Headers) > maxConversationKeyHeaders {
		return ck, fmt.Errorf(
			"%w: conversationKey.headers holds more than %d entries",
			ErrConnectionInvalid,
			maxConversationKeyHeaders,
		)
	}

	ck.headers = formed.Headers
	ck.bodyField = formed.BodyField

	if len(formed.Prefix) > maxConversationKeyPrefix {
		return ck, fmt.Errorf(
			"%w: conversationKey.prefix is longer than %d bytes",
			ErrConnectionInvalid,
			maxConversationKeyPrefix,
		)
	}

	ck.prefix = formed.Prefix

	return ck, nil
}

func parseProviderFields(raw json.RawMessage) (map[string]any, error) {
	var pf map[string]any

	if len(raw) == 0 {
		return pf, nil
	}

	if err := json.Unmarshal(raw, &pf); err != nil {
		return nil, fmt.Errorf("%w: providerFields does not parse: %w", ErrConnectionInvalid, err)
	}

	if len(pf) > 8*1024 {
		return nil, fmt.Errorf("%w: providerFields is larger than 8 KiB", ErrConnectionInvalid)
	}

	return pf, nil
}

func parseErrors(raw json.RawMessage) (errorsSpec, error) {
	var es errorsSpec

	if len(raw) == 0 {
		return es, nil
	}

	var formed struct {
		Quota          []string `json:"quota"`
		ContextTooLong []string `json:"contextTooLong"`
	}

	if err := json.Unmarshal(raw, &formed); err != nil {
		return es, fmt.Errorf("%w: errors does not parse: %w", ErrConnectionInvalid, err)
	}

	if len(formed.Quota) > maxErrorsQuota {
		return es, fmt.Errorf("%w: errors.quota holds more than %d entries", ErrConnectionInvalid, maxErrorsQuota)
	}

	if len(formed.ContextTooLong) > maxErrorsContextTooLong {
		return es, fmt.Errorf(
			"%w: errors.contextTooLong holds more than %d entries",
			ErrConnectionInvalid,
			maxErrorsContextTooLong,
		)
	}

	es.quota = formed.Quota
	es.contextTooLong = formed.ContextTooLong

	return es, nil
}

func parseRateLimit(raw json.RawMessage) (rateLimitSpec, error) {
	var rl rateLimitSpec

	if len(raw) == 0 {
		return rl, nil
	}

	var formed struct {
		WaitHeaders []string `json:"waitHeaders"`
	}

	if err := json.Unmarshal(raw, &formed); err != nil {
		return rl, fmt.Errorf("%w: rateLimit does not parse: %w", ErrConnectionInvalid, err)
	}

	if len(formed.WaitHeaders) > maxRateLimitWaitHeaders {
		return rl, fmt.Errorf(
			"%w: rateLimit.waitHeaders holds more than %d entries",
			ErrConnectionInvalid,
			maxRateLimitWaitHeaders,
		)
	}

	rl.waitHeaders = formed.WaitHeaders

	return rl, nil
}

func (c ConnectionSpec) validateNames() error {
	// Header names of headers, query, auth and conversationKey must not
	// collide case-insensitively, and the header of auth must not be a
	// transport-owned name or User-Agent.
	seen := make(map[string]string)

	if err := addHeaderNames(seen, c.headers); err != nil {
		return err
	}

	if err := addQueryNames(seen, c.query); err != nil {
		return err
	}

	if err := addAuthName(seen, c.auth); err != nil {
		return err
	}

	return addConversationKeyNames(seen, c.conversationKey.headers)
}

func addHeaderNames(seen map[string]string, headers map[string]headerEntry) error {
	for name := range headers {
		if isForbiddenHeaderName(strings.ToLower(name)) {
			return fmt.Errorf("%w: the header %q is set by the transport", ErrConnectionInvalid, name)
		}

		if err := addSeenName(seen, name, "header"); err != nil {
			return err
		}
	}

	return nil
}

func addQueryNames(seen map[string]string, query map[string]queryEntry) error {
	for name := range query {
		if err := addSeenName(seen, name, "query"); err != nil {
			return err
		}
	}

	return nil
}

func addAuthName(seen map[string]string, auth authSpec) error {
	if auth.none {
		return nil
	}

	if isForbiddenHeaderName(strings.ToLower(auth.header)) {
		return fmt.Errorf("%w: auth.header %q is set by the transport", ErrConnectionInvalid, auth.header)
	}

	if other, ok := seen[strings.ToLower(auth.header)]; ok {
		return fmt.Errorf(
			"%w: the header of auth %q and %q differ only in case",
			ErrConnectionInvalid,
			auth.header,
			other,
		)
	}

	seen[strings.ToLower(auth.header)] = auth.header

	return nil
}

func addConversationKeyNames(seen map[string]string, headers []string) error {
	for _, h := range headers {
		if isForbiddenHeaderName(strings.ToLower(h)) {
			return fmt.Errorf(
				"%w: the conversationKey header %q is set by the transport",
				ErrConnectionInvalid,
				h,
			)
		}

		if err := addSeenName(seen, h, "header"); err != nil {
			return err
		}
	}

	return nil
}

func addSeenName(seen map[string]string, name, kind string) error {
	lower := strings.ToLower(name)

	other, ok := seen[lower]
	if !ok {
		seen[lower] = name

		return nil
	}

	return fmt.Errorf("%w: the %s %q and %q differ only in case", ErrConnectionInvalid, kind, name, other)
}

// isForbiddenHeaderName reports whether the lower-case header name is set by
// the transport or by the server, not by a connection document.
func isForbiddenHeaderName(lower string) bool {
	switch lower {
	case "host", "content-length", "content-type", "transfer-encoding",
		"connection", "te", "upgrade", "cookie", "user-agent":
		return true
	}

	return strings.HasPrefix(lower, "proxy-")
}

func isHeaderName(name string) bool {
	if name == "" {
		return false
	}

	for _, r := range name {
		if !isTokenChar(r) {
			return false
		}
	}

	return true
}

func isTokenChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}

	switch r {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}

	return false
}

func isQueryName(name string) bool {
	if name == "" {
		return false
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '.', r == '_', r == '~':
		default:
			return false
		}
	}

	return true
}

// controlField is one field name and value of the control character check.
type controlField struct {
	name  string
	value string
}

// checkControlChars refuses a document whose strings hold a control
// character. It checks the named fields and the raw fields.
func checkControlChars(doc connectionDoc) error {
	fields := make([]controlField, 0, 5+len(doc.Headers)+len(doc.Query)+len(doc.RequestIDHeaders))
	fields = append(fields,
		controlField{name: "model", value: doc.Model},
		controlField{name: "format", value: doc.Format},
		controlField{name: "baseURL", value: doc.BaseURL},
		controlField{name: "userAgent", value: doc.UserAgent},
		controlField{name: "transport", value: doc.Transport},
	)

	for name := range doc.Headers {
		fields = append(fields, controlField{name: "header name", value: name})
	}

	for name := range doc.Query {
		fields = append(fields, controlField{name: "query name", value: name})
	}

	for _, h := range doc.RequestIDHeaders {
		fields = append(fields, controlField{name: "requestIDHeader", value: h})
	}

	for _, f := range fields {
		if err := checkStringControl(f.value, f.name); err != nil {
			return err
		}
	}

	return checkRawControl(doc)
}

// checkRawControl walks the raw fields of doc: auth, headers, query,
// outputTokens, tools, reasoning, cache, conversationKey, providerFields,
// errors and rateLimit.
func checkRawControl(doc connectionDoc) error {
	raw := make([]json.RawMessage, 0, 9+len(doc.Headers)+len(doc.Query))
	raw = append(raw,
		doc.Auth, doc.OutputTokens, doc.Tools, doc.Reasoning, doc.Cache,
		doc.ConversationKey, doc.ProviderFields, doc.Errors, doc.RateLimit,
	)

	for _, fields := range doc.Headers {
		raw = append(raw, fields)
	}

	for _, fields := range doc.Query {
		raw = append(raw, fields)
	}

	for _, r := range raw {
		if err := walkRawForControl(r); err != nil {
			return err
		}
	}

	return nil
}

// checkUserAgent refuses a userAgent above maxUserAgentBytes or with a byte
// outside printable ASCII.
func checkUserAgent(v string) error {
	if len(v) > maxUserAgentBytes {
		return fmt.Errorf(
			"%w: userAgent is longer than %d bytes",
			ErrConnectionInvalid,
			maxUserAgentBytes,
		)
	}

	for i := range len(v) {
		if v[i] < firstPrintableRune || v[i] > lastPrintableASCII {
			return fmt.Errorf("%w: userAgent holds a byte that is not printable ASCII", ErrConnectionInvalid)
		}
	}

	return nil
}

// isControlRune reports whether r is an ASCII control character.
func isControlRune(r rune) bool {
	return r < firstPrintableRune
}

func checkStringControl(s, field string) error {
	if strings.ContainsFunc(s, isControlRune) {
		return fmt.Errorf("%w: the %s holds a control character", ErrConnectionInvalid, field)
	}

	return nil
}

func walkRawForControl(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Defense in depth: the document decode already read this field,
		// so it holds valid JSON. Fail closed when it does not, so that a
		// new caller cannot hand us a field that this check skips.
		return fmt.Errorf("%w: a field does not hold valid JSON", ErrConnectionInvalid)
	}

	return checkAnyControl(v)
}

func checkAnyControl(v any) error {
	switch x := v.(type) {
	case string:
		return checkStringControl(x, "string")
	case []any:
		return checkAnyControlSlice(x)
	case map[string]any:
		return checkAnyControlObject(x)
	default:
		return nil
	}
}

func checkAnyControlSlice(items []any) error {
	for _, e := range items {
		if err := checkAnyControl(e); err != nil {
			return err
		}
	}

	return nil
}

func checkAnyControlObject(obj map[string]any) error {
	for k, e := range obj {
		if err := checkStringControl(k, "string"); err != nil {
			return err
		}

		if err := checkAnyControl(e); err != nil {
			return err
		}
	}

	return nil
}

func isSecretLikeName(lower string) bool {
	for _, bad := range []string{"token", "key", "secret", "auth", "session", "cookie", "password", "signature"} {
		if strings.Contains(lower, bad) {
			return true
		}
	}

	return false
}

// ProbeRef is the stable ID of a capability probe.
type ProbeRef string

// maxProbeRefBytes is the largest length of a probe reference.
const maxProbeRefBytes = 64

// CurrentDocumentRef is the reference of the current document in the
// reference rows of the secret items.
const CurrentDocumentRef = "cur-1"

// Validate returns ErrInvalid unless p is 1 to 64 ASCII letters, digits,
// '-', '_' and '.', and does not start with '.' or '-'.
func (p ProbeRef) Validate() error {
	if !ident.Valid(string(p), maxProbeRefBytes) {
		return fmt.Errorf("%w: the probe reference is not valid", ErrInvalid)
	}

	return nil
}

// DocumentRef returns the reference of the pending document of p in the
// reference rows of the secret items.
func (p ProbeRef) DocumentRef() string {
	return "pnd-" + string(p)
}

// ConnectionStore is the persistence port for the stored connection of an
// installation. Current returns ErrDisabled when no document exists.
type ConnectionStore interface {
	// Current returns the connection document and its version, or
	// ErrDisabled when no document exists. The answer holds references
	// only: no secret value.
	Current(ctx context.Context) (ConnectionSpec, int64, error)
	// Pending returns the pending document for probe, or ErrNotFound when
	// none exists.
	Pending(ctx context.Context, probe ProbeRef) (ConnectionSpec, int64, error)
	// PutPending stores the document as the pending document for probe,
	// replacing an earlier one for the same probe.
	PutPending(ctx context.Context, probe ProbeRef, c ConnectionSpec, e audit.Entry) error
	// Promote replaces the current document by the pending document of
	// probe, in one transaction. It returns ErrNotFound when no pending
	// document exists.
	Promote(ctx context.Context, probe ProbeRef, e audit.Entry) error
	// Delete removes the current document, each pending document and the
	// reference rows in one transaction. It returns ErrDisabled when no
	// document exists.
	Delete(ctx context.Context, e audit.Entry) error
}
