package schema_test

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"testing"

	"opendustpan.org/providerkit/schema"
)

func attr(name string, t schema.Type, mode schema.Mode) schema.Attribute {
	return schema.Attribute{
		Name: name, Type: t, Mode: mode,
		Sensitive: false, WriteOnly: false, Replace: false, Stable: false, Description: "",
	}
}

func TestValidateResource(t *testing.T) {
	t.Parallel()

	id := attr("id", schema.String(), schema.ModeComputed)

	writeOnlyComputed := attr("token", schema.String(), schema.ModeComputed)
	writeOnlyComputed.WriteOnly = true

	replaceComputed := attr("zone", schema.String(), schema.ModeComputed)
	replaceComputed.Replace = true

	replaceID := attr("id", schema.String(), schema.ModeRequired)
	replaceID.Replace = true

	sensitiveID := id
	sensitiveID.Sensitive = true

	stableComputed := attr("created_at", schema.String(), schema.ModeComputed)
	stableComputed.Stable = true

	stableOptionalComputed := attr("ttl", schema.String(), schema.ModeOptionalComputed)
	stableOptionalComputed.Stable = true

	stableRequired := attr("name", schema.String(), schema.ModeRequired)
	stableRequired.Stable = true

	tests := []struct {
		name  string
		attrs []schema.Attribute
		ok    bool
	}{
		{
			name:  "computed id and a required name",
			attrs: []schema.Attribute{id, attr("name", schema.String(), schema.ModeRequired)},
			ok:    true,
		},
		{name: "required id with replace", attrs: []schema.Attribute{replaceID}, ok: true},
		{
			name:  "required id without replace",
			attrs: []schema.Attribute{attr("id", schema.String(), schema.ModeRequired)},
			ok:    false,
		},
		{name: "optional id", attrs: []schema.Attribute{attr("id", schema.String(), schema.ModeOptional)}, ok: false},
		{
			name:  "optional computed id",
			attrs: []schema.Attribute{attr("id", schema.String(), schema.ModeOptionalComputed)},
			ok:    false,
		},
		{name: "no id", attrs: []schema.Attribute{attr("name", schema.String(), schema.ModeRequired)}, ok: false},
		{name: "numeric id", attrs: []schema.Attribute{attr("id", schema.Number(), schema.ModeComputed)}, ok: false},
		{name: "sensitive id", attrs: []schema.Attribute{sensitiveID}, ok: false},
		{
			name:  "name twice",
			attrs: []schema.Attribute{id, attr("id", schema.String(), schema.ModeComputed)},
			ok:    false,
		},
		{
			name:  "camelCase name",
			attrs: []schema.Attribute{id, attr("teamId", schema.String(), schema.ModeRequired)},
			ok:    false,
		},
		{name: "no mode", attrs: []schema.Attribute{id, attr("name", schema.String(), schema.ModeInvalid)}, ok: false},
		{name: "write-only computed", attrs: []schema.Attribute{id, writeOnlyComputed}, ok: false},
		{name: "replace on computed", attrs: []schema.Attribute{id, replaceComputed}, ok: false},
		{name: "stable computed", attrs: []schema.Attribute{id, stableComputed}, ok: true},
		{name: "stable optional computed", attrs: []schema.Attribute{id, stableOptionalComputed}, ok: true},
		{name: "stable required", attrs: []schema.Attribute{id, stableRequired}, ok: false},
		{
			name: "list without element type",
			attrs: []schema.Attribute{
				id,
				attr("tags", schema.Type{Kind: schema.KindList, Elem: nil}, schema.ModeOptional),
			},
			ok: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := schema.Schema{Version: 0, Attributes: tt.attrs}.ValidateResource()
			if tt.ok && err != nil {
				t.Fatalf("ValidateResource() = %v, want nil", err)
			}

			if !tt.ok && !errors.Is(err, schema.ErrInvalid) {
				t.Fatalf("ValidateResource() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want schema.Type
		ok   bool
	}{
		{in: "string", want: schema.String(), ok: true},
		{in: "number", want: schema.Number(), ok: true},
		{in: "bool", want: schema.Bool(), ok: true},
		{in: "list(string)", want: schema.ListOf(schema.String()), ok: true},
		{in: "map(list(number))", want: schema.MapOf(schema.ListOf(schema.Number())), ok: true},
		{in: "object", want: schema.Type{Kind: schema.KindInvalid, Elem: nil}, ok: false},
		{in: "list(string", want: schema.Type{Kind: schema.KindInvalid, Elem: nil}, ok: false},
		{in: "list()", want: schema.Type{Kind: schema.KindInvalid, Elem: nil}, ok: false},
		{in: "", want: schema.Type{Kind: schema.KindInvalid, Elem: nil}, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := schema.ParseType(tt.in)
			if !tt.ok {
				if !errors.Is(err, schema.ErrInvalid) {
					t.Fatalf("ParseType(%q) error = %v, want ErrInvalid", tt.in, err)
				}

				return
			}

			if err != nil || !got.Equal(tt.want) {
				t.Fatalf("ParseType(%q) = %v, %v, want %v", tt.in, got, err, tt.want)
			}

			if got.String() != tt.in {
				t.Fatalf("String() = %q, want %q", got.String(), tt.in)
			}
		})
	}
}

func TestValueEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b schema.Value
		want bool
	}{
		{name: "same string", a: schema.StringValue("a"), b: schema.StringValue("a"), want: true},
		{name: "other string", a: schema.StringValue("a"), b: schema.StringValue("b"), want: false},
		{name: "same number", a: schema.IntValue(3), b: schema.IntValue(3), want: true},
		{name: "string and number", a: schema.StringValue("3"), b: schema.IntValue(3), want: false},
		{name: "two nulls", a: schema.NullOf(schema.String()), b: schema.NullOf(schema.String()), want: true},
		{name: "null and empty", a: schema.NullOf(schema.String()), b: schema.StringValue(""), want: false},
		{name: "two unknowns", a: schema.UnknownOf(schema.String()), b: schema.UnknownOf(schema.String()), want: false},
		{
			name: "same list",
			a:    schema.ListValue(schema.String(), schema.StringValue("x")),
			b:    schema.ListValue(schema.String(), schema.StringValue("x")),
			want: true,
		},
		{
			name: "map with unknown entry",
			a:    schema.MapValue(schema.String(), map[string]schema.Value{"k": schema.UnknownOf(schema.String())}),
			b:    schema.MapValue(schema.String(), map[string]schema.Value{"k": schema.UnknownOf(schema.String())}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.a.Equal(tt.b); got != tt.want {
				t.Fatalf("Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNumberPrecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   *big.Float
	}{
		{name: "default precision", in: big.NewFloat(0.1)},
		{name: "low precision", in: new(big.Float).SetPrec(8).SetInt64(3)},
		{name: "high precision", in: new(big.Float).SetPrec(2048).SetInt64(math.MaxInt64)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := schema.NumberValue(tt.in).AsNumber()
			if !ok || got.Prec() != schema.NumberPrecision {
				t.Fatalf("AsNumber() precision = %d, %v, want %d", got.Prec(), ok, schema.NumberPrecision)
			}

			if got.Cmp(tt.in) != 0 {
				t.Fatalf("AsNumber() = %s, want %s", got, tt.in)
			}
		})
	}

	if !schema.IntValue(math.MaxInt64).Equal(schema.NumberValue(new(big.Float).SetInt64(math.MaxInt64))) {
		t.Fatal("IntValue and NumberValue of one int64 are not equal")
	}

	high, _, err := new(big.Float).SetPrec(2048).Parse("0.1", 10)
	if err != nil {
		t.Fatal(err)
	}

	low, _, err := new(big.Float).SetPrec(schema.NumberPrecision).Parse("0.1", 10)
	if err != nil {
		t.Fatal(err)
	}

	if !schema.NumberValue(high).Equal(schema.NumberValue(low)) {
		t.Fatal("NumberValue of 0.1 at 2048 bits and at 512 bits are not equal, want one rounding rule")
	}
}

func TestFloatValue(t *testing.T) {
	t.Parallel()

	decimal := func(s string) schema.Value {
		n, _, err := new(big.Float).SetPrec(schema.NumberPrecision).Parse(s, 10)
		if err != nil {
			t.Fatal(err)
		}

		return schema.NumberValue(n)
	}

	tests := []struct {
		name string
		x    float64
		bits int
		want string
	}{
		{name: "float64 0.1", x: 0.1, bits: 64, want: "0.1"},
		{name: "float32 0.1", x: float64(float32(0.1)), bits: 32, want: "0.1"},
		{name: "float64 1e-300", x: 1e-300, bits: 64, want: "1e-300"},
		{name: "float64 whole number", x: 3, bits: 64, want: "3"},
		{
			name: "float64 largest",
			x:    math.MaxFloat64,
			bits: 64,
			want: strconv.FormatFloat(math.MaxFloat64, 'g', -1, 64),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := schema.FloatValue(tt.x, tt.bits)
			if !ok || !got.Equal(decimal(tt.want)) {
				t.Fatalf("FloatValue(%v, %d) = %v, %v, want the decimal %s", tt.x, tt.bits, got, ok, tt.want)
			}
		})
	}

	for _, x := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, ok := schema.FloatValue(x, 64); ok {
			t.Errorf("FloatValue(%v) ok, want false", x)
		}
	}

	if _, ok := schema.FloatValue(1, 16); ok {
		t.Error("FloatValue(1, 16) ok, want false")
	}
}
