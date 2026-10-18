package catalog_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/catalog"
	"opendustpan.org/opendustpan/pkg/policy"
	"opendustpan.org/opendustpan/pkg/quota"
)

func parseBlueprint(t *testing.T, data []byte) catalog.Blueprint {
	t.Helper()

	b, err := catalog.ParseBlueprint(data)
	if err != nil {
		t.Fatalf("ParseBlueprint() = %v", err)
	}

	return b
}

// edit returns the JSON of the blueprint of the ADR, changed by f.
func edit(t *testing.T, f func(b *catalog.Blueprint)) []byte {
	t.Helper()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))
	f(&b)

	out, err := json.Marshal(&b)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}

	return out
}

func source(b *catalog.Blueprint, i int) *catalog.Source {
	return b.Inputs[i].Choice.Source
}

func onInput(name string) []catalog.Filter {
	return []catalog.Filter{{Equals: catalog.Operand{Input: name, Value: nil}, Output: "x", Path: ""}}
}

// chain returns n string inputs where input i+1 depends on input i.
func chain(n int) []catalog.Input {
	out := make([]catalog.Input, 0, n)
	for i := range n {
		src := catalog.Source{
			Label:      nil,
			Value:      catalog.OutputRef{Output: "id", Path: ""},
			Blueprint:  "network",
			Collection: "",
			Where:      nil,
		}
		if i > 0 {
			src.Where = onInput(fmt.Sprintf("c%d", i-1))
		}

		out = append(out, catalog.Input{
			Choice: &catalog.Choice{Source: &src, Values: nil},
			Name:   fmt.Sprintf("c%d", i), Title: "", Description: "", Type: catalog.TypeString,
			Maps: fmt.Sprintf("k%d", i), Default: nil, Validate: nil, Required: false,
		})
	}

	return out
}

func registry(version string) catalog.Program {
	m := catalog.Module{
		Source: "registry.example.com/platform/postgres/aws", Version: version,
		Connection: "", Repository: "", Directory: "", Revision: "",
	}

	return catalog.Program{Module: &m, Component: nil}
}

func TestParseBlueprint(t *testing.T) {
	t.Parallel()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))
	if b.Name != "postgres" || len(b.Inputs) != 4 || b.Program.Module == nil {
		t.Fatalf("ParseBlueprint() = %+v", b)
	}

	order, err := catalog.Order(b.Inputs)
	if err != nil {
		t.Fatalf("Order() = %v", err)
	}

	if strings.Join(order, ",") != "size,network,subnetGroup,storageGb" {
		t.Fatalf("Order() = %v", order)
	}

	unknown := strings.Replace(string(readFile(t, "postgres.blueprint.json")), `"schemaVersion": 1,`,
		`"schemaVersion": 1, "steps": [],`, 1)
	if _, err := catalog.ParseBlueprint([]byte(unknown)); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("ParseBlueprint(an unknown field) = %v, want ErrInvalid", err)
	}
}

func TestBlueprintValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		edit func(b *catalog.Blueprint)
		want error
	}{
		{"the blueprint of the ADR", func(*catalog.Blueprint) {}, nil},
		{"a component", func(b *catalog.Blueprint) {
			b.Tool = "pulumi"
			b.Program = catalog.Program{Module: nil, Component: &catalog.Component{
				Package: "acme-postgres", Version: "1.2.0", Type: "acme:index:Postgres",
			}}
		}, nil},
		{"a module from git at a tag", func(b *catalog.Blueprint) {
			b.Program = catalog.Program{Module: &catalog.Module{
				Source: "", Version: "", Connection: "", Repository: "platform/modules", Directory: "postgres",
				Revision: "v2.4.1",
			}, Component: nil}
		}, nil},
		{"a newer schema version", func(b *catalog.Blueprint) { b.SchemaVersion = 2 }, catalog.ErrSchemaVersion},
		{"a version range", func(b *catalog.Blueprint) { b.Program = registry("~> 2.4") }, catalog.ErrInvalid},
		{"a module and a component", func(b *catalog.Blueprint) {
			b.Program.Component = &catalog.Component{Package: "p", Version: "1.0.0", Type: "t"}
		}, catalog.ErrInvalid},
		{"no unit name", func(b *catalog.Blueprint) { b.UnitName = "" }, catalog.ErrInvalid},
		{
			"two inputs map to one key",
			func(b *catalog.Blueprint) { b.Inputs[1].Maps = "instance_size" },
			catalog.ErrOneHome,
		},
		{"an input maps to a platform value", func(b *catalog.Blueprint) {
			b.Inputs[3].Maps = "backup_retention_days"
		}, catalog.ErrOneHome},
		{"two inputs with one name", func(b *catalog.Blueprint) { b.Inputs[1].Name = "size" }, catalog.ErrInvalid},
		{
			"a default of another type", func(b *catalog.Blueprint) { b.Inputs[3].Default = json.RawMessage(`"20"`) },
			catalog.ErrInvalid,
		},
		{"a default that is not an integer", func(b *catalog.Blueprint) {
			b.Inputs[3].Default = json.RawMessage(`20.5`)
		}, catalog.ErrInvalid},
		{"required with a default", func(b *catalog.Blueprint) { b.Inputs[0].Required = true }, catalog.ErrInvalid},
		{"a choice on an integer", func(b *catalog.Blueprint) {
			b.Inputs[3].Choice = &catalog.Choice{Source: nil, Values: []string{"20"}}
		}, catalog.ErrInvalid},
		{"a choice with values and a source", func(b *catalog.Blueprint) {
			b.Inputs[1].Choice.Values = []string{"a"}
		}, catalog.ErrInvalid},
		{"a value twice", func(b *catalog.Blueprint) {
			b.Inputs[0].Choice = &catalog.Choice{Source: nil, Values: []string{"small", "small"}}
		}, catalog.ErrInvalid},
		{
			"a filter on an unknown input", func(b *catalog.Blueprint) { source(b, 2).Where = onInput("vpc") },
			catalog.ErrInvalid,
		},
		{
			"a choice that depends on itself",
			func(b *catalog.Blueprint) { source(b, 2).Where = onInput("subnetGroup") },
			catalog.ErrChoiceCycle,
		},
		{
			"two choices in a loop", func(b *catalog.Blueprint) { source(b, 1).Where = onInput("subnetGroup") },
			catalog.ErrChoiceCycle,
		},
		{"a chain at the limit", func(b *catalog.Blueprint) { b.Inputs = chain(catalog.MaxChoiceDepth + 1) }, nil},
		{
			"a chain over the limit", func(b *catalog.Blueprint) { b.Inputs = chain(catalog.MaxChoiceDepth + 2) },
			catalog.ErrLimit,
		},
		{"a filter with an object value", func(b *catalog.Blueprint) {
			source(b, 2).Where = []catalog.Filter{{
				Equals: catalog.Operand{Input: "", Value: json.RawMessage(`{}`)}, Output: "x", Path: "",
			}}
		}, catalog.ErrInvalid},
		{"a source with a blueprint and a collection", func(b *catalog.Blueprint) {
			source(b, 1).Collection = "networks"
		}, catalog.ErrInvalid},
		{"a rule with no message", func(b *catalog.Blueprint) {
			b.Inputs[3].Validate = []catalog.Rule{{Rule: "value > 1", Message: ""}}
		}, catalog.ErrInvalid},
		{"approve with no approvals", func(b *catalog.Blueprint) {
			b.Policy = []catalog.PolicyRule{
				{When: "true", Effect: policy.EffectApprove, Message: "", Groups: nil, Approvals: 0},
			}
		}, catalog.ErrInvalid},
		{"a policy rule that allows", func(b *catalog.Blueprint) {
			b.Policy = []catalog.PolicyRule{
				{When: "true", Effect: policy.EffectAllow, Message: "", Groups: nil, Approvals: 0},
			}
		}, catalog.ErrInvalid},
		{"a quota with a bad window", func(b *catalog.Blueprint) {
			b.Quotas = []catalog.QuotaRule{{
				Kind: quota.KindRequests, Scope: quota.ScopeRequester, Window: "1 day",
				Rule: "", Message: "", Max: 3,
			}}
		}, catalog.ErrInvalid},
		{"a quota with a short window", func(b *catalog.Blueprint) {
			b.Quotas = []catalog.QuotaRule{{
				Kind: quota.KindRequests, Scope: quota.ScopeRequester, Window: "1s",
				Rule: "", Message: "", Max: 3,
			}}
		}, catalog.ErrInvalid},
		{"a custom quota check", func(b *catalog.Blueprint) {
			b.Quotas = []catalog.QuotaRule{{
				Kind: quota.KindExpr, Scope: "", Window: "", Rule: "usage.units < 5",
				Message: "Five at most.", Max: 0,
			}}
		}, nil},
		{"a default home that is not allowed", func(b *catalog.Blueprint) {
			b.Delivery = catalog.Delivery{
				Git:     nil,
				Default: catalog.HomeOpenDustpan,
				Homes:   []catalog.HomeKind{catalog.HomeGit},
			}
		}, catalog.ErrInvalid},
		{"no home", func(b *catalog.Blueprint) {
			b.Delivery = catalog.Delivery{Git: nil, Default: catalog.HomeGit, Homes: nil}
		}, catalog.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := catalog.ParseBlueprint(edit(t, tc.edit))
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("ParseBlueprint() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestQuotaRule(t *testing.T) {
	t.Parallel()

	r, err := catalog.QuotaRule{
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

	got := catalog.CheckAnswers(&b, &d)

	want := []catalog.Failure{
		{Input: "size", Code: catalog.FailureType, Message: "Give a value of the type string."},
		{Input: "network", Code: catalog.FailureRequired, Message: "Give an answer."},
		{Input: "subnetGroup", Code: catalog.FailureRequired, Message: "Give an answer."},
		{Input: "storageGb", Code: catalog.FailureType, Message: "Give a value of the type integer."},
		{Input: "color", Code: catalog.FailureUnknownInput, Message: "The blueprint postgres has no input color."},
		{Input: "zone", Code: catalog.FailureUnknownInput, Message: "The blueprint postgres has no input zone."},
	}

	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("CheckAnswers() =\n%v\nwant\n%v", got, want)
	}

	if len(got) == 0 {
		t.Fatal("CheckAnswers() = no failure")
	}

	var err error = &catalog.CheckError{Point: catalog.PointSubmit, Failures: got[:1]}
	if !errors.Is(err, catalog.ErrCheckFailed) ||
		err.Error() != "catalog request failed its checks at submit: size: type: Give a value of the type string." {
		t.Fatalf("CheckError = %q", err.Error())
	}

	values, err := catalog.AnswerValues(&b, &d)
	if err != nil || values["size"] != float64(3) {
		t.Fatalf("AnswerValues() = %v, %v", values, err)
	}

	ok := parseDocument(t, string(readFile(t, "orders-db.request.json")))
	if f := catalog.CheckAnswers(&b, &ok); f != nil {
		t.Fatalf("CheckAnswers(the document of the ADR) = %v", f)
	}
}
