package selfservice_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/identity"
)

func unit(t *testing.T, name, id string) identity.Object {
	t.Helper()

	o, err := identity.UnitObject("opentofu", "payments", name)
	if err != nil {
		t.Fatalf("UnitObject() = %v", err)
	}

	return o.WithUnitID(id)
}

// row returns the outputs of one unit. A value that starts with "secret:"
// is a secret output.
func row(t *testing.T, id string, outputs map[string]string) selfservice.UnitOutputs {
	t.Helper()

	out := selfservice.UnitOutputs{
		Unit:         unit(t, "u/"+id, id),
		Outputs:      map[string]selfservice.OutputValue{},
		StateVersion: "sv-1",
	}

	for k, v := range outputs {
		secret := false
		if len(v) > 7 && v[:7] == "secret:" {
			v, secret = v[7:], true
		}

		out.Outputs[k] = selfservice.OutputValue{Value: json.RawMessage(v), Secret: secret}
	}

	return out
}

func networkInput() selfservice.Input {
	label := selfservice.OutputRef{Output: "display_name", Path: ""}

	return selfservice.Input{
		Choice: &selfservice.Choice{Source: &selfservice.Source{
			Label: &label, Value: selfservice.OutputRef{Output: "network_id", Path: ""},
			Blueprint: "network", Collection: "", Where: nil,
		}, Values: nil},
		Name: "network", Title: "", Description: "", Type: selfservice.TypeString, Maps: "network_id",
		Default: nil, Validate: nil, Required: true,
	}
}

func subnetInput() selfservice.Input {
	return selfservice.Input{
		Choice: &selfservice.Choice{Source: &selfservice.Source{
			Label: nil, Value: selfservice.OutputRef{Output: "name", Path: ""},
			Blueprint: "subnet-group", Collection: "",
			Where: []selfservice.Filter{
				{Equals: selfservice.Operand{Input: "network", Value: nil}, Output: "network_id", Path: ""},
				{
					Equals: selfservice.Operand{Input: "", Value: selfservice.ScalarValue(`"private"`)},
					Output: "tier", Path: "",
				},
			},
		}, Values: nil},
		Name: "subnetGroup", Title: "", Description: "", Type: selfservice.TypeString, Maps: "subnet_group",
		Default: nil, Validate: nil, Required: true,
	}
}

func values(set selfservice.OptionSet) []string {
	out := make([]string, 0, len(set.Options))
	for _, o := range set.Options {
		out = append(out, o.Value)
	}

	return out
}

func TestOptionsOfASource(t *testing.T) {
	t.Parallel()

	in := networkInput()
	rows := []selfservice.UnitOutputs{
		row(t, "stk-1", map[string]string{"network_id": `"net-b"`, "display_name": `"Production"`}),
		row(t, "stk-2", map[string]string{"network_id": `"net-a"`, "display_name": `"Development"`}),
		// Two units give one value: one option with both labels.
		row(t, "stk-3", map[string]string{"network_id": `"net-a"`, "display_name": `"Dev (old)"`}),
		// A secret value, a missing value and an object value give no
		// option.
		row(t, "stk-4", map[string]string{"network_id": `secret:"net-s"`, "display_name": `"Secret"`}),
		row(t, "stk-5", map[string]string{"display_name": `"No ID"`}),
		row(t, "stk-6", map[string]string{"network_id": `{"id":"net-o"}`}),
		// A number gives its JSON text, and a missing label shows the value.
		row(t, "stk-7", map[string]string{"network_id": `42`}),
		// The unit of the request itself gives no option.
		row(t, "stk-self", map[string]string{"network_id": `"net-self"`}),
	}

	got := selfservice.Options(&in, nil, rows, "stk-self")

	want := selfservice.OptionSet{
		Input: "network",
		Options: []selfservice.Option{
			{Value: "42", Labels: []string{"42"}},
			{Value: "net-a", Labels: []string{"Dev (old)", "Development"}},
			{Value: "net-b", Labels: []string{"Production"}},
		},
		WaitingFor: nil, Truncated: false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Options() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestOptionsOfADependentChoice(t *testing.T) {
	t.Parallel()

	in := subnetInput()
	rows := []selfservice.UnitOutputs{
		row(t, "s1", map[string]string{"name": `"orders-private"`, "network_id": `"net-a"`, "tier": `"private"`}),
		row(t, "s2", map[string]string{"name": `"orders-public"`, "network_id": `"net-a"`, "tier": `"public"`}),
		row(t, "s3", map[string]string{"name": `"web-private"`, "network_id": `"net-b"`, "tier": `"private"`}),
		// A secret output in a filter never matches.
		row(t, "s4", map[string]string{"name": `"hidden"`, "network_id": `secret:"net-a"`, "tier": `"private"`}),
	}

	waiting := selfservice.Options(&in, map[string]json.RawMessage{}, rows, "")
	if len(waiting.Options) != 0 || !reflect.DeepEqual(waiting.WaitingFor, []string{"network"}) {
		t.Fatalf("Options(no network) = %+v, want waiting for network", waiting)
	}

	for _, tc := range []struct {
		network string
		want    []string
	}{
		{network: `"net-a"`, want: []string{"orders-private"}},
		{network: `"net-b"`, want: []string{"web-private"}},
		{network: `"net-c"`, want: []string{}},
	} {
		got := selfservice.Options(&in, map[string]json.RawMessage{"network": json.RawMessage(tc.network)}, rows, "")
		if !reflect.DeepEqual(values(got), tc.want) {
			t.Fatalf("Options(network %s) = %v, want %v", tc.network, values(got), tc.want)
		}
	}
}

func TestOptionsPathAndEquality(t *testing.T) {
	t.Parallel()

	in := selfservice.Input{
		Choice: &selfservice.Choice{Source: &selfservice.Source{
			Label: nil, Value: selfservice.OutputRef{Output: "network", Path: "/ids/0"},
			Blueprint: "", Collection: "networks",
			Where: []selfservice.Filter{
				{
					Equals: selfservice.Operand{
						Input: "",
						Value: selfservice.ScalarValue(`1`),
					},
					Output: "network",
					Path:   "/zone~1count",
				},
			},
		}, Values: nil},
		Name: "net", Title: "", Description: "", Type: selfservice.TypeString, Maps: "net",
		Default: nil, Validate: nil, Required: false,
	}
	rows := []selfservice.UnitOutputs{
		row(t, "a", map[string]string{"network": `{"ids":["net-a","x"],"zone/count":1.0}`}),
		// The string "1" does not equal the number 1.
		row(t, "b", map[string]string{"network": `{"ids":["net-b"],"zone/count":"1"}`}),
		row(t, "c", map[string]string{"network": `{"ids":[],"zone/count":1}`}),
	}

	if got := values(selfservice.Options(&in, nil, rows, "")); !reflect.DeepEqual(got, []string{"net-a"}) {
		t.Fatalf("Options() = %v, want [net-a]", got)
	}
}

func TestOptionsTruncate(t *testing.T) {
	t.Parallel()

	in := networkInput()
	in.Choice.Source.Label = nil
	rows := make([]selfservice.UnitOutputs, 0, selfservice.MaxOptions+5)

	for i := range selfservice.MaxOptions + 5 {
		id := fmt.Sprintf("u%04d", i)
		rows = append(rows, row(t, id, map[string]string{"network_id": `"` + id + `"`}))
	}

	full := selfservice.Options(&in, nil, rows, "")
	if len(full.Options) != selfservice.MaxOptions+5 || full.Truncated {
		t.Fatalf("Options() gave %d options, truncated %v, want every option", len(full.Options), full.Truncated)
	}

	got := full.ForRoute()
	if len(got.Options) != selfservice.MaxOptions || !got.Truncated || got.Options[0].Value != "u0000" {
		t.Fatalf("ForRoute() gave %d options, truncated %v", len(got.Options), got.Truncated)
	}

	// An answer past the cut is an option of the full set, and the check
	// refuses the cut set.
	last := json.RawMessage(fmt.Sprintf(`"u%04d"`, selfservice.MaxOptions+4))

	if fails, err := selfservice.CheckAnswer(&in, last, full); err != nil || fails != nil {
		t.Fatalf("CheckAnswer(full) = %v, %v, want a pass", fails, err)
	}

	if _, err := selfservice.CheckAnswer(&in, last, got); !errors.Is(err, selfservice.ErrInvalid) {
		t.Fatalf("CheckAnswer(cut) = %v, want ErrInvalid", err)
	}
}

func TestOptionsOfStaticValues(t *testing.T) {
	t.Parallel()

	in := selfservice.Input{
		Choice: &selfservice.Choice{Source: nil, Values: []string{"small", "medium", "large"}},
		Name:   "size", Title: "", Description: "", Type: selfservice.TypeString, Maps: "instance_size",
		Default: json.RawMessage(`"small"`), Validate: nil, Required: false,
	}

	got := selfservice.Options(&in, nil, nil, "")
	if !reflect.DeepEqual(values(got), []string{"small", "medium", "large"}) {
		t.Fatalf("Options() = %v, want the order of the blueprint", values(got))
	}
}

func TestCheckAnswer(t *testing.T) {
	t.Parallel()

	set := selfservice.OptionSet{
		Input:      "zones",
		Options:    []selfservice.Option{{Value: "a", Labels: []string{"a"}}, {Value: "b", Labels: []string{"b"}}},
		WaitingFor: nil, Truncated: false,
	}
	list := selfservice.Input{
		Choice: &selfservice.Choice{Source: nil, Values: []string{"a", "b"}},
		Name:   "zones", Title: "", Description: "", Type: selfservice.TypeStringList, Maps: "zones",
		Default: nil, Validate: nil, Required: false,
	}
	single := list
	single.Type = selfservice.TypeString

	cases := []struct {
		name   string
		in     selfservice.Input
		answer string
		set    selfservice.OptionSet
		codes  []selfservice.FailureCode
	}{
		{"an option", single, `"a"`, set, nil},
		{"an option that is gone", single, `"c"`, set, []selfservice.FailureCode{selfservice.FailureChoice}},
		{"a list of options", list, `["a","b"]`, set, nil},
		{
			"a list with two items that are gone", list, `["a","c","d"]`, set,
			[]selfservice.FailureCode{selfservice.FailureChoice, selfservice.FailureChoice},
		},
		{"a number for a string", single, `1`, set, []selfservice.FailureCode{selfservice.FailureType}},
		{
			"a choice that waits", single, `"a"`,
			selfservice.OptionSet{Input: "zones", Options: nil, WaitingFor: []string{"network"}, Truncated: false},
			[]selfservice.FailureCode{selfservice.FailureChoice},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fails, err := selfservice.CheckAnswer(&tc.in, json.RawMessage(tc.answer), tc.set)
			if err != nil {
				t.Fatalf("CheckAnswer() = %v", err)
			}

			var codes []selfservice.FailureCode
			for _, f := range fails {
				codes = append(codes, f.Code)
			}

			if !reflect.DeepEqual(codes, tc.codes) {
				t.Fatalf("CheckAnswer() = %v, want %v", codes, tc.codes)
			}
		})
	}
}

func TestSourceReads(t *testing.T) {
	t.Parallel()

	in := subnetInput()
	if got := in.Choice.Source.Outputs(); !reflect.DeepEqual(got, []string{"name", "network_id", "tier"}) {
		t.Fatalf("Outputs() = %v", got)
	}

	if got := in.Choice.Source.DependsOn(); !reflect.DeepEqual(got, []string{"network"}) {
		t.Fatalf("DependsOn() = %v", got)
	}
}
