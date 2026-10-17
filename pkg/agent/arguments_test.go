package agent_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
)

func TestValidateArguments(t *testing.T) {
	t.Parallel()

	const unit = `"unit":"pulumi:acme/web"`

	tests := []struct {
		name string
		tool agent.ToolName
		args string
		ok   bool
	}{
		{"valid", agent.ToolDescribeUnit, `{` + unit + `}`, true},
		{"no arguments for a tool with none required", agent.ToolSearchUnits, ``, true},
		{"not an object", agent.ToolDescribeUnit, `["pulumi:acme/web"]`, false},
		{"null", agent.ToolSearchUnits, `null`, false},
		{"missing required", agent.ToolDescribeUnit, `{}`, false},
		{"unknown parameter", agent.ToolDescribeUnit, `{` + unit + `,"command":"ls"}`, false},
		{"wrong type", agent.ToolDescribeUnit, `{"unit":7}`, false},
		{
			"string at its maximum",
			agent.ToolSearchUnits,
			`{"query":"` + strings.Repeat("a", agent.MaxQueryBytes) + `"}`,
			true,
		},
		{
			"string above its maximum",
			agent.ToolSearchUnits,
			`{"query":"` + strings.Repeat("a", agent.MaxQueryBytes+1) + `"}`,
			false,
		},
		{"enum value", agent.ToolObserveUnit, `{` + unit + `,"what":"health"}`, true},
		{"value outside the enum", agent.ToolObserveUnit, `{` + unit + `,"what":"secrets"}`, false},
		{"limit at its maximum", agent.ToolListHistory, `{` + unit + `,"limit":100}`, true},
		{"limit above its maximum", agent.ToolListHistory, `{` + unit + `,"limit":101}`, false},
		{"limit 0", agent.ToolListHistory, `{` + unit + `,"limit":0}`, false},
		{"limit with a fraction", agent.ToolListHistory, `{` + unit + `,"limit":2.5}`, false},
		{"boolean", agent.ToolReadState, `{` + unit + `,"secretValues":true}`, true},
		{"boolean as a string", agent.ToolReadState, `{` + unit + `,"secretValues":"yes"}`, false},
		{
			"secret values with a comparison",
			agent.ToolReadState,
			`{` + unit + `,"compareTo":"3","secretValues":true}`,
			false,
		},
		{"comparison", agent.ToolReadState, `{` + unit + `,"compareTo":"3"}`, true},
		{
			"too many edits", agent.ToolProposeChange,
			`{` + unit + `,"operation":"apply","reason":"r","edits":[` + strings.Repeat(`{},`, agent.MaxEdits) + `{}]}`,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec, ok := agent.SpecFor(tt.tool)
			if !ok {
				t.Fatalf("SpecFor(%q) found no tool", tt.tool)
			}

			_, err := agent.ValidateArguments(spec, json.RawMessage(tt.args))
			if tt.ok && err != nil {
				t.Fatalf("ValidateArguments() = %v, want nil", err)
			}

			if !tt.ok && !errors.Is(err, agent.ErrInvalidArguments) {
				t.Fatalf("ValidateArguments() = %v, want ErrInvalidArguments", err)
			}
		})
	}
}

func TestValidateArgumentsHidesValues(t *testing.T) {
	t.Parallel()

	spec, _ := agent.SpecFor(agent.ToolObserveUnit)

	_, err := agent.ValidateArguments(spec, json.RawMessage(`{"unit":"pulumi:acme/web","what":"s3cr3t"}`))
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("ValidateArguments() = %v, want an error that holds no value", err)
	}
}

func TestArgumentsAccessors(t *testing.T) {
	t.Parallel()

	spec, _ := agent.SpecFor(agent.ToolListHistory)

	args, err := agent.ValidateArguments(spec, json.RawMessage(`{"unit":"pulumi:acme/web","limit":7}`))
	if err != nil {
		t.Fatalf("ValidateArguments() = %v", err)
	}

	if args.String("unit") != "pulumi:acme/web" || args.Int("limit", 1) != 7 || args.Int("other", 3) != 3 ||
		args.Bool("errorsOnly") {
		t.Fatalf("accessors of %v answer the wrong values", args)
	}
}

func TestArgsDigest(t *testing.T) {
	t.Parallel()

	a, errA := agent.ArgsDigest(json.RawMessage(`{"unit":"pulumi:acme/web","limit":5}`))
	b, errB := agent.ArgsDigest(json.RawMessage(` { "limit" : 5, "unit" : "pulumi:acme/web" } `))
	c, errC := agent.ArgsDigest(json.RawMessage(`{"unit":"pulumi:acme/web","limit":6}`))

	if errA != nil || errB != nil || errC != nil || a != b || a == c || len(a) != 64 {
		t.Fatalf(
			"ArgsDigest() = %q %q %q (%v %v %v), want one digest for one set of arguments",
			a,
			b,
			c,
			errA,
			errB,
			errC,
		)
	}

	if _, err := agent.ArgsDigest(json.RawMessage(`{`)); !errors.Is(err, agent.ErrInvalidArguments) {
		t.Fatalf("ArgsDigest() of a cut document = %v, want ErrInvalidArguments", err)
	}
}
