// Package agenttest holds the conformance suites for the ports of package
// agent:
//
//   - RunModel for each agent.Model adapter,
//   - RunChanges for the adapter of the shared job model,
//   - RunRunStore for each agent.RunStore adapter,
//   - RunToolset for each agent.Toolset,
//   - RunCredentials for the identity service,
//   - RunTurns for the turn steps on the durable runner,
//   - RunGateStores for the window, consent and settings stores,
//   - RunRevealSealer for each agent.RevealSealer.
//
// An adapter calls the suite from its own tests with a factory that gives
// it a fresh harness for each case.
package agenttest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
)

// Scenario is a scripted behavior of the fake provider behind a Model
// adapter. The adapter test runs a fake server of its provider, and makes it
// answer each scenario as the real provider does.
type Scenario string

// The scenarios of the model suite.
const (
	// ScenarioText: the provider answers a final text answer with usage
	// above 0 for input and output tokens.
	ScenarioText Scenario = "text"
	// ScenarioToolCall: the provider answers one call of the tool
	// describe_unit with the arguments {"unit":"pulumi:acme/web"}, and usage.
	ScenarioToolCall Scenario = "tool-call"
	// ScenarioRateLimited: the provider refuses for a rate limit and asks
	// for a wait of 7 seconds. The body of its error repeats the request.
	ScenarioRateLimited Scenario = "rate-limited"
	// ScenarioUnavailable: the provider answers a server error or an
	// overload. The body of its error repeats the request.
	ScenarioUnavailable Scenario = "unavailable"
	// ScenarioAuth: the provider refuses the credential.
	ScenarioAuth Scenario = "auth"
	// ScenarioBadRequest: the provider refuses the request as not valid, for
	// example for an unknown model. The body repeats the request.
	ScenarioBadRequest Scenario = "bad-request"
	// ScenarioTooLong: the provider refuses a request that is longer than
	// the context window.
	ScenarioTooLong Scenario = "too-long"
	// ScenarioRefusal: the provider answers that the model refused.
	ScenarioRefusal Scenario = "refusal"
	// ScenarioHang: the provider never answers.
	ScenarioHang Scenario = "hang"
)

// ModelFactory returns a Model whose fake provider plays s.
type ModelFactory func(t *testing.T, s Scenario) agent.Model

// requestCanary is in the prompt of each request. No error may hold it: an
// error text that holds a part of the request can carry a value of a tool
// result to a log.
const requestCanary = "canary-request-7f3a91"

// modelCase is one case of the model suite.
type modelCase struct {
	name     string
	scenario Scenario
	check    func(t *testing.T, resp agent.ModelResponse, err error)
}

// RunModel runs the model suite against the adapter that f makes.
func RunModel(t *testing.T, f ModelFactory) {
	t.Helper()

	for _, c := range modelCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			m := f(t, c.scenario)

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			if c.scenario == ScenarioHang {
				ctx, cancel = context.WithTimeout(t.Context(), 200*time.Millisecond)
			}
			defer cancel()

			resp, err := m.Complete(ctx, Request())
			if err != nil && strings.Contains(err.Error(), requestCanary) {
				t.Fatalf("Complete() error holds a part of the request: %v", err)
			}

			c.check(t, resp, err)
		})
	}

	t.Run("request with no message", func(t *testing.T) {
		t.Parallel()

		req := Request()
		req.Messages = nil

		if _, err := f(t, ScenarioText).Complete(t.Context(), req); !errors.Is(err, agent.ErrInvalid) {
			t.Fatalf("Complete() = %v, want ErrInvalid", err)
		}
	})

	t.Run("info names provider and model", func(t *testing.T) {
		t.Parallel()

		if info := f(t, ScenarioText).Info(); info.Provider == "" || info.Model == "" {
			t.Fatalf("Info() = %+v, want a provider and a model", info)
		}
	})
}

// Request returns the request that the model suite sends: one user message
// with the canary, and the tool catalog.
func Request() agent.ModelRequest {
	return agent.ModelRequest{
		System: "You are a test.",
		Messages: []agent.Message{{
			Role:  agent.RoleUser,
			Parts: []agent.Part{TextPart("Describe the unit pulumi:acme/web. " + requestCanary)},
		}},
		Tools:           agent.Catalog(),
		MaxOutputTokens: 256,
	}
}

// TextPart returns a text part.
func TextPart(text string) agent.Part {
	return agent.Part{
		Kind:   agent.PartText,
		Text:   text,
		Call:   agent.ToolCall{ID: "", Name: "", Arguments: nil},
		Result: agent.ToolResult{CallID: "", Content: nil, IsError: false},
	}
}

func modelCases() []modelCase {
	return []modelCase{
		{name: "text answer", scenario: ScenarioText, check: checkText},
		{name: "tool call", scenario: ScenarioToolCall, check: checkToolCall},
		{name: "rate limit", scenario: ScenarioRateLimited, check: checkRateLimit},
		{name: "unavailable", scenario: ScenarioUnavailable, check: wantRetryable(agent.ErrModelUnavailable)},
		{name: "credential refused", scenario: ScenarioAuth, check: wantFinal(agent.ErrModelAuth)},
		{name: "request refused", scenario: ScenarioBadRequest, check: wantFinal(agent.ErrModelRequest)},
		{name: "context too long", scenario: ScenarioTooLong, check: wantFinal(agent.ErrContextTooLong)},
		{name: "model refused", scenario: ScenarioRefusal, check: wantFinal(agent.ErrModelRefused)},
		{name: "context ends", scenario: ScenarioHang, check: checkHang},
	}
}

func checkText(t *testing.T, resp agent.ModelResponse, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("Complete() = %v", err)
	}

	if resp.Stop != agent.StopEnd || resp.Message.Role != agent.RoleAssistant {
		t.Fatalf(
			"Complete() stop %q role %q, want %q %q",
			resp.Stop,
			resp.Message.Role,
			agent.StopEnd,
			agent.RoleAssistant,
		)
	}

	if len(resp.Message.Parts) == 0 || resp.Message.Parts[0].Kind != agent.PartText ||
		resp.Message.Parts[0].Text == "" {
		t.Fatalf("Complete() parts = %+v, want a text part first", resp.Message.Parts)
	}

	checkUsage(t, resp)
}

func checkToolCall(t *testing.T, resp agent.ModelResponse, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("Complete() = %v", err)
	}

	if resp.Stop != agent.StopToolUse {
		t.Fatalf("Complete() stop = %q, want %q", resp.Stop, agent.StopToolUse)
	}

	for i := range resp.Message.Parts {
		p := &resp.Message.Parts[i]
		if p.Kind != agent.PartToolCall {
			continue
		}

		var args struct {
			Unit string `json:"unit"`
		}

		if p.Call.ID == "" || p.Call.Name != agent.ToolDescribeUnit || json.Unmarshal(p.Call.Arguments, &args) != nil ||
			args.Unit != "pulumi:acme/web" {
			t.Fatalf("Complete() call = %+v, want describe_unit with unit pulumi:acme/web and an ID", p.Call)
		}

		checkUsage(t, resp)

		return
	}

	t.Fatalf("Complete() parts = %+v, want a tool call", resp.Message.Parts)
}

func checkUsage(t *testing.T, resp agent.ModelResponse) {
	t.Helper()

	if resp.Usage.TotalInput() <= 0 || resp.Usage.OutputTokens <= 0 || resp.Model == "" {
		t.Fatalf("Complete() usage %+v model %q, want tokens above 0 and a model", resp.Usage, resp.Model)
	}
}

func checkRateLimit(t *testing.T, _ agent.ModelResponse, err error) {
	t.Helper()

	var rl agent.RateLimitError
	if !errors.As(err, &rl) || !errors.Is(err, agent.ErrModelRateLimited) || !agent.Retryable(err) {
		t.Fatalf("Complete() = %v, want a RateLimitError", err)
	}

	if rl.RetryAfter != 7*time.Second {
		t.Fatalf("RetryAfter = %v, want 7s", rl.RetryAfter)
	}
}

func checkHang(t *testing.T, _ agent.ModelResponse, err error) {
	t.Helper()

	if !errors.Is(err, context.DeadlineExceeded) || agent.Retryable(err) {
		t.Fatalf("Complete() = %v, want the error of the context", err)
	}
}

// wantRetryable returns a check for an error that wraps want and that the
// loop retries.
func wantRetryable(want error) func(t *testing.T, resp agent.ModelResponse, err error) {
	return func(t *testing.T, _ agent.ModelResponse, err error) {
		t.Helper()

		if !errors.Is(err, want) || !agent.Retryable(err) {
			t.Fatalf("Complete() = %v, want %v that the loop retries", err, want)
		}
	}
}

// wantFinal returns a check for an error that wraps want and that stops the
// run.
func wantFinal(want error) func(t *testing.T, resp agent.ModelResponse, err error) {
	return func(t *testing.T, _ agent.ModelResponse, err error) {
		t.Helper()

		if !errors.Is(err, want) || agent.Retryable(err) {
			t.Fatalf("Complete() = %v, want %v that stops the run", err, want)
		}
	}
}
