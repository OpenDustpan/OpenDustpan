// Package conformance holds the contract of the client conformance suite:
// the protocol between the harness and the adapter of one client, the ports
// of the harness, and the list of cases.
//
// The harness starts a real server and a recording proxy in front of it. It
// starts the adapter of one client as a child process and sends it requests
// as JSON-RPC 2.0 messages, one per line, on its standard input. The adapter
// calls the generated client and its hand-written transport, and answers on
// its standard output. The harness checks each answer and the requests that
// the proxy recorded. The same cases run for each client and each transport.
//
// The package holds no implementation yet. The cases have names and no body.
package conformance

import (
	"context"
	"encoding/json"
	"errors"
)

// The methods of the protocol.
const (
	// MethodInitialize starts a session. Params: InitializeParams. Result:
	// InitializeResult.
	MethodInitialize = "initialize"
	// MethodCall calls one operation once. Params: CallParams. Result:
	// CallResult.
	MethodCall = "call"
	// MethodPaginate calls a list operation and follows the cursor to the
	// last page. Params: PaginateParams. Result: PaginateResult.
	MethodPaginate = "paginate"
	// MethodShutdown ends the session. The adapter answers and then exits
	// with status 0.
	MethodShutdown = "shutdown"
)

// MaxMessageBytes is the largest message, in bytes, that each side reads.
// A longer line is an error, and the harness stops the adapter.
const MaxMessageBytes = 1 << 20

// ErrProtocol means that the adapter sent a message that is not valid JSON,
// is longer than MaxMessageBytes, has an unknown ID, or does not match the
// method.
var ErrProtocol = errors.New("conformance: adapter broke the protocol")

// ErrAdapterExited means that the adapter process exited before the end of
// the session.
var ErrAdapterExited = errors.New("conformance: adapter exited")

// InitializeParams are the parameters of MethodInitialize.
type InitializeParams struct {
	// BaseURL is the URL of the recording proxy, for example
	// http://127.0.0.1:41234.
	BaseURL string `json:"baseURL"`
	// Token is the access token for the operations that need a sign-in, or
	// empty to call as an anonymous caller.
	Token string `json:"token"`
	// Transport names the transport of the client, for example urllib or
	// httpx, or is empty for the default transport.
	Transport string `json:"transport"`
	// RetryLimit is the number of retries of the transport for this session.
	RetryLimit int `json:"retryLimit"`
}

// InitializeResult is the result of MethodInitialize.
type InitializeResult struct {
	// Client is the package name of the client, for example
	// opendustpan.org/client.
	Client string `json:"client"`
	// ClientVersion is the version of the client.
	ClientVersion string `json:"clientVersion"`
	// DescriptionVersion is the version of the description that the client
	// was generated from.
	DescriptionVersion string `json:"descriptionVersion"`
	// Transports lists the transports that the adapter can use.
	Transports []string `json:"transports"`
}

// CallParams are the parameters of MethodCall.
type CallParams struct {
	// Operation is the operation ID.
	Operation string `json:"operation"`
	// Path holds the path parameters by name.
	Path map[string]string `json:"path,omitempty"`
	// Query holds the query parameters by name.
	Query map[string]json.RawMessage `json:"query,omitempty"`
	// Body is the request body. The adapter decodes it into the typed
	// request of the client, so a field that the client does not know is
	// lost.
	Body json.RawMessage `json:"body,omitempty"`
	// IdempotencyKey is the key that the caller gives, or empty to let the
	// transport make one.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	// IfMatch is the ETag that the caller gives, or empty.
	IfMatch string `json:"ifMatch,omitempty"`
}

// CallResult is the result of MethodCall.
type CallResult struct {
	// Status is the HTTP status that the client saw.
	Status int `json:"status"`
	// ETag is the ETag that the client returned, or empty.
	ETag string `json:"etag,omitempty"`
	// Body is the typed answer of the client, encoded again as JSON. The
	// harness compares it with the answer of the server.
	Body json.RawMessage `json:"body,omitempty"`
	// Error is the error that the client returned, or nil.
	Error *ErrorResult `json:"error,omitempty"`
}

// ErrorResult is an error of the client, as the adapter reads it from the
// typed error of the client.
type ErrorResult struct {
	Code      int                        `json:"code"`
	Message   string                     `json:"message"`
	ErrorCode string                     `json:"errorCode,omitempty"`
	ErrorArgs map[string]json.RawMessage `json:"errorArgs,omitempty"`
	// Transport is true for an error with no answer from the server, for
	// example a refused connection or a time-out.
	Transport bool `json:"transport,omitempty"`
}

// PaginateParams are the parameters of MethodPaginate.
type PaginateParams struct {
	Operation string                     `json:"operation"`
	Path      map[string]string          `json:"path,omitempty"`
	Query     map[string]json.RawMessage `json:"query,omitempty"`
	// PageSize is the page size that the client sends, or 0 for the default
	// of the server.
	PageSize int `json:"pageSize,omitempty"`
}

// PaginateResult is the result of MethodPaginate.
type PaginateResult struct {
	// Items are the items of all pages, in order, each encoded again as
	// JSON.
	Items []json.RawMessage `json:"items"`
	// Error is the error that stopped the iteration, or nil.
	Error *ErrorResult `json:"error,omitempty"`
}

// Adapter is the harness side of one adapter process.
//
// Each method returns ErrProtocol or ErrAdapterExited, wrapped, when the
// session breaks, and the context error when the context ends first. An
// error of the client is not an error of the method. It is in the result.
type Adapter interface {
	Initialize(ctx context.Context, p InitializeParams) (InitializeResult, error)
	Call(ctx context.Context, p CallParams) (CallResult, error)
	Paginate(ctx context.Context, p PaginateParams) (PaginateResult, error)
	// Close sends MethodShutdown, waits for the exit, and stops the
	// process when it does not exit in time.
	Close() error
}

// FaultKind is the kind of a change that the proxy makes to one exchange.
type FaultKind int

// The kinds of a fault.
const (
	// FaultStatus answers a status, for example 503, in place of the
	// server, and does not send the request to the server.
	FaultStatus FaultKind = iota + 1
	// FaultDropAnswer sends the request to the server and closes the
	// connection before the answer, so the operation runs and the client
	// sees no answer.
	FaultDropAnswer
	// FaultAddField adds a field that the description does not declare to
	// the JSON object of the answer.
	FaultAddField
	// FaultEnumValue replaces the value of a field of the answer with a
	// value that the enum of the description does not list.
	FaultEnumValue
)

// Fault is one change that the proxy makes to the next exchange that
// matches the operation.
type Fault struct {
	Kind      FaultKind
	Operation string
	// Status is the status of FaultStatus.
	Status int
	// Field is a JSON pointer into the answer for FaultAddField and
	// FaultEnumValue.
	Field string
	// Value is the JSON value that FaultAddField or FaultEnumValue writes.
	Value json.RawMessage
}

// Exchange is one request that the proxy recorded, with its answer status.
type Exchange struct {
	Method string
	Path   string
	// RawQuery is the query string as the client sent it.
	RawQuery string
	// Headers holds the request headers that the suite checks:
	// Authorization (with the token replaced), Idempotency-Key, If-Match,
	// Content-Type and User-Agent.
	Headers map[string]string
	Body    []byte
	Status  int
}

// Proxy is the recording proxy between the client and the server.
type Proxy interface {
	// URL is the base URL that the adapter uses.
	URL() string
	// Inject queues a fault for the next exchange that matches it.
	Inject(f Fault)
	// Exchanges returns the exchanges since the last Reset, in order.
	Exchanges() []Exchange
	// Reset removes the recorded exchanges and the queued faults.
	Reset()
}

// Server is the real server under test.
type Server interface {
	// URL is the base URL of the server.
	URL() string
	// Token returns an access token that has every action.
	Token() string
	// Description returns the bytes that the server serves at its
	// description route.
	Description(ctx context.Context) ([]byte, error)
}

// Env is what a case gets.
type Env struct {
	Server  Server
	Proxy   Proxy
	Adapter Adapter
	// Transport is the transport of this run.
	Transport string
}

// Group is a group of cases.
type Group string

// The groups of cases.
const (
	GroupOperations    Group = "operations"
	GroupErrors        Group = "errors"
	GroupPagination    Group = "pagination"
	GroupIdempotency   Group = "idempotency"
	GroupConcurrency   Group = "concurrency"
	GroupCompatibility Group = "compatibility"
	GroupVersion       Group = "version"
)

// Case is one case of the suite.
type Case struct {
	// Group is one of the groups that Groups returns.
	Group Group
	// Name is unique in its group, in kebab-case.
	Name string
	// Run runs the case. It is nil until the case has a body.
	Run func(ctx context.Context, env Env) error
}

// Groups returns the groups of cases, in the order that the suite runs them.
func Groups() []Group {
	return []Group{
		GroupOperations,
		GroupErrors,
		GroupPagination,
		GroupIdempotency,
		GroupConcurrency,
		GroupCompatibility,
		GroupVersion,
	}
}

// Cases returns the cases of the suite. A case with a nil Run has no body
// yet, and the harness reports it as not implemented, which fails a release.
func Cases() []Case {
	return []Case{
		{Group: GroupOperations, Name: "every-operation-is-called", Run: nil},
		{Group: GroupOperations, Name: "public-operation-sends-no-token", Run: nil},
		{Group: GroupOperations, Name: "signed-in-operation-sends-bearer-token", Run: nil},
		{Group: GroupOperations, Name: "path-parameter-is-escaped", Run: nil},
		{Group: GroupOperations, Name: "query-parameter-is-encoded", Run: nil},
		{Group: GroupOperations, Name: "user-agent-names-client-and-version", Run: nil},
		{Group: GroupOperations, Name: "token-is-not-sent-after-redirect-to-another-host", Run: nil},
		{Group: GroupErrors, Name: "error-shape-is-decoded", Run: nil},
		{Group: GroupErrors, Name: "error-args-keep-their-json-types", Run: nil},
		{Group: GroupErrors, Name: "error-without-error-code", Run: nil},
		{Group: GroupErrors, Name: "error-body-that-is-not-json", Run: nil},
		{Group: GroupErrors, Name: "transport-error-is-not-a-server-error", Run: nil},
		{Group: GroupPagination, Name: "list-follows-next-page-token", Run: nil},
		{Group: GroupPagination, Name: "empty-next-page-token-ends-the-list", Run: nil},
		{Group: GroupPagination, Name: "page-size-is-sent", Run: nil},
		{Group: GroupPagination, Name: "error-on-a-later-page-stops-the-list", Run: nil},
		{Group: GroupIdempotency, Name: "mutating-call-sends-a-key", Run: nil},
		{Group: GroupIdempotency, Name: "generated-key-is-valid", Run: nil},
		{Group: GroupIdempotency, Name: "caller-key-is-sent-unchanged", Run: nil},
		{Group: GroupIdempotency, Name: "retry-after-503-sends-the-same-key", Run: nil},
		{Group: GroupIdempotency, Name: "retry-after-dropped-answer-gets-the-replay", Run: nil},
		{Group: GroupIdempotency, Name: "read-call-sends-no-key", Run: nil},
		{Group: GroupIdempotency, Name: "reused-key-error-is-decoded", Run: nil},
		{Group: GroupIdempotency, Name: "in-progress-error-is-decoded", Run: nil},
		{Group: GroupConcurrency, Name: "etag-is-returned", Run: nil},
		{Group: GroupConcurrency, Name: "if-match-is-sent", Run: nil},
		{Group: GroupConcurrency, Name: "stale-if-match-gives-412", Run: nil},
		{Group: GroupConcurrency, Name: "missing-required-if-match-gives-428", Run: nil},
		{Group: GroupCompatibility, Name: "unknown-field-is-ignored", Run: nil},
		{Group: GroupCompatibility, Name: "unknown-enum-value-is-kept", Run: nil},
		{Group: GroupCompatibility, Name: "merge-patch-null-clears-a-field", Run: nil},
		{Group: GroupCompatibility, Name: "absent-field-is-not-sent", Run: nil},
		{Group: GroupVersion, Name: "description-version-matches-the-server", Run: nil},
	}
}
