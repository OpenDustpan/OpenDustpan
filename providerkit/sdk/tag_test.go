package sdk_test

import (
	"errors"
	"testing"

	"opendustpan.org/providerkit/schema"
	"opendustpan.org/providerkit/sdk"
)

func TestParseTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tag       string
		mode      schema.Mode
		sensitive bool
		writeOnly bool
		replace   bool
		ok        bool
	}{
		{tag: "id,computed", mode: schema.ModeComputed, sensitive: false, writeOnly: false, replace: false, ok: true},
		{
			tag:       "name,required,replace",
			mode:      schema.ModeRequired,
			sensitive: false,
			writeOnly: false,
			replace:   true,
			ok:        true,
		},
		{
			tag:       "ttl,optional,computed",
			mode:      schema.ModeOptionalComputed,
			sensitive: false,
			writeOnly: false,
			replace:   false,
			ok:        true,
		},
		{
			tag:       "token,optional,sensitive,writeonly",
			mode:      schema.ModeOptional,
			sensitive: true,
			writeOnly: true,
			replace:   false,
			ok:        true,
		},
		{tag: "name", mode: schema.ModeInvalid, sensitive: false, writeOnly: false, replace: false, ok: false},
		{
			tag:       "name,required,optional",
			mode:      schema.ModeInvalid,
			sensitive: false,
			writeOnly: false,
			replace:   false,
			ok:        false,
		},
		{
			tag:       "name,required,required",
			mode:      schema.ModeInvalid,
			sensitive: false,
			writeOnly: false,
			replace:   false,
			ok:        false,
		},
		{
			tag:       "created,computed,stable",
			mode:      schema.ModeComputed,
			sensitive: false,
			writeOnly: false,
			replace:   false,
			ok:        true,
		},
		{tag: "name,secret", mode: schema.ModeInvalid, sensitive: false, writeOnly: false, replace: false, ok: false},
		{tag: "Name,required", mode: schema.ModeInvalid, sensitive: false, writeOnly: false, replace: false, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			t.Parallel()

			a, err := sdk.ParseTag(tt.tag)
			if !tt.ok {
				if !errors.Is(err, sdk.ErrInvalidModel) {
					t.Fatalf("ParseTag(%q) error = %v, want ErrInvalidModel", tt.tag, err)
				}

				return
			}

			if err != nil || a.Mode != tt.mode || a.Sensitive != tt.sensitive || a.WriteOnly != tt.writeOnly ||
				a.Replace != tt.replace {
				t.Fatalf("ParseTag(%q) = %+v, %v", tt.tag, a, err)
			}
		})
	}
}

type override struct {
	ID       *string            `provider:"id,computed"`
	Schedule string             `provider:"schedule,required,replace"`
	People   []string           `provider:"people,required"`
	Labels   *map[string]string `provider:"labels,optional"`
	Hours    *int               `provider:"hours,optional,computed"`
	Token    *string            `provider:"token,optional,sensitive,writeonly"`
	note     string
}

type badField struct {
	ID   *string         `provider:"id,computed"`
	Data *chan<- float64 `provider:"data,optional"`
}

type badKey struct {
	ID   *string         `provider:"id,computed"`
	Data *map[int]string `provider:"data,optional"`
}

type optionalNotPointer struct {
	ID   *string `provider:"id,computed"`
	Team string  `provider:"team,optional"`
}

type computedNotPointer struct {
	ID string `provider:"id,computed"`
}

type requiredPointer struct {
	ID   *string `provider:"id,computed"`
	Name *string `provider:"name,required"`
}

type pointerElements struct {
	ID    *string   `provider:"id,computed"`
	Names []*string `provider:"names,required"`
}

type stableRequired struct {
	ID   *string `provider:"id,computed"`
	Name string  `provider:"name,required,stable"`
}

func TestSchemaOf(t *testing.T) {
	t.Parallel()

	s, err := sdk.SchemaOf[override]()
	if err != nil {
		t.Fatalf("SchemaOf() error = %v", err)
	}

	if err := s.ValidateResource(); err != nil {
		t.Fatalf("ValidateResource() = %v", err)
	}

	want := map[string]schema.Type{
		"id": schema.String(), "schedule": schema.String(), "people": schema.ListOf(schema.String()),
		"labels": schema.MapOf(schema.String()), "hours": schema.Number(), "token": schema.String(),
	}

	if len(s.Attributes) != len(want) {
		t.Fatalf("SchemaOf() has %d attributes, want %d", len(s.Attributes), len(want))
	}

	for _, a := range s.Attributes {
		if !a.Type.Equal(want[a.Name]) {
			t.Errorf("attribute %q type = %s, want %s", a.Name, a.Type, want[a.Name])
		}
	}

	_ = override{ID: nil, Schedule: "", People: nil, Labels: nil, Hours: nil, Token: nil, note: ""}
	_ = optionalNotPointer{ID: nil, Team: ""}
	_ = computedNotPointer{ID: ""}
	_ = requiredPointer{ID: nil, Name: nil}
	_ = pointerElements{ID: nil, Names: nil}
	_ = stableRequired{ID: nil, Name: ""}
}

func TestSchemaOfRefuses(t *testing.T) {
	t.Parallel()

	for name, f := range map[string]func() (schema.Schema, error){
		"not a struct":                 sdk.SchemaOf[string],
		"channel field":                sdk.SchemaOf[badField],
		"integer keys":                 sdk.SchemaOf[badKey],
		"optional field is no pointer": sdk.SchemaOf[optionalNotPointer],
		"computed field is no pointer": sdk.SchemaOf[computedNotPointer],
		"required field is a pointer":  sdk.SchemaOf[requiredPointer],
		"slice of pointers":            sdk.SchemaOf[pointerElements],
		"stable on a required field":   sdk.SchemaOf[stableRequired],
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := f(); !errors.Is(err, sdk.ErrInvalidModel) {
				t.Fatalf("SchemaOf() error = %v, want ErrInvalidModel", err)
			}
		})
	}
}
