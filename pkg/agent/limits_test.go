package agent_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/agent"
)

func TestDefaultLimitsAreValid(t *testing.T) {
	t.Parallel()

	if err := agent.DefaultLimits().Validate(); err != nil {
		t.Fatalf("DefaultLimits().Validate() = %v", err)
	}
}

func TestLimitsValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(l *agent.Limits)
	}{
		{name: "no turns", change: func(l *agent.Limits) { l.MaxTurns = 0 }},
		{name: "negative input tokens", change: func(l *agent.Limits) { l.MaxInputTokens = -1 }},
		{name: "no output tokens", change: func(l *agent.Limits) { l.MaxOutputTokens = 0 }},
		{name: "no duration", change: func(l *agent.Limits) { l.MaxDuration = 0 }},
		{name: "no tool calls", change: func(l *agent.Limits) { l.MaxToolCallsPerTurn = 0 }},
		{name: "no result bytes", change: func(l *agent.Limits) { l.MaxToolResultBytes = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := agent.DefaultLimits()
			tt.change(&l)

			if err := l.Validate(); !errors.Is(err, agent.ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestLimitsNarrow(t *testing.T) {
	t.Parallel()

	base := agent.DefaultLimits()

	got := base.Narrow(agent.Limits{
		MaxTurns:            5,
		MaxInputTokens:      base.MaxInputTokens * 2,
		MaxOutputTokens:     0,
		MaxDuration:         time.Minute,
		MaxToolCallsPerTurn: -3,
		MaxToolResultBytes:  0,
	})

	want := base
	want.MaxTurns = 5
	want.MaxDuration = time.Minute

	if got != want {
		t.Fatalf("Narrow() = %+v, want %+v: a lower limit applies, a higher, zero or negative one does not", got, want)
	}
}

func TestLimitsCheck(t *testing.T) {
	t.Parallel()

	l := agent.Limits{
		MaxTurns:            3,
		MaxInputTokens:      1000,
		MaxOutputTokens:     100,
		MaxDuration:         time.Minute,
		MaxToolCallsPerTurn: 2,
		MaxToolResultBytes:  1024,
	}

	tests := []struct {
		name     string
		progress agent.Progress
		want     error
		stop     agent.StopReason
	}{
		{"fresh run", progress(0, 0, 0, usage(0, 0, 0, 0)), nil, ""},
		{"turns", progress(3, 0, 0, usage(0, 0, 0, 0)), agent.ErrTurnLimit, agent.StopTurnLimit},
		{
			"input counts cached tokens",
			progress(0, 0, 0, usage(400, 0, 500, 100)),
			agent.ErrTokenLimit,
			agent.StopTokenLimit,
		},
		{"below the input limit", progress(0, 0, 0, usage(400, 0, 500, 99)), nil, ""},
		{"output tokens", progress(0, 0, 0, usage(0, 100, 0, 0)), agent.ErrTokenLimit, agent.StopTokenLimit},
		{"time", progress(0, 0, time.Minute, usage(0, 0, 0, 0)), agent.ErrTimeLimit, agent.StopTimeLimit},
		{
			"time comes first",
			progress(9, 0, 2*time.Minute, usage(0, 900, 0, 0)),
			agent.ErrTimeLimit,
			agent.StopTimeLimit,
		},
		{"tool calls do not stop a run", progress(0, 99, 0, usage(0, 0, 0, 0)), nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := l.Check(tt.progress)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Check() = %v, want %v", err, tt.want)
			}

			if tt.want == nil {
				return
			}

			if !errors.Is(err, agent.ErrLimit) {
				t.Fatalf("Check() = %v, want an error that wraps ErrLimit", err)
			}

			stop, ok := agent.StopReasonFor(err)
			if !ok || stop != tt.stop {
				t.Fatalf("StopReasonFor() = %q, %v, want %q, true", stop, ok, tt.stop)
			}
		})
	}
}

func TestStopReasonForOtherErrors(t *testing.T) {
	t.Parallel()

	for _, err := range []error{nil, agent.ErrToolCallLimit, agent.ErrCanceled} {
		if stop, ok := agent.StopReasonFor(err); ok {
			t.Fatalf("StopReasonFor(%v) = %q, true, want false", err, stop)
		}
	}
}

func TestUsageAdd(t *testing.T) {
	t.Parallel()

	got := usage(1, 2, 3, 4).Add(usage(10, 20, 30, 40))

	want := usage(11, 22, 33, 44)
	if got != want {
		t.Fatalf("Add() = %+v, want %+v", got, want)
	}

	if got.TotalInput() != 11+33+44 {
		t.Fatalf("TotalInput() = %d, want %d", got.TotalInput(), 11+33+44)
	}
}

func usage(in, out, cacheRead, cacheWrite int64) agent.Usage {
	return agent.Usage{InputTokens: in, OutputTokens: out, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite}
}

func progress(turns, toolCalls int, elapsed time.Duration, u agent.Usage) agent.Progress {
	return agent.Progress{Turns: turns, ToolCalls: toolCalls, Usage: u, Elapsed: elapsed}
}
