package provider_test

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/provider"
)

func named(name string, k provider.Kind) *fake {
	f := newLogin(nil, nil)
	f.name, f.kind = name, k

	return f
}

func TestNewRegistry(t *testing.T) {
	t.Parallel()

	withSpec := func(mod func(s *provider.Spec)) *fake {
		f := newLogin(nil, nil)
		mod(&f.spec)

		return f
	}

	for _, tc := range []struct {
		want error
		name string
		ps   []provider.Provider
	}{
		{nil, "one of each kind", []provider.Provider{newLogin(nil, nil), newSecrets(nil, nil)}},
		{profile.ErrConflict, "two with one use", []provider.Provider{newLogin(nil, nil), newLogin(nil, nil)}},
		{profile.ErrInvalid, "nil", []provider.Provider{nil}},
		{profile.ErrInvalid, "bad name", []provider.Provider{named("a/b", provider.KindLogin)}},
		{profile.ErrInvalid, "empty name", []provider.Provider{named("", provider.KindLogin)}},
		{profile.ErrInvalid, "unknown kind", []provider.Provider{named("x", "rotate")}},
		{profile.ErrInvalid, "login with no secret output", []provider.Provider{withSpec(func(s *provider.Spec) {
			s.Outputs = []provider.Field{field("note", provider.TypeString, false, false)}
		})}},
		{profile.ErrInvalid, "input repeats", []provider.Provider{withSpec(func(s *provider.Spec) {
			s.Inputs = append(s.Inputs, s.Inputs[0])
		})}},
		{profile.ErrInvalid, "unknown type", []provider.Provider{withSpec(func(s *provider.Spec) {
			s.Inputs[0].Type = "date"
		})}},
		{profile.ErrInvalid, "no description", []provider.Provider{withSpec(func(s *provider.Spec) {
			s.Outputs[0].Description = ""
		})}},
		{profile.ErrInvalid, "field name with a space", []provider.Provider{withSpec(func(s *provider.Spec) {
			s.Inputs[0].Name = "a b"
		})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := provider.NewRegistry(clock, tc.ps...)
			if !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
				t.Errorf("NewRegistry = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	t.Parallel()

	reg := registry(t, newLogin(nil, nil), newSecrets(nil, nil))

	if got := reg.Uses(); len(got) != 2 || got[0] != "login/test" || got[1] != "secrets/test" {
		t.Errorf("Uses = %v", got)
	}

	for _, tc := range []struct {
		use string
		ok  bool
	}{
		{"login/test", true},
		{"secrets/test", true},
		{"outputs/test", false},
		{"login", false},
		{"login/", false},
		{"/test", false},
		{"login/test/x", false},
		{"rotate/test", false},
		{"login/test\n", false},
	} {
		t.Run(tc.use, func(t *testing.T) {
			t.Parallel()

			kind, spec, err := reg.Spec(tc.use)
			if tc.ok != (err == nil) || tc.ok != (kind != "") || tc.ok != (len(spec.Inputs) > 0) {
				t.Fatalf("Spec = %q, %v, %v, want found: %v", kind, spec, err, tc.ok)
			}

			if !tc.ok && (!errors.Is(err, profile.ErrInvalid) || !errors.Is(err, provider.ErrUnknownProvider)) {
				t.Errorf("Spec = %v, want profile.ErrInvalid and provider.ErrUnknownProvider", err)
			}
		})
	}
}

func TestCheckInputs(t *testing.T) {
	t.Parallel()

	spec := provider.Spec{
		Inputs: []provider.Field{
			field("s", provider.TypeString, true, false),
			field("n", provider.TypeNumber, false, false),
			field("b", provider.TypeBoolean, false, false),
			field("l", provider.TypeList, false, false),
			field("m", provider.TypeMap, false, false),
			field("d", provider.TypeDuration, false, false),
			field("k", provider.TypeString, false, true),
		},
		Outputs: nil, MintsTokens: false,
	}

	leaf := func(v any) provider.Leaf { return provider.Leaf{Value: v, Secret: false} }
	base := func(extra provider.Value) provider.Value {
		in := provider.Value{"s": leaf("x")}
		maps.Copy(in, extra)

		return in
	}

	for _, tc := range []struct {
		in   provider.Value
		name string
		ok   bool
	}{
		{base(nil), "only required", true},
		{base(provider.Value{
			"n": leaf(1.5), "b": leaf(true), "l": leaf([]any{"a"}), "m": leaf(map[string]any{"a": 1}), "d": leaf("15m"),
			"k": {Value: "secret", Secret: true},
		}), "every type", true},
		{base(provider.Value{"n": leaf(int64(3))}), "int64 number", true},
		{base(provider.Value{"s": {Value: "x", Secret: true}}), "secret value for a plain input", true},
		{provider.Value{}, "missing required", false},
		{base(provider.Value{"z": leaf("x")}), "unknown input", false},
		{base(provider.Value{"s": leaf(1)}), "number for a string", false},
		{base(provider.Value{"n": leaf("1")}), "string for a number", false},
		{base(provider.Value{"b": leaf("true")}), "string for a boolean", false},
		{base(provider.Value{"l": leaf("a")}), "string for a list", false},
		{base(provider.Value{"m": leaf([]any{})}), "list for a map", false},
		{base(provider.Value{"d": leaf("soon")}), "text for a duration", false},
		{base(provider.Value{"d": leaf("-1m")}), "negative duration", false},
		{base(provider.Value{"d": leaf(60)}), "number for a duration", false},
		{base(provider.Value{"k": leaf("plain")}), "plain value for a secret input", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := provider.CheckInputs(&spec, tc.in)
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, profile.ErrInvalid)) {
				t.Errorf("CheckInputs = %v, want ok: %v", err, tc.ok)
			}
		})
	}
}

func TestCheckInputsDoesNotEchoAValue(t *testing.T) {
	t.Parallel()

	spec := provider.Spec{Inputs: nil, Outputs: nil, MintsTokens: false}

	err := provider.CheckInputs(&spec, provider.Value{"token=s3cr3t\n": {Value: "s3cr3t", Secret: true}})
	if err == nil {
		t.Fatal("CheckInputs accepted an unknown input")
	}

	if msg := err.Error(); strings.Contains(msg, "s3cr3t") || strings.Contains(msg, "\n") {
		t.Errorf("the error echoes the input: %q", msg)
	}
}

func TestKind(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		k  provider.Kind
		ok bool
	}{{provider.KindLogin, true}, {provider.KindSecrets, true}, {provider.KindOutputs, true}, {"rotate", false}, {"", false}} {
		if tc.k.Valid() != tc.ok {
			t.Errorf("Kind(%q).Valid() = %v", tc.k, !tc.ok)
		}
	}
}
