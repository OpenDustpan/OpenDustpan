package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Errors that a Model returns. An adapter translates each error of its
// provider into one of them, and never returns the error text of the
// provider to a caller, because that text can hold a part of the request.
var (
	// ErrModelUnavailable reports a provider that did not answer, answered
	// with a server error, or is overloaded. The loop retries the turn with
	// a backoff.
	ErrModelUnavailable = errors.New("model provider is not available")
	// ErrModelRateLimited reports a provider that refused the request for a
	// rate or quota limit. A RateLimitError carries the wait that the
	// provider asked for. The loop retries the turn after that wait.
	ErrModelRateLimited = errors.New("model provider rate limit")
	// ErrModelAuth reports a provider that refused the credential of the
	// connection. The loop stops the run. An administrator must connect the
	// model again.
	ErrModelAuth = errors.New("model provider refused the credential")
	// ErrModelRequest reports a request that the provider refused as not
	// valid, for example an unknown model name. The loop stops the run.
	ErrModelRequest = errors.New("model provider refused the request")
	// ErrContextTooLong reports a request that is longer than the context
	// window of the model. The loop stops the run.
	ErrContextTooLong = errors.New("agent run is longer than the context window of the model")
	// ErrModelRefused reports a model that refused to answer. The loop
	// stops the run and shows the refusal to the person.
	ErrModelRefused = errors.New("model refused to answer")
)

// RateLimitError is an ErrModelRateLimited with the wait that the provider
// asked for. RetryAfter is 0 when the provider gave none.
type RateLimitError struct {
	RetryAfter time.Duration
}

// Error returns the message of ErrModelRateLimited.
func (e RateLimitError) Error() string { return ErrModelRateLimited.Error() }

// Unwrap returns ErrModelRateLimited.
func (RateLimitError) Unwrap() error { return ErrModelRateLimited }

// Retryable reports whether the loop retries a turn after err from a Model.
// A context error is not retryable: the run stops or another instance takes
// it over.
func Retryable(err error) bool {
	return errors.Is(err, ErrModelUnavailable) || errors.Is(err, ErrModelRateLimited)
}

// Model is the port to a model provider. An adapter for one provider, or for
// one family of compatible providers, implements it. The port is
// provider-neutral: it names no provider, no model and no price. Each adapter
// runs the conformance suite in package agenttest.
//
// Complete is one request and one whole answer, with no stream. A durable
// step records the whole answer, so a stream gives the loop nothing to
// record. The console shows progress by turn.
type Model interface {
	// Complete sends req and returns the answer of the model. It returns
	// the errors of this package for provider failures, and ctx.Err() when
	// ctx ends first. It never retries a request: the loop retries the turn
	// in a new durable attempt. It returns ErrInvalid for a request with no
	// message or a MaxOutputTokens of 0 or less, before any call.
	Complete(ctx context.Context, req ModelRequest) (ModelResponse, error)
	// Info returns the names of the provider and the model, for the status
	// API and the audit log. It holds no credential.
	Info() ModelInfo
}

// ModelInfo names the provider and the model of a connection. Provider is a
// name of the adapter, for example "openai-compatible". Model is the model
// name that the adapter sends to the provider.
type ModelInfo struct {
	Provider string
	Model    string
}

// Role is the author of a message.
type Role string

// The roles of a message. The system prompt is not a message: it is
// ModelRequest.System.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleTool is a message that holds tool results. An adapter maps it to
	// the form that its provider expects.
	RoleTool Role = "tool"
)

// PartKind is the kind of one part of a message.
type PartKind string

// The kinds of a part.
const (
	PartText       PartKind = "text"
	PartToolCall   PartKind = "tool-call"
	PartToolResult PartKind = "tool-result"
)

// Message is one message of a conversation.
type Message struct {
	Role  Role
	Parts []Part
}

// Part is one part of a message. Kind tells which field holds its content:
// Text for PartText, Call for PartToolCall and Result for PartToolResult.
type Part struct {
	Kind   PartKind
	Text   string
	Call   ToolCall
	Result ToolResult
}

// ToolCall is a call of a tool by the model. ID is the ID that the model
// gave the call, so a result can name it.
type ToolCall struct {
	ID        string
	Name      ToolName
	Arguments json.RawMessage
}

// ToolResult is the answer to one tool call. Content is JSON. IsError is
// true when the call failed. Content then holds the error message, in the
// strict style of the API errors, with no value from the failure.
type ToolResult struct {
	CallID  string
	Content json.RawMessage
	IsError bool
}

// ModelRequest is one call of the model. Tools are the tools that the model
// can call. MaxOutputTokens bounds the answer.
type ModelRequest struct {
	System          string
	Messages        []Message
	Tools           []ToolSpec
	MaxOutputTokens int64
}

// StopKind tells why the model ended its answer.
type StopKind string

// The kinds of the end of an answer.
const (
	// StopEnd: the answer is final. The run ends with StopAnswered.
	StopEnd StopKind = "end"
	// StopToolUse: the answer holds tool calls. The loop runs them and
	// starts the next turn.
	StopToolUse StopKind = "tool-use"
	// StopMaxTokens: the answer reached MaxOutputTokens. The loop starts
	// the next turn with the part of the answer that it has.
	StopMaxTokens StopKind = "max-tokens"
)

// ModelResponse is the answer of one call. Message has the role
// RoleAssistant. Usage holds the tokens that the provider reported for this
// call. Model is the model name that the provider reported, which can differ
// from ModelInfo.Model for an alias.
type ModelResponse struct {
	Message Message
	Stop    StopKind
	Usage   Usage
	Model   string
}
