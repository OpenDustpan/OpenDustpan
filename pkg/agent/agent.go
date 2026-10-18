// Package agent defines the built-in infrastructure agent and the tools that
// it shares with outside agents: the run and its durable turns, the model
// port, the task-shaped tool catalog, the gate for secret values, the change
// modes and the limits of a run.
//
// A run acts as the person who started it. Each tool call checks the
// authorization of that person at the time of the call, so a run never has
// more access than its person has now. The package never holds a credential
// of the person. A run holds the subject and the ID of the credential that
// started it, and the Credentials port tells at each turn whether that
// credential is still valid.
//
// The agent never runs a shell or an arbitrary command. Its only effects on
// infrastructure are changes that it proposes through the Changes port, which
// run as jobs on the shared job model.
//
// The package is tool-neutral. It never imports a store adapter, a model
// provider client or a transport.
package agent

import (
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of the package and of its ports.
var (
	// ErrInvalid reports a request with an empty or bad field.
	ErrInvalid = errors.New("agent request is not valid")
	// ErrNotFound reports a run ID that the store does not know.
	ErrNotFound = errors.New("agent run not found")
	// ErrDisabled reports that the agent is off: no administrator connected
	// a model.
	ErrDisabled = errors.New("agent is off: no model is connected")
	// ErrCredentialEnded reports that the credential that started a run is
	// no longer valid: it expired, a person revoked it, or its session
	// ended. The run stops.
	ErrCredentialEnded = errors.New("credential that started the agent run is no longer valid")
	// ErrConcurrencyLimit reports a start when the person or the
	// installation already has the maximum number of active runs.
	ErrConcurrencyLimit = errors.New("too many active agent runs")
	// ErrCanceled reports a write to a run that a cancel stopped.
	ErrCanceled = errors.New("agent run is canceled")
	// ErrFinished reports a cancel of a run that already ended.
	ErrFinished = errors.New("agent run already ended")
	// ErrKeyConflict reports a start whose key names a run of another
	// person.
	ErrKeyConflict = errors.New("start key names an agent run of another person")
	// ErrTurnFailed is the LastError of a run that the reaper ended: the
	// durable runner failed its turn operation for good, and no step ended
	// the run.
	ErrTurnFailed = errors.New("the turn of the agent run failed too often")
	// ErrTurnActive reports a reap of a run whose latest turn operation did
	// not end.
	ErrTurnActive = errors.New("the turn of the agent run did not end")
)

// RunID identifies a run. The server makes it from a random source at the
// start of a run.
type RunID string

// State is the life-cycle state of a run. Succeeded, failed and canceled are
// terminal.
type State string

// The states of a run.
const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateCanceled  State = "canceled"
)

// Terminal reports whether s is an end state.
func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCanceled
}

// StopReason tells why a run ended. It is empty while the run is active.
type StopReason string

// The reasons why a run ends.
const (
	// StopAnswered: the model gave its final answer.
	StopAnswered StopReason = "answered"
	// StopTurnLimit, StopTokenLimit and StopTimeLimit: the run reached a
	// limit of its Limits.
	StopTurnLimit  StopReason = "turn-limit"
	StopTokenLimit StopReason = "token-limit"
	StopTimeLimit  StopReason = "time-limit"
	// StopCanceled: a person canceled the run.
	StopCanceled StopReason = "canceled"
	// StopCredentialEnded: the credential of the person is no longer valid.
	StopCredentialEnded StopReason = "credential-ended" //nolint:gosec // A stop reason, not a credential.
	// StopDisabled: an administrator disconnected the model during the run.
	StopDisabled StopReason = "disabled"
	// StopModelError: the model failed with a final error, or failed more
	// often than the retry limit allows.
	StopModelError StopReason = "model-error"
	// StopTurnFailed: the durable runner failed the turn operation for good,
	// at its failure limit or its expiry limit, and the reaper ended the
	// run.
	StopTurnFailed StopReason = "turn-failed"
)

// Channel names how a tool call reached the server. The audit log records
// it.
type Channel string

// The channels of a tool call.
const (
	// ChannelAgent is a call by a run of the built-in agent.
	ChannelAgent Channel = "agent"
	// ChannelMCP is a call by an outside agent through the MCP endpoint of
	// the server.
	ChannelMCP Channel = "mcp"
	// ChannelProxy is a call by an outside agent through the stdio proxy of
	// the command-line client, which forwards to the MCP endpoint.
	ChannelProxy Channel = "mcp-proxy"
)

// Valid reports whether c is one of the channels of this package.
func (c Channel) Valid() bool {
	return c == ChannelAgent || c == ChannelMCP || c == ChannelProxy
}

// Ref names the agent that acts for a person: the channel of the call,
// and the run (ChannelAgent) or the MCP session (ChannelMCP, ChannelProxy).
// The job model records it on each job, approval and policy input that an
// agent causes, and the audit log records it on each entry.
type Ref struct {
	Channel Channel
	// RunID names the run of a ChannelAgent call. It is empty for the other
	// channels.
	RunID RunID
	// SessionID names the MCP session of a ChannelMCP or ChannelProxy call.
	// It is empty for a ChannelAgent call.
	SessionID string
}

// Validate returns ErrInvalid unless r names a known channel and exactly the
// ID that its channel needs.
func (r Ref) Validate() error {
	switch {
	case !r.Channel.Valid():
		return fmt.Errorf("%w: unknown channel %q", ErrInvalid, r.Channel)
	case r.Channel == ChannelAgent && (r.RunID == "" || r.SessionID != ""):
		return fmt.Errorf("%w: a call of the built-in agent names its run and no session", ErrInvalid)
	case r.Channel != ChannelAgent && (r.SessionID == "" || r.RunID != ""):
		return fmt.Errorf("%w: a call of an outside agent names its session and no run", ErrInvalid)
	default:
		return nil
	}
}

// Actor is who makes a tool call. Principal is the person (or service
// principal) that asked, as the server resolved it for this call: every
// check uses its authorization. Agent is the agent that acts for it. The
// agent is never a principal: it holds no grant, no role and no token.
// BreakGlassSession is true when the credential of the call belongs to a
// break-glass session. It is not the break-glass of a change freeze: an
// agent never asks for that. ServicePrincipal is true for a principal that
// cannot sign in to the console, for example the principal of a service
// token: it can never pass a confirmation gate or approve a consent.
type Actor struct {
	Principal         identity.Principal
	Agent             Ref
	BreakGlassSession bool
	ServicePrincipal  bool
}

// Run is what any instance can read about a run.
type Run struct {
	ID RunID
	// Key is the start key. Two starts with one key and one subject give
	// one run, so a client can retry a start.
	Key string
	// Subject is the person who started the run. Every tool call of the
	// run acts as this subject.
	Subject string
	// CredentialID names the access token or the console session that
	// started the run. The run never holds the credential itself.
	CredentialID string
	// Namespace is the namespace that the person started the run from.
	// Tool calls can reach other namespaces, with the authorization of the
	// person in each one.
	Namespace namespace.Name
	// Parent is the run that this run continues, in one conversation, or
	// empty for the first run of a conversation.
	Parent RunID
	// Model is the model that the run uses, as ModelInfo.Model names it.
	Model string
	State State
	Stop  StopReason
	// LastError is the message of a failed run, in plain text, with no
	// value from a tool result.
	LastError string
	Limits    Limits
	// Progress is what the recorded turns used. Its Usage counts the model
	// calls whose answers the run recorded.
	Progress Progress
	// Ledger is the sum of the token ledger: every model call attempt,
	// also an attempt whose claim was lost before its record. It is the
	// number for the cost of the run, and it can be above Progress.Usage.
	Ledger Usage
	// ReadPlainSecrets is true after a tool call of the run answered secret
	// values in plain text. From then on the run cannot propose a change.
	ReadPlainSecrets bool
	// CancelRequested is true after a cancel of an active run.
	CancelRequested bool
	// CanceledBy is the subject that requested the cancel, or empty.
	CanceledBy string
	CreatedAt  time.Time
	EndedAt    time.Time
}
