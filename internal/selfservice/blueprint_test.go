package selfservice_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/policy"
	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/internal/selfservice"
)

func parseBlueprint(t *testing.T, data []byte) selfservice.Blueprint {
	t.Helper()

	b, err := selfservice.ParseBlueprint(data)
	if err != nil {
		t.Fatalf("ParseBlueprint() = %v", err)
	}

	return b
}

// edit returns the JSON of the blueprint of the ADR, changed by f.
func edit(t *testing.T, f func(b *selfservice.Blueprint)) []byte {
	t.Helper()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))
	f(&b)

	out, err := json.Marshal(&b)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}

	return out
}

func source(b *selfservice.Blueprint, i int) *selfservice.Source {
	return b.Inputs[i].Choice.Source
}

func onInput(name string) []selfservice.Filter {
	return []selfservice.Filter{{Equals: selfservice.Operand{Input: name, Value: nil}, Output: "x", Path: ""}}
}

// chain returns n string inputs where input i+1 depends on input i.
func chain(n int) []selfservice.Input {
	out := make([]selfservice.Input, 0, n)
	for i := range n {
		src := selfservice.Source{
			Label:      nil,
			Value:      selfservice.OutputRef{Output: "id", Path: ""},
			Blueprint:  "network",
			Collection: "",
			Where:      nil,
		}
		if i > 0 {
			src.Where = onInput(fmt.Sprintf("c%d", i-1))
		}

		out = append(out, selfservice.Input{
			Choice: &selfservice.Choice{Source: &src, Values: nil},
			Name:   fmt.Sprintf("c%d", i), Title: "", Description: "", Type: selfservice.TypeString,
			Maps: fmt.Sprintf("k%d", i), Default: nil, Validate: nil, Required: false,
		})
	}

	return out
}

func registry(version string) selfservice.Program {
	m := selfservice.Module{
		Source: "registry.example.com/platform/postgres/aws", Version: version,
		Connection: "", Repository: "", Directory: "", Revision: "",
	}

	return selfservice.Program{Module: &m, Component: nil}
}

func TestParseBlueprint(t *testing.T) {
	t.Parallel()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))
	if b.Name != "postgres" || len(b.Inputs) != 4 || b.Program.Module == nil {
		t.Fatalf("ParseBlueprint() = %+v", b)
	}

	order, err := selfservice.Order(b.Inputs)
	if err != nil {
		t.Fatalf("Order() = %v", err)
	}

	if strings.Join(order, ",") != "size,network,subnetGroup,storageGb" {
		t.Fatalf("Order() = %v", order)
	}

	unknown := strings.Replace(string(readFile(t, "postgres.blueprint.json")), `"schemaVersion": 1,`,
		`"schemaVersion": 1, "steps": [],`, 1)
	if _, err := selfservice.ParseBlueprint([]byte(unknown)); !errors.Is(err, selfservice.ErrInvalid) {
		t.Fatalf("ParseBlueprint(an unknown field) = %v, want ErrInvalid", err)
	}
}

func TestBlueprintValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		edit func(b *selfservice.Blueprint)
		want error
	}{
		{"the blueprint of the ADR", func(*selfservice.Blueprint) {}, nil},
		{"a component", func(b *selfservice.Blueprint) {
			b.Tool = "pulumi"
			b.Program = selfservice.Program{Module: nil, Component: &selfservice.Component{
				Package: "acme-postgres", Version: "1.2.0", Type: "acme:index:Postgres",
			}}
		}, nil},
		{"a module from git at a tag", func(b *selfservice.Blueprint) {
			b.Program = selfservice.Program{Module: &selfservice.Module{
				Source: "", Version: "", Connection: "", Repository: "platform/modules", Directory: "postgres",
				Revision: "v2.4.1",
			}, Component: nil}
		}, nil},
		{
			"a newer schema version",
			func(b *selfservice.Blueprint) { b.SchemaVersion = 2 },
			selfservice.ErrSchemaVersion,
		},
		{"a version range", func(b *selfservice.Blueprint) { b.Program = registry("~> 2.4") }, selfservice.ErrInvalid},
		{"a module and a component", func(b *selfservice.Blueprint) {
			b.Program.Component = &selfservice.Component{Package: "p", Version: "1.0.0", Type: "t"}
		}, selfservice.ErrInvalid},
		{"no unit name", func(b *selfservice.Blueprint) { b.UnitName = "" }, selfservice.ErrInvalid},
		{
			"two inputs map to one key",
			func(b *selfservice.Blueprint) { b.Inputs[1].Maps = "instance_size" },
			selfservice.ErrOneHome,
		},
		{"an input maps to a platform value", func(b *selfservice.Blueprint) {
			b.Inputs[3].Maps = "backup_retention_days"
		}, selfservice.ErrOneHome},
		{
			"two inputs with one name",
			func(b *selfservice.Blueprint) { b.Inputs[1].Name = "size" },
			selfservice.ErrInvalid,
		},
		{
			"a default of another type",
			func(b *selfservice.Blueprint) { b.Inputs[3].Default = json.RawMessage(`"20"`) },
			selfservice.ErrInvalid,
		},
		{"a default that is not an integer", func(b *selfservice.Blueprint) {
			b.Inputs[3].Default = json.RawMessage(`20.5`)
		}, selfservice.ErrInvalid},
		{
			"required with a default",
			func(b *selfservice.Blueprint) { b.Inputs[0].Required = true },
			selfservice.ErrInvalid,
		},
		{"a choice on an integer", func(b *selfservice.Blueprint) {
			b.Inputs[3].Choice = &selfservice.Choice{Source: nil, Values: []string{"20"}}
		}, selfservice.ErrInvalid},
		{"a choice with values and a source", func(b *selfservice.Blueprint) {
			b.Inputs[1].Choice.Values = []string{"a"}
		}, selfservice.ErrInvalid},
		{"a value twice", func(b *selfservice.Blueprint) {
			b.Inputs[0].Choice = &selfservice.Choice{Source: nil, Values: []string{"small", "small"}}
		}, selfservice.ErrInvalid},
		{
			"a filter on an unknown input", func(b *selfservice.Blueprint) { source(b, 2).Where = onInput("vpc") },
			selfservice.ErrInvalid,
		},
		{
			"a choice that depends on itself",
			func(b *selfservice.Blueprint) { source(b, 2).Where = onInput("subnetGroup") },
			selfservice.ErrChoiceCycle,
		},
		{
			"two choices in a loop", func(b *selfservice.Blueprint) { source(b, 1).Where = onInput("subnetGroup") },
			selfservice.ErrChoiceCycle,
		},
		{
			"a chain at the limit",
			func(b *selfservice.Blueprint) { b.Inputs = chain(selfservice.MaxChoiceDepth + 1) },
			nil,
		},
		{
			"a chain over the limit",
			func(b *selfservice.Blueprint) { b.Inputs = chain(selfservice.MaxChoiceDepth + 2) },
			selfservice.ErrLimit,
		},
		{"a filter with an object value", func(b *selfservice.Blueprint) {
			source(b, 2).Where = []selfservice.Filter{{
				Equals: selfservice.Operand{Input: "", Value: selfservice.ScalarValue(`{}`)}, Output: "x", Path: "",
			}}
		}, selfservice.ErrInvalid},
		{"a source with a blueprint and a collection", func(b *selfservice.Blueprint) {
			source(b, 1).Collection = "networks"
		}, selfservice.ErrInvalid},
		{"a rule with no message", func(b *selfservice.Blueprint) {
			b.Inputs[3].Validate = []selfservice.Rule{{Rule: "value > 1", Message: ""}}
		}, selfservice.ErrInvalid},
		{"approve with no approvals", func(b *selfservice.Blueprint) {
			b.Policy = []selfservice.PolicyRule{
				{When: "true", Effect: policy.EffectApprove, Message: "", Groups: nil, Approvals: 0},
			}
		}, selfservice.ErrInvalid},
		{"a policy rule that allows", func(b *selfservice.Blueprint) {
			b.Policy = []selfservice.PolicyRule{
				{When: "true", Effect: policy.EffectAllow, Message: "", Groups: nil, Approvals: 0},
			}
		}, selfservice.ErrInvalid},
		{"a quota with a bad window", func(b *selfservice.Blueprint) {
			b.Quotas = []selfservice.QuotaRule{{
				Kind: quota.KindRequests, Scope: quota.ScopeRequester, Window: "1 day",
				Rule: "", Message: "", Max: 3,
			}}
		}, selfservice.ErrInvalid},
		{"a quota with a short window", func(b *selfservice.Blueprint) {
			b.Quotas = []selfservice.QuotaRule{{
				Kind: quota.KindRequests, Scope: quota.ScopeRequester, Window: "1s",
				Rule: "", Message: "", Max: 3,
			}}
		}, selfservice.ErrInvalid},
		{"a custom quota check", func(b *selfservice.Blueprint) {
			b.Quotas = []selfservice.QuotaRule{{
				Kind: quota.KindExpr, Scope: "", Window: "", Rule: "usage.units < 5",
				Message: "Five at most.", Max: 0,
			}}
		}, nil},
		{"a default home that is not allowed", func(b *selfservice.Blueprint) {
			b.Delivery = selfservice.Delivery{
				Git:     nil,
				Default: selfservice.HomeOpenDustpan,
				Homes:   []selfservice.HomeKind{selfservice.HomeGit},
			}
		}, selfservice.ErrInvalid},
		{"no home", func(b *selfservice.Blueprint) {
			b.Delivery = selfservice.Delivery{Git: nil, Default: selfservice.HomeGit, Homes: nil}
		}, selfservice.ErrInvalid},
		{"an unknown tool", func(b *selfservice.Blueprint) { b.Tool = "terraform" }, selfservice.ErrInvalid},
		{
			"a module for Pulumi",
			func(b *selfservice.Blueprint) { b.Tool = selfservice.ToolPulumi },
			selfservice.ErrInvalid,
		},
		{"maps to a key that is not an HCL identifier", func(b *selfservice.Blueprint) {
			b.Inputs[0].Maps = "size\"\nresource \"x\""
		}, selfservice.ErrInvalid},
		{"maps to a name that a module block keeps", func(b *selfservice.Blueprint) {
			b.Inputs[0].Maps = "count"
		}, selfservice.ErrInvalid},
		{"a platform value that is not a key", func(b *selfservice.Blueprint) {
			b.Platform.Values["${file(\"/etc/passwd\")}"] = json.RawMessage(`1`)
		}, selfservice.ErrInvalid},
		{"two inputs map to one key in another case", func(b *selfservice.Blueprint) {
			b.Inputs[1].Maps = "Instance_Size"
		}, selfservice.ErrOneHome},
		{"a Pulumi key with and with no project namespace", func(b *selfservice.Blueprint) {
			b.Tool = selfservice.ToolPulumi
			b.Program = selfservice.Program{Module: nil, Component: &selfservice.Component{
				Package: "acme-postgres", Version: "1.2.0", Type: "acme:index:Postgres",
			}}
			b.Inputs[1].Maps = "postgres:instance_size"
		}, selfservice.ErrOneHome},
		{"a Pulumi key of a provider", func(b *selfservice.Blueprint) {
			b.Tool = selfservice.ToolPulumi
			b.Program = selfservice.Program{Module: nil, Component: &selfservice.Component{
				Package: "acme-postgres", Version: "1.2.0", Type: "acme:index:Postgres",
			}}
			b.Platform.Values["aws:region"] = json.RawMessage(`"eu-west-1"`)
		}, nil},
		{"a Pulumi key with two namespaces", func(b *selfservice.Blueprint) {
			b.Tool = selfservice.ToolPulumi
			b.Program = selfservice.Program{Module: nil, Component: &selfservice.Component{
				Package: "acme-postgres", Version: "1.2.0", Type: "acme:index:Postgres",
			}}
			b.Inputs[0].Maps = "a:b:c"
		}, selfservice.ErrInvalid},
		{
			"a source with no source principal", func(b *selfservice.Blueprint) { b.SourcePrincipal = "" },
			selfservice.ErrInvalid,
		},
		{"a source principal with no source", func(b *selfservice.Blueprint) {
			b.Inputs = b.Inputs[:1]
		}, selfservice.ErrInvalid},
		{"a source principal that is not an ID", func(b *selfservice.Blueprint) {
			b.SourcePrincipal = "a/b"
		}, selfservice.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := selfservice.ParseBlueprint(edit(t, tc.edit))
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("ParseBlueprint() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestQuotaRule(t *testing.T) {
	t.Parallel()

	r, err := selfservice.QuotaRule{
		Kind:    "requests",
		Scope:   "requester",
		Window:  "24h",
		Rule:    "",
		Message: "",
		Max:     3,
	}.QuotaRule()
	if err != nil || r.Window.Hours() != 24 || r.Max != 3 {
		t.Fatalf("QuotaRule() = %+v, %v", r, err)
	}
}

func TestCheckAnswers(t *testing.T) {
	t.Parallel()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))

	d := parseDocument(t, `{"schemaVersion":1,"namespace":"payments","name":"orders-db",`+
		`"blueprint":{"name":"postgres","version":3},`+
		`"inputs":{"size":3,"storageGb":20.5,"zone":"a","color":"red"}}`)

	got := selfservice.CheckAnswers(&b, &d)

	want := []selfservice.Failure{
		{Input: "size", Code: selfservice.FailureType, Message: "Give a value of the type string."},
		{Input: "network", Code: selfservice.FailureRequired, Message: "Give an answer."},
		{Input: "subnetGroup", Code: selfservice.FailureRequired, Message: "Give an answer."},
		{Input: "storageGb", Code: selfservice.FailureType, Message: "Give a value of the type integer."},
		{Input: "color", Code: selfservice.FailureUnknownInput, Message: "The blueprint postgres has no input color."},
		{Input: "zone", Code: selfservice.FailureUnknownInput, Message: "The blueprint postgres has no input zone."},
	}

	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("CheckAnswers() =\n%v\nwant\n%v", got, want)
	}

	if len(got) == 0 {
		t.Fatal("CheckAnswers() = no failure")
	}

	var err error = &selfservice.CheckError{Point: selfservice.PointSubmit, Failures: got[:1]}
	if !errors.Is(err, selfservice.ErrCheckFailed) ||
		err.Error() != "self-service request failed its checks at submit: size: type: Give a value of the type string." {
		t.Fatalf("CheckError = %q", err.Error())
	}

	values, err := selfservice.AnswerValues(&b, &d)
	if err != nil || values["size"] != float64(3) {
		t.Fatalf("AnswerValues() = %v, %v", values, err)
	}

	ok := parseDocument(t, string(readFile(t, "orders-db.request.json")))
	if f := selfservice.CheckAnswers(&b, &ok); f != nil {
		t.Fatalf("CheckAnswers(the document of the ADR) = %v", f)
	}
}
