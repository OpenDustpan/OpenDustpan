package agent_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/pkg/identity"
)

// toolNameRE is the form of a tool name that every model provider and MCP
// client accepts.
var toolNameRE = regexp.MustCompile(`^[a-z][a-z_]{2,40}$`)

func TestCatalogSize(t *testing.T) {
	t.Parallel()

	// The catalog stays small and task-shaped. A tool for each API route
	// fills the context of the model and makes each choice harder.
	const minTools, maxTools = 8, 12
	if n := len(agent.Catalog()); n < minTools || n > maxTools {
		t.Fatalf("catalog has %d tools, want %d to %d", n, minTools, maxTools)
	}
}

func TestCatalogSpecs(t *testing.T) {
	t.Parallel()

	seen := map[agent.ToolName]bool{}

	for _, spec := range agent.Catalog() {
		t.Run(string(spec.Name), func(t *testing.T) {
			t.Parallel()
			checkSpec(t, spec)
		})

		if seen[spec.Name] {
			t.Errorf("tool %q is in the catalog twice", spec.Name)
		}

		seen[spec.Name] = true
	}
}

func checkSpec(t *testing.T, spec agent.ToolSpec) {
	t.Helper()

	if !toolNameRE.MatchString(string(spec.Name)) {
		t.Errorf("name %q does not match %s", spec.Name, toolNameRE)
	}

	if spec.Description == "" || (spec.Action == "") != spec.StartActions {
		t.Errorf("tool %q needs a description, and an action unless the job model gives it", spec.Name)
	}

	if spec.StartActions && spec.Effect != agent.EffectPropose {
		t.Errorf("tool %q takes the actions of a start but does not propose", spec.Name)
	}

	if spec.Action != "" && !strings.Contains(string(spec.Action), ":") {
		t.Errorf("action %q is not in the form <family>:<verb>", spec.Action)
	}

	checkEffect(t, spec)
	checkParams(t, spec)
}

func checkEffect(t *testing.T, spec agent.ToolSpec) {
	t.Helper()

	if spec.Effect != agent.EffectRead && spec.Object != agent.ObjectUnit && spec.Object != agent.ObjectChangeUnit {
		t.Errorf("tool %q has an effect on %q: a change applies to one unit", spec.Name, spec.Object)
	}

	if spec.SecretAction != "" && spec.SecretAction != identity.ActionUnitReadSecrets {
		t.Errorf("tool %q has secret action %q, want %q", spec.Name, spec.SecretAction, identity.ActionUnitReadSecrets)
	}
}

func checkParams(t *testing.T, spec agent.ToolSpec) {
	t.Helper()

	names := map[string]bool{}

	for _, p := range spec.Params {
		checkParam(t, spec.Name, p)

		if names[p.Name] {
			t.Errorf("tool %q has the parameter %q twice", spec.Name, p.Name)
		}

		names[p.Name] = true
	}

	// The object of the action must come from a required parameter, so the
	// server can check the action before it does anything.
	want := map[agent.ObjectKind]string{
		agent.ObjectUnit:       "unit",
		agent.ObjectNamespace:  "namespace",
		agent.ObjectChangeUnit: "change",
	}[spec.Object]
	if want == "" {
		return
	}

	for _, p := range spec.Params {
		if p.Name == want && p.Required {
			return
		}
	}

	t.Errorf("tool %q acts on %q but has no required parameter %q", spec.Name, spec.Object, want)
}

func checkParam(t *testing.T, tool agent.ToolName, p agent.Param) {
	t.Helper()

	if p.Name == "" || p.Type == "" || p.Description == "" {
		t.Errorf("tool %q has a parameter with no name, type or description: %+v", tool, p)
	}

	// Every parameter but a boolean has a maximum, and only a string has an
	// enum.
	if (p.Max > 0) == (p.Type == agent.ParamBoolean) || (len(p.Enum) > 0 && p.Type != agent.ParamString) {
		t.Errorf("tool %q parameter %q: type %q with max %d and enum %v", tool, p.Name, p.Type, p.Max, p.Enum)
	}
}

func TestCatalogHasNoCommandTool(t *testing.T) {
	t.Parallel()

	// The agent never runs a command. A tool that takes a command, a script
	// or code to run must never enter the catalog.
	banned := []string{"shell", "exec", "command", "script", "eval", "run_code", "bash"}

	for _, spec := range agent.Catalog() {
		for _, word := range banned {
			if strings.Contains(string(spec.Name), word) {
				t.Errorf("tool %q looks like a command tool (%q)", spec.Name, word)
			}

			for _, p := range spec.Params {
				if strings.Contains(strings.ToLower(p.Name), word) {
					t.Errorf("tool %q has parameter %q that looks like a command (%q)", spec.Name, p.Name, word)
				}
			}
		}
	}
}

func TestCatalogOnlyReadStateAnswersSecrets(t *testing.T) {
	t.Parallel()

	for _, spec := range agent.Catalog() {
		wantSecret := spec.Name == agent.ToolReadState
		if (spec.SecretAction != "") != wantSecret {
			t.Errorf("tool %q: secret action %q", spec.Name, spec.SecretAction)
		}

		hasConsent := slices.ContainsFunc(spec.Params, func(p agent.Param) bool { return p.Name == agent.ConsentParam })
		if hasConsent != (spec.SecretAction != "") {
			t.Errorf("tool %q: parameter %q is %v, want it on each tool that can reveal and on no other",
				spec.Name, agent.ConsentParam, hasConsent)
		}
	}
}

func TestCatalogActions(t *testing.T) {
	t.Parallel()

	// The tools check the actions of the job model, so an agent never does
	// more through a tool than the person can do through the API.
	want := map[agent.ToolName]identity.Action{
		agent.ToolReadRunOutput: agent.ActionJobReadLogs,
		agent.ToolGetChange:     agent.ActionJobRead,
		agent.ToolCancelChange:  agent.ActionJobCancel,
		agent.ToolObserveUnit:   agent.ActionUnitObserve,
		agent.ToolProposeChange: "",
	}

	for name, action := range want {
		spec, ok := agent.SpecFor(name)
		if !ok || spec.Action != action {
			t.Errorf("SpecFor(%q) = %q, %v, want action %q", name, spec.Action, ok, action)
		}
	}

	if _, ok := agent.SpecFor("compare_states"); ok {
		t.Error("compare_states is in the catalog: read_state with compareTo replaces it")
	}
}

func TestCatalogReturnsACopy(t *testing.T) {
	t.Parallel()

	first := agent.Catalog()
	first[0].Name = "changed"
	first[1].Params[0].Name = "changed"

	second := agent.Catalog()
	if second[0].Name == "changed" || second[1].Params[0].Name == "changed" {
		t.Fatal("a change of the returned catalog changed the catalog")
	}
}
