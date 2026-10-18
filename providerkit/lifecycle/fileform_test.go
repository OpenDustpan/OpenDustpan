package lifecycle_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/providerkit/lifecycle"
	"opendustpan.org/providerkit/schema"
)

func secret(name string, mode schema.Mode) schema.Attribute {
	a := attr(name, mode)
	a.Sensitive = true

	return a
}

func numberSecret(name string) schema.Attribute {
	a := secret(name, schema.ModeRequired)
	a.Type = schema.Number()

	return a
}

func TestWithFileForms(t *testing.T) {
	t.Parallel()

	base := schema.Schema{Version: 0, Attributes: []schema.Attribute{
		attr("endpoint", schema.ModeRequired),
		secret("token", schema.ModeRequired),
		secret("password", schema.ModeOptional),
	}}

	got, err := lifecycle.WithFileForms(base)
	if err != nil {
		t.Fatalf("WithFileForms() error = %v", err)
	}

	want := []struct {
		name      string
		mode      schema.Mode
		sensitive bool
	}{
		{name: "endpoint", mode: schema.ModeRequired, sensitive: false},
		{name: "token", mode: schema.ModeOptional, sensitive: true},
		{name: "token_file", mode: schema.ModeOptional, sensitive: false},
		{name: "password", mode: schema.ModeOptional, sensitive: true},
		{name: "password_file", mode: schema.ModeOptional, sensitive: false},
	}

	if len(got.Attributes) != len(want) {
		t.Fatalf("WithFileForms() has %d attributes, want %d", len(got.Attributes), len(want))
	}

	for i, w := range want {
		a := got.Attributes[i]
		if a.Name != w.name || a.Mode != w.mode || a.Sensitive != w.sensitive || !a.Type.Equal(schema.String()) {
			t.Errorf("attribute %d = %s %s sensitive=%v, want %s %s sensitive=%v",
				i, a.Name, a.Mode, a.Sensitive, w.name, w.mode, w.sensitive)
		}
	}

	if base.Attributes[1].Mode != schema.ModeRequired {
		t.Fatal("WithFileForms() changed its input")
	}
}

func TestWithFileFormsRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		attrs []schema.Attribute
	}{
		{name: "file form is taken", attrs: []schema.Attribute{
			secret("token", schema.ModeRequired), attr("token_file", schema.ModeOptional),
		}},
		{name: "file form is longer than 64 characters", attrs: []schema.Attribute{
			secret(strings.Repeat("t", 60), schema.ModeRequired),
		}},
		{name: "invalid base", attrs: []schema.Attribute{secret("Token", schema.ModeRequired)}},
		{name: "computed attribute", attrs: []schema.Attribute{attr("region", schema.ModeComputed)}},
		{name: "optional computed secret", attrs: []schema.Attribute{secret("token", schema.ModeOptionalComputed)}},
		{name: "secret that is not a string", attrs: []schema.Attribute{numberSecret("pin")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := lifecycle.WithFileForms(schema.Schema{Version: 0, Attributes: tt.attrs})
			if !errors.Is(err, schema.ErrInvalid) {
				t.Fatalf("WithFileForms() error = %v, want schema.ErrInvalid", err)
			}
		})
	}

	longest := schema.Schema{
		Version:    0,
		Attributes: []schema.Attribute{secret(strings.Repeat("t", 59), schema.ModeRequired)},
	}
	if _, err := lifecycle.WithFileForms(longest); err != nil {
		t.Fatalf("WithFileForms() of a 64-character file form error = %v, want nil", err)
	}
}

func TestCheckFileForms(t *testing.T) {
	t.Parallel()

	base := schema.Schema{Version: 0, Attributes: []schema.Attribute{
		secret("token", schema.ModeRequired),
		secret("password", schema.ModeOptional),
	}}
	null := schema.NullOf(schema.String())

	tests := []struct {
		name   string
		config schema.Values
		env    bool
		ok     bool
	}{
		{name: "value", config: schema.Values{"token": str("t")}, env: false, ok: true},
		{name: "file form", config: schema.Values{"token_file": str("/run/token")}, env: false, ok: true},
		{
			name:   "file form and a null value",
			config: schema.Values{"token": null, "token_file": str("/t")},
			env:    false,
			ok:     true,
		},
		{name: "neither of a required one", config: schema.Values{}, env: false, ok: false},
		{name: "only the variable of a required one", config: schema.Values{"token": null}, env: true, ok: true},
		{
			name:   "both nulls of a required one",
			config: schema.Values{"token": null, "token_file": null},
			env:    false,
			ok:     false,
		},
		{
			name:   "both",
			config: schema.Values{"token": str("t"), "token_file": str("/run/token")},
			env:    false,
			ok:     false,
		},
		{
			name:   "both of an optional one",
			config: schema.Values{"token": str("t"), "password": str("p"), "password_file": str("/p")},
			env:    false,
			ok:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := lifecycle.CheckFileForms(base, tt.config, func(string) bool { return tt.env })
			if tt.ok && err != nil {
				t.Fatalf("CheckFileForms() = %v, want nil", err)
			}

			if !tt.ok && !errors.Is(err, lifecycle.ErrInvalidConfig) {
				t.Fatalf("CheckFileForms() = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestFileEnv(t *testing.T) {
	t.Parallel()

	if got := lifecycle.FileEnv("ipam", "api_token"); got != "IPAM_API_TOKEN_FILE" {
		t.Fatalf("FileEnv() = %q, want %q", got, "IPAM_API_TOKEN_FILE")
	}
}
