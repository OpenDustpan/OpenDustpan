package lifecycle_test

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"opendustpan.org/providerkit/lifecycle"
	"opendustpan.org/providerkit/schema"
)

func attr(name string, mode schema.Mode) schema.Attribute {
	return schema.Attribute{
		Name: name, Type: schema.String(), Mode: mode,
		Sensitive: false, WriteOnly: false, Replace: false, Stable: false, Description: "",
	}
}

// recordSchema is a DNS-like record: the zone forces a replace, the value
// changes in place, the token is write-only, and the API sets the id, the
// fqdn, the creation time and the version. The ttl is optional and computed:
// the API picks one when the user does not. The creation time is stable: an
// update never changes it. The version changes on every write.
func recordSchema() schema.Schema {
	zone := attr("zone", schema.ModeRequired)
	zone.Replace = true

	token := attr("token", schema.ModeOptional)
	token.WriteOnly = true

	created := attr("created_at", schema.ModeComputed)
	created.Stable = true

	return schema.Schema{Version: 0, Attributes: []schema.Attribute{
		attr("id", schema.ModeComputed),
		zone,
		attr("value", schema.ModeRequired),
		attr("ttl", schema.ModeOptionalComputed),
		token,
		attr("fqdn", schema.ModeComputed),
		created,
		attr("version", schema.ModeComputed),
	}}
}

func str(s string) schema.Value { return schema.StringValue(s) }

func state() schema.Values {
	return schema.Values{
		"id": str("r1"), "zone": str("example.org"), "value": str("10.0.0.1"),
		"ttl": str("300"), "token": schema.NullOf(schema.String()), "fqdn": str("a.example.org"),
		"created_at": str("2026-10-18T00:00:00Z"), "version": str("1"),
	}
}

func config(over schema.Values) schema.Values {
	c := schema.Values{"zone": str("example.org"), "value": str("10.0.0.1"), "token": str("secret")}
	maps.Copy(c, over)

	return c
}

// want is the expected plan: the action, the changed and replacing
// attributes, and the attributes that are unknown in Planned.
type want struct {
	action     lifecycle.Action
	changed    []string
	replacedBy []string
	unknown    []string
}

func TestPlan(t *testing.T) {
	t.Parallel()

	created := []string{"id", "ttl", "fqdn", "created_at", "version"}
	tests := []struct {
		name   string
		prior  schema.Values
		config schema.Values
		want   want
	}{
		{
			name: "nothing on either side", prior: nil, config: nil,
			want: want{action: lifecycle.ActionNoOp, changed: nil, replacedBy: nil, unknown: nil},
		},
		{
			name: "new object", prior: nil, config: config(nil),
			want: want{action: lifecycle.ActionCreate, changed: nil, replacedBy: nil, unknown: created},
		},
		{
			name: "gone from configuration", prior: state(), config: nil,
			want: want{action: lifecycle.ActionDelete, changed: nil, replacedBy: nil, unknown: nil},
		},
		{
			name: "same values", prior: state(), config: config(nil),
			want: want{action: lifecycle.ActionNoOp, changed: nil, replacedBy: nil, unknown: nil},
		},
		{
			name: "new write-only value only", prior: state(), config: config(schema.Values{"token": str("other")}),
			want: want{action: lifecycle.ActionNoOp, changed: nil, replacedBy: nil, unknown: nil},
		},
		{
			name: "value changes in place", prior: state(), config: config(schema.Values{"value": str("10.0.0.2")}),
			want: want{
				action: lifecycle.ActionUpdate, changed: []string{"value"}, replacedBy: nil,
				unknown: []string{"ttl", "fqdn", "version"},
			},
		},
		{
			name:   "user sets the optional computed ttl",
			prior:  state(),
			config: config(schema.Values{"ttl": str("60")}),
			want: want{
				action: lifecycle.ActionUpdate, changed: []string{"ttl"}, replacedBy: nil,
				unknown: []string{"fqdn", "version"},
			},
		},
		{
			name: "zone forces a replace", prior: state(), config: config(schema.Values{"zone": str("example.net")}),
			want: want{
				action:     lifecycle.ActionReplace,
				changed:    []string{"zone"},
				replacedBy: []string{"zone"},
				unknown:    created,
			},
		},
		{
			name: "unknown value counts as changed", prior: state(),
			config: config(schema.Values{"value": schema.UnknownOf(schema.String())}),
			want: want{
				action: lifecycle.ActionUpdate, changed: []string{"value"}, replacedBy: nil,
				unknown: []string{"value", "ttl", "fqdn", "version"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := lifecycle.Plan(recordSchema(), tt.prior, tt.config)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}

			checkChange(t, got, tt.want)
			checkWriteOnly(t, got, tt.config)
		})
	}
}

func checkChange(t *testing.T, got lifecycle.Change, w want) {
	t.Helper()

	if got.Action != w.action {
		t.Fatalf("Action = %v, want %v", got.Action, w.action)
	}

	if !slices.Equal(got.Changed, w.changed) || !slices.Equal(got.ReplacedBy, w.replacedBy) {
		t.Fatalf("Changed, ReplacedBy = %v, %v, want %v, %v", got.Changed, got.ReplacedBy, w.changed, w.replacedBy)
	}

	for _, name := range w.unknown {
		if _, ok := got.Planned[name]; !ok {
			t.Errorf("Planned has no %q, want it unknown", name)
		}
	}

	for name, v := range got.Planned {
		if v.IsKnown() == slices.Contains(w.unknown, name) {
			t.Errorf("Planned[%q] known = %v, want %v", name, v.IsKnown(), !slices.Contains(w.unknown, name))
		}
	}
}

// checkWriteOnly checks that the engine gets the write-only token of the
// configuration and that the tool does not.
func checkWriteOnly(t *testing.T, got lifecycle.Change, config schema.Values) {
	t.Helper()

	if got.Planned == nil {
		return
	}

	if !got.Planned["token"].Equal(config["token"]) {
		t.Errorf("Planned[token] = %v, want the configured value for the engine", got.Planned["token"])
	}

	if !lifecycle.ForTool(recordSchema(), got.Planned)["token"].IsNull() {
		t.Error("ForTool(Planned) holds the write-only token, want null")
	}
}

func TestForTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   schema.Values
		want schema.Values
	}{
		{name: "nil", in: nil, want: nil},
		{
			name: "write-only value", in: schema.Values{"id": str("r1"), "token": str("secret")},
			want: schema.Values{"id": str("r1"), "token": schema.NullOf(schema.String())},
		},
		{
			name: "no write-only key", in: schema.Values{"id": str("r1")},
			want: schema.Values{"id": str("r1")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := maps.Clone(tt.in)
			got := lifecycle.ForTool(recordSchema(), in)

			if (got == nil) != (tt.want == nil) || !maps.EqualFunc(got, tt.want, schema.Value.Equal) {
				t.Fatalf("ForTool() = %v, want %v", got, tt.want)
			}

			if !maps.EqualFunc(in, tt.in, schema.Value.Equal) {
				t.Fatal("ForTool() changed its input")
			}
		})
	}
}

func TestCreated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  schema.Values
		want bool
	}{
		{name: "nil", got: nil, want: false},
		{name: "id", got: schema.Values{"id": str("r1")}, want: true},
		{name: "empty id", got: schema.Values{"id": str("")}, want: false},
		{name: "null id", got: schema.Values{"id": schema.NullOf(schema.String())}, want: false},
		{name: "unknown id", got: schema.Values{"id": schema.UnknownOf(schema.String())}, want: false},
		{name: "values without an id", got: schema.Values{"value": str("10.0.0.1")}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := lifecycle.Created(tt.got); got != tt.want {
				t.Fatalf("Created() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanRefusesInvalidConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config schema.Values
	}{
		{name: "unknown attribute", config: config(schema.Values{"owner": str("x")})},
		{name: "computed attribute set", config: config(schema.Values{"fqdn": str("b.example.org")})},
		{name: "required attribute null", config: config(schema.Values{"value": schema.NullOf(schema.String())})},
		{name: "required attribute missing", config: schema.Values{"zone": str("example.org")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := lifecycle.Plan(recordSchema(), state(), tt.config)
			if !errors.Is(err, lifecycle.ErrInvalidConfig) {
				t.Fatalf("Plan() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}
