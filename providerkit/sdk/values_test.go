package sdk_test

import (
	"errors"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
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

type wide struct {
	Signed   *int64   `provider:"signed,optional"`
	Unsigned *uint64  `provider:"unsigned,optional"`
	Single   *float32 `provider:"single,optional"`
	Double   *float64 `provider:"double,optional"`
}

func TestDecodeWideIntegers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		attr string
		v    string
		ok   bool
	}{
		{name: "2^53+1 to int64", attr: "signed", v: "9007199254740993", ok: true},
		{name: "2^63-1 to int64", attr: "signed", v: "9223372036854775807", ok: true},
		{name: "2^63 to int64", attr: "signed", v: "9223372036854775808", ok: false},
		{name: "-2^63 to int64", attr: "signed", v: "-9223372036854775808", ok: true},
		{name: "-2^63-1 to int64", attr: "signed", v: "-9223372036854775809", ok: false},
		{name: "2^64-1 to int64", attr: "signed", v: "18446744073709551615", ok: false},
		{name: "2^53+1 to uint64", attr: "unsigned", v: "9007199254740993", ok: true},
		{name: "2^64-1 to uint64", attr: "unsigned", v: "18446744073709551615", ok: true},
		{name: "2^64 to uint64", attr: "unsigned", v: "18446744073709551616", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := sdk.Decode[wide](schema.Values{tt.attr: num(tt.v)})
			if !tt.ok {
				if !errors.Is(err, lifecycle.ErrInvalidConfig) {
					t.Fatalf("Decode() error = %v, want ErrInvalidConfig", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}

			if text := wideText(got); text != tt.v {
				t.Fatalf("Decode() = %s, want exactly %s", text, tt.v)
			}
		})
	}
}

// wideText returns the integer that w holds, in decimal.
func wideText(w wide) string {
	switch {
	case w.Signed != nil:
		return strconv.FormatInt(*w.Signed, 10)
	case w.Unsigned != nil:
		return strconv.FormatUint(*w.Unsigned, 10)
	default:
		return ""
	}
}

// TestFloatRoundTrip checks that a float from a model equals the decimal of
// the configuration: else the plan after an apply shows a change.
func TestFloatRoundTrip(t *testing.T) {
	t.Parallel()

	single, double := float32(0.1), 0.1

	vs, err := sdk.Encode(wide{Signed: nil, Unsigned: nil, Single: &single, Double: &double})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	for _, name := range []string{"single", "double"} {
		if !vs[name].Equal(num("0.1")) {
			t.Errorf("Encode() of the %s float 0.1 is not equal to the decimal 0.1", name)
		}
	}

	got, err := sdk.Decode[wide](schema.Values{"single": num("0.1"), "double": num("0.1")})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if got.Single == nil || *got.Single != single || got.Double == nil || *got.Double != double {
		t.Fatal("Decode() of the decimal 0.1 is not the float 0.1")
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

	dir := filepath.Join(t.TempDir(), "session")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	c, err := sdk.NewCall(config{Endpoint: "", Token: ""}, nil, dir)
	if err != nil {
		t.Fatalf("NewCall() error = %v", err)
	}

	seen := map[string]bool{}

	for _, value := range []string{"t0ken", "", "line one\nline two"} {
		path := checkSecretFile(t, c, dir, value)
		if seen[path] {
			t.Fatalf("SecretFile() = %q twice, want a new file", path)
		}

		seen[path] = true
	}

	none, err := sdk.NewCall(config{Endpoint: "", Token: ""}, nil, "")
	if err != nil {
		t.Fatalf("NewCall() without a directory error = %v", err)
	}

	if _, _, err := none.SecretFile("x"); !errors.Is(err, sdk.ErrNoSessionDir) {
		t.Fatalf("SecretFile() without a directory error = %v, want ErrNoSessionDir", err)
	}
}

// checkSecretFile writes value with SecretFile, checks the file, and removes
// it. It returns the path.
func checkSecretFile(t *testing.T, c sdk.Call[config], dir, value string) string {
	t.Helper()

	path, remove, err := c.SecretFile(value)
	if err != nil {
		t.Fatalf("SecretFile() error = %v", err)
	}

	if filepath.Dir(path) != dir {
		t.Fatalf("SecretFile() = %q, want a file in %q", path, dir)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode = %v, %v, want 0600", info.Mode().Perm(), err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // A file of this test.
	if err != nil || string(got) != value {
		t.Fatalf("secret file holds %q, %v, want the value", got, err)
	}

	if err := remove(); err != nil {
		t.Fatalf("remove() error = %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secret file after remove() = %v, want it gone", err)
	}

	if err := remove(); err != nil {
		t.Fatalf("second remove() error = %v, want nil", err)
	}

	return path
}

func TestNewCallRefusesOpenDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	//nolint:gosec // The test needs a directory that other users can read.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := sdk.NewCall(config{Endpoint: "", Token: ""}, nil, dir); !errors.Is(err, sdk.ErrSessionDirMode) {
		t.Fatalf("NewCall() with a directory of mode 0755 error = %v, want ErrSessionDirMode", err)
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := sdk.NewCall(config{Endpoint: "", Token: ""}, nil, file); !errors.Is(err, sdk.ErrSessionDirMode) {
		t.Fatalf("NewCall() with a file error = %v, want ErrSessionDirMode", err)
	}
}

type computedConfig struct {
	Region *string `provider:"region,computed"`
}

type numberSecretConfig struct {
	Pin int `provider:"pin,required,sensitive"`
}

type writeOnlyResult struct {
	Name string  `provider:"name,required"`
	Key  *string `provider:"key,optional,writeonly"`
}

type stableResult struct {
	Name    string  `provider:"name,required"`
	Created *string `provider:"created,computed,stable"`
}

type replaceInput struct {
	Name string `provider:"name,required,replace"`
}

func TestSchemaRulesOfConfigAndDataSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		f    func() error
	}{
		{name: "computed configuration field", f: func() error {
			_, err := sdk.ConfigSchemaOf[computedConfig]()

			return err
		}},
		{name: "sensitive configuration number", f: func() error {
			_, err := sdk.ConfigSchemaOf[numberSecretConfig]()

			return err
		}},
		{name: "write-only data source field", f: func() error {
			_, err := sdk.DataSourceSchemaOf[writeOnlyResult]()

			return err
		}},
		{name: "stable data source field", f: func() error {
			_, err := sdk.DataSourceSchemaOf[stableResult]()

			return err
		}},
		{name: "replace data source field", f: func() error {
			_, err := sdk.DataSourceSchemaOf[replaceInput]()

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.f(); !errors.Is(err, sdk.ErrInvalidModel) {
				t.Fatalf("error = %v, want ErrInvalidModel", err)
			}
		})
	}

	if _, err := sdk.DataSourceSchemaOf[record](); err != nil {
		t.Fatalf("DataSourceSchemaOf() of a plain model error = %v", err)
	}

	_ = computedConfig{Region: nil}
	_ = numberSecretConfig{Pin: 0}
	_ = writeOnlyResult{Name: "", Key: nil}
	_ = stableResult{Name: "", Created: nil}
	_ = replaceInput{Name: ""}
}
