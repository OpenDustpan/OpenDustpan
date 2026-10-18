package agent

import (
	"errors"
	"fmt"
	"time"
)

// Errors of Limits.Check. Each one wraps ErrLimit.
var (
	// ErrLimit reports a run that reached one of its limits.
	ErrLimit = errors.New("agent run reached a limit")
	// ErrTurnLimit reports a run with as many turns as MaxTurns allows.
	ErrTurnLimit = fmt.Errorf("%w: turns", ErrLimit)
	// ErrTokenLimit reports a run whose input or output tokens reached
	// MaxInputTokens or MaxOutputTokens.
	ErrTokenLimit = fmt.Errorf("%w: tokens", ErrLimit)
	// ErrTimeLimit reports a run that is older than MaxDuration.
	ErrTimeLimit = fmt.Errorf("%w: time", ErrLimit)
	// ErrToolCallLimit reports a model answer with more tool calls than
	// MaxToolCallsPerTurn allows. The run does not stop: the loop answers
	// each call above the limit with an error result.
	ErrToolCallLimit = fmt.Errorf("%w: tool calls in one turn", ErrLimit)
)

// The default limits of a run. An administrator can lower or raise each one
// for the installation, a namespace administrator can lower it for a
// namespace, and a person can lower it for one run.
const (
	DefaultMaxTurns            = 25
	DefaultMaxInputTokens      = 400_000
	DefaultMaxOutputTokens     = 40_000
	DefaultMaxDuration         = 20 * time.Minute
	DefaultMaxToolCallsPerTurn = 8
	DefaultMaxToolResultBytes  = 64 << 10
	DefaultMaxActiveForSubject = 3
	DefaultMaxActive           = 20
)

// Limits bound one run. A turn is one model call and the tool calls of its
// answer. Input tokens count every input token that the provider reports,
// cached or not. MaxToolResultBytes bounds the result of one tool call that
// the loop sends to the model. The loop cuts a longer result and tells the
// model that it did. MaxActiveForSubject and MaxActive bound the runs that
// are not terminal, of the person and of the installation, at the create of
// a run. No limit is ever 0: Validate refuses it.
type Limits struct {
	MaxTurns            int
	MaxInputTokens      int64
	MaxOutputTokens     int64
	MaxDuration         time.Duration
	MaxToolCallsPerTurn int
	MaxToolResultBytes  int
	MaxActiveForSubject int
	MaxActive           int
}

// DefaultLimits returns the limits of an installation that sets none.
func DefaultLimits() Limits {
	return Limits{
		MaxTurns:            DefaultMaxTurns,
		MaxInputTokens:      DefaultMaxInputTokens,
		MaxOutputTokens:     DefaultMaxOutputTokens,
		MaxDuration:         DefaultMaxDuration,
		MaxToolCallsPerTurn: DefaultMaxToolCallsPerTurn,
		MaxToolResultBytes:  DefaultMaxToolResultBytes,
		MaxActiveForSubject: DefaultMaxActiveForSubject,
		MaxActive:           DefaultMaxActive,
	}
}

// Validate returns ErrInvalid when a limit is 0 or less. A run always has
// every limit: no value means "no limit".
func (l Limits) Validate() error {
	if l.MaxTurns <= 0 || l.MaxInputTokens <= 0 || l.MaxOutputTokens <= 0 || l.MaxDuration <= 0 ||
		l.MaxToolCallsPerTurn <= 0 || l.MaxToolResultBytes <= 0 || l.MaxActiveForSubject <= 0 || l.MaxActive <= 0 {
		return fmt.Errorf("%w: every limit must be above 0", ErrInvalid)
	}

	return nil
}

// Narrow returns l with each limit lowered to the limit of lower, where
// lower sets one. A field of lower that is 0 or less is not set and changes
// nothing: it never means "no limit". So a namespace or a person can lower a
// limit of the installation, and never raise or remove it.
func (l Limits) Narrow(lower Limits) Limits {
	return Limits{
		MaxTurns:            narrow(l.MaxTurns, lower.MaxTurns),
		MaxInputTokens:      narrow(l.MaxInputTokens, lower.MaxInputTokens),
		MaxOutputTokens:     narrow(l.MaxOutputTokens, lower.MaxOutputTokens),
		MaxDuration:         narrow(l.MaxDuration, lower.MaxDuration),
		MaxToolCallsPerTurn: narrow(l.MaxToolCallsPerTurn, lower.MaxToolCallsPerTurn),
		MaxToolResultBytes:  narrow(l.MaxToolResultBytes, lower.MaxToolResultBytes),
		MaxActiveForSubject: narrow(l.MaxActiveForSubject, lower.MaxActiveForSubject),
		MaxActive:           narrow(l.MaxActive, lower.MaxActive),
	}
}

func narrow[T int | int64 | time.Duration](limit, lower T) T {
	if lower > 0 && lower < limit {
		return lower
	}

	return limit
}

// Progress is what a run used so far.
type Progress struct {
	// Turns is the number of turns that started.
	Turns int
	// ToolCalls is the number of tool calls of all turns.
	ToolCalls int
	Usage     Usage
	// Elapsed is the time from the create of the run, on the clock of the
	// store.
	Elapsed time.Duration
}

// Check returns the error of the first limit that p reached, in the order
// time, tokens, turns, or nil. The loop calls it before each turn, so a run
// can pass a token limit by the tokens of its last turn, never by more.
func (l Limits) Check(p Progress) error {
	switch {
	case p.Elapsed >= l.MaxDuration:
		return ErrTimeLimit
	case p.Usage.TotalInput() >= l.MaxInputTokens || p.Usage.OutputTokens >= l.MaxOutputTokens:
		return ErrTokenLimit
	case p.Turns >= l.MaxTurns:
		return ErrTurnLimit
	default:
		return nil
	}
}

// StopReasonFor returns the stop reason of a limit error, and false for an
// error that is not a limit that stops a run.
func StopReasonFor(err error) (StopReason, bool) {
	switch {
	case errors.Is(err, ErrTimeLimit):
		return StopTimeLimit, true
	case errors.Is(err, ErrTokenLimit):
		return StopTokenLimit, true
	case errors.Is(err, ErrTurnLimit):
		return StopTurnLimit, true
	default:
		return "", false
	}
}

// Usage counts the tokens of one model call or of a whole run, as the
// provider reports them. The cache fields are 0 for a provider that reports
// no cache. The counts are what a cost per run needs: the operator applies
// the prices of the provider.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Add returns the sum of u and o.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + o.InputTokens,
		OutputTokens:     u.OutputTokens + o.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + o.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + o.CacheWriteTokens,
	}
}

// TotalInput returns every input token: uncached, read from a cache and
// written to a cache.
func (u Usage) TotalInput() int64 {
	return u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
}
