package agent_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
)

func TestMaskText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		revealed []string
		want     string
	}{
		{"no value", "the password is hunter2", nil, "the password is hunter2"},
		{"one value", "the password is hunter2.", []string{"hunter2"}, "the password is [secret value]."},
		{"every copy", "hunter2 and hunter2", []string{"hunter2"}, "[secret value] and [secret value]"},
		{"longest first", "key abc123xyz", []string{"abc", "abc123xyz"}, "key [secret value]"},
		{"empty value masks nothing", "text", []string{""}, "text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := agent.MaskText(tt.in, tt.revealed); got != tt.want {
				t.Fatalf("MaskText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMaskJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		revealed []string
		want     string
	}{
		{"string value", `{"password":"p@ss\"w"}`, []string{`p@ss"w`}, `{"password":"[secret value]"}`},
		{
			"inside a string",
			`{"url":"https://u:s3cr3t@host"}`,
			[]string{"s3cr3t"},
			`{"url":"https://u:[secret value]@host"}`,
		},
		{"number", `{"pin":482913}`, []string{"482913"}, `{"pin":"[secret value]"}`},
		{"other number stays", `{"replicas":3}`, []string{"482913"}, `{"replicas":3}`},
		{"key", `{"s3cr3t":1}`, []string{"s3cr3t"}, `{"[secret value]":1}`},
		{"array", `["a","s3cr3t"]`, []string{"s3cr3t"}, `["a","[secret value]"]`},
		{"nothing to mask keeps the bytes", `{"a" : 1}`, nil, `{"a" : 1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := agent.MaskJSON(json.RawMessage(tt.in), tt.revealed)
			if err != nil || string(got) != tt.want {
				t.Fatalf("MaskJSON() = %s, %v, want %s", got, err, tt.want)
			}
		})
	}

	if _, err := agent.MaskJSON(json.RawMessage(`{"a":`), []string{"x"}); !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("MaskJSON() of a cut document = %v, want ErrInvalid", err)
	}
}

func TestMaskMessage(t *testing.T) {
	t.Parallel()

	const secret = "s3cr3t-value"

	m := agent.Message{Role: agent.RoleAssistant, Parts: []agent.Part{
		{Kind: agent.PartText, Text: "The value is " + secret, Call: noCall(), Result: noResult()},
		{
			Kind: agent.PartToolCall, Text: "",
			Call: agent.ToolCall{
				ID: "c1", Name: agent.ToolProposeChange,
				Arguments: json.RawMessage(`{"reason":"set ` + secret + `"}`),
			},
			Result: noResult(),
		},
	}}

	got, err := agent.MaskMessage(m, []string{secret})
	if err != nil {
		t.Fatalf("MaskMessage() = %v", err)
	}

	for _, p := range got.Parts {
		if strings.Contains(p.Text, secret) || strings.Contains(string(p.Call.Arguments), secret) {
			t.Fatalf("MaskMessage() kept the value in %+v", p)
		}
	}

	if !strings.Contains(m.Parts[0].Text, secret) {
		t.Fatal("MaskMessage() changed its input")
	}
}

func noCall() agent.ToolCall {
	var c agent.ToolCall

	return c
}

func noResult() agent.ToolResult {
	var r agent.ToolResult

	return r
}
