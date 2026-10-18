package sdk_test

import (
	"errors"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/providerkit/lifecycle"
	"opendustpan.org/providerkit/schema"
	"opendustpan.org/providerkit/sdk"
)

type record struct {
	ID      *string            `provider:"id,computed"`
	Name    string             `provider:"name,required"`
	Team    *string            `provider:"team,optional"`
	Count   *int8              `provider:"count,optional"`
	Size    *uint16            `provider:"size,optional"`
	Weight  *float64           `provider:"weight,optional"`
	Enabled *bool              `provider:"enabled,optional"`
	Tags    *[]string          `provider:"tags,optional"`
	Limits  *map[string]uint32 `provider:"limits,optional"`
}

func num(s string) schema.Value {
	n, ok := new(big.Float).SetPrec(schema.NumberPrecision).SetString(s)
	if !ok {
		panic("bad number in a test: " + s)
	}

	return schema.NumberValue(n)
}

func TestDecodeNullIsNil(t *testing.T) {
	t.Parallel()

	got, err := sdk.Decode[record](schema.Values{
		"name": schema.StringValue(""), "team": schema.NullOf(schema.String()),
		"id": schema.UnknownOf(schema.String()), "count": schema.IntValue(0),
	})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if got.ID != nil || got.Team != nil || got.Size != nil || got.Tags != nil {
		t.Fatal("Decode() set a field of a null or unknown value, want nil")
	}

	if got.Count == nil || *got.Count != 0 {
		t.Fatal("Decode() of the number 0 gave nil, want a pointer to 0")
	}
}

func TestDecodeNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		attr string
		v    schema.Value
		ok   bool
	}{
		{name: "whole number to int8", attr: "count", v: num("-128"), ok: true},
		{name: "fraction to int8", attr: "count", v: num("1.5"), ok: false},
		{name: "above int8", attr: "count", v: num("128"), ok: false},
		{name: "above int64", attr: "count", v: num("9223372036854775808"), ok: false},
		{name: "whole number to uint16", attr: "size", v: num("65535"), ok: true},
		{name: "negative to uint16", attr: "size", v: num("-1"), ok: false},
		{name: "above uint16", attr: "size", v: num("65536"), ok: false},
		{name: "fraction to float64", attr: "weight", v: num("0.1"), ok: true},
		{name: "above float64", attr: "weight", v: num("1e400"), ok: false},
		{name: "fraction in a map of uint32", attr: "limits", v: schema.MapValue(schema.Number(),
			map[string]schema.Value{"cpu": num("2.5")}), ok: false},
		{name: "string for a number", attr: "count", v: schema.StringValue("1"), ok: false},
		{name: "list with an unknown element", attr: "tags", v: schema.ListValue(schema.String(),
			schema.UnknownOf(schema.String())), ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := sdk.Decode[record](schema.Values{"name": schema.StringValue("a"), tt.attr: tt.v})
			if tt.ok && err != nil {
				t.Fatalf("Decode() error = %v, want nil", err)
			}

			if !tt.ok && !errors.Is(err, lifecycle.ErrInvalidConfig) {
				t.Fatalf("Decode() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestDecodeRequiredNull(t *testing.T) {
	t.Parallel()

	if _, err := sdk.Decode[record](schema.Values{}); !errors.Is(err, lifecycle.ErrInvalidConfig) {
		t.Fatalf("Decode() error = %v, want ErrInvalidConfig", err)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	id, team, count, size, weight, enabled := "r1", "", int8(-3), uint16(7), 0.25, false
	tags := []string{"a", "b"}
	limits := map[string]uint32{"cpu": math.MaxUint32}
	in := record{
		ID: &id, Name: "n", Team: &team, Count: &count, Size: &size, Weight: &weight, Enabled: &enabled,
		Tags: &tags, Limits: &limits,
	}

	vs, err := sdk.Encode(in)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	if vs["team"].IsNull() || !vs["team"].Equal(schema.StringValue("")) {
		t.Fatal("Encode() of an empty string gave null, want the empty string")
	}

	out, err := sdk.Decode[record](vs)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	again, err := sdk.Encode(out)
	if err != nil {
		t.Fatalf("Encode() of the decoded model error = %v", err)
	}

	for name, v := range vs {
		if !again[name].Equal(v) {
			t.Errorf("attribute %q changed in a round trip", name)
		}
	}

	nulls, err := sdk.Encode(record{
		ID: nil, Name: "n", Team: nil, Count: nil, Size: nil, Weight: nil, Enabled: nil, Tags: nil, Limits: nil,
	})
	if err != nil || !nulls["team"].IsNull() || !nulls["tags"].Type().Equal(schema.ListOf(schema.String())) {
		t.Fatalf("Encode() of nil fields = %v, %v, want typed nulls", nulls, err)
	}
}

func TestEncodeRefusesNaN(t *testing.T) {
	t.Parallel()

	nan := math.NaN()
	m := record{
		ID:      nil,
		Name:    "n",
		Team:    nil,
		Count:   nil,
		Size:    nil,
		Weight:  &nan,
		Enabled: nil,
		Tags:    nil,
		Limits:  nil,
	}

	if _, err := sdk.Encode(m); !errors.Is(err, sdk.ErrInvalidModel) {
		t.Fatalf("Encode() error = %v, want ErrInvalidModel", err)
	}
}

type config struct {
	Endpoint string `provider:"endpoint,required"`
	Token    string `provider:"token,required,sensitive"`
}

func TestConfigSchemaOf(t *testing.T) {
	t.Parallel()

	s, err := sdk.ConfigSchemaOf[config]()
	if err != nil {
		t.Fatalf("ConfigSchemaOf() error = %v", err)
	}

	token, _ := s.Lookup("token")
	file, ok := s.Lookup("token_file")

	if !ok || file.Mode != schema.ModeOptional || token.Mode != schema.ModeOptional {
		t.Fatalf("ConfigSchemaOf() = %+v, want an optional token and token_file", s.Attributes)
	}

	_ = config{Endpoint: "", Token: ""}
}

func TestSecretFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := sdk.NewCall(config{Endpoint: "", Token: ""}, nil, dir)

	tests := []string{"t0ken", "", "line one\nline two"}
	seen := map[string]bool{}

	for _, value := range tests {
		path, err := c.SecretFile(value)
		if err != nil {
			t.Fatalf("SecretFile() error = %v", err)
		}

		if filepath.Dir(path) != dir || seen[path] {
			t.Fatalf("SecretFile() = %q, want a new file in %q", path, dir)
		}

		seen[path] = true

		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("secret file mode = %v, %v, want 0600", info.Mode().Perm(), err)
		}

		got, err := os.ReadFile(path) //nolint:gosec // A file of this test.
		if err != nil || string(got) != value {
			t.Fatalf("secret file holds %q, %v, want the value", got, err)
		}
	}

	none := sdk.NewCall(config{Endpoint: "", Token: ""}, nil, "")
	if _, err := none.SecretFile("x"); !errors.Is(err, sdk.ErrNoSessionDir) {
		t.Fatalf("SecretFile() without a directory error = %v, want ErrNoSessionDir", err)
	}
}
