package cedargo_test

import (
	"errors"
	"maps"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
)

// TestLimitGroupDirections checks, for each position of a group in the text
// of an access limit, the changes of its members that can lift the forbid.
func TestLimitGroupDirections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want map[string]cedargo.GroupDirections
	}{
		{
			name: "a when clause makes a remove lift the forbid",
			text: `forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: false, Remove: true}},
		},
		{
			name: "an unless clause makes an add lift the forbid",
			text: `forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: false}},
		},
		{
			name: "the principal scope makes a remove lift the forbid",
			text: `forbid (principal in OpenDustpan::Group::"sre", action, resource);`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: false, Remove: true}},
		},
		{
			name: "the is-in principal scope makes a remove lift the forbid",
			text: `forbid (principal is Principal in OpenDustpan::Group::"sre", action, resource);`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: false, Remove: true}},
		},
		{
			name: "an is-in clause makes a remove lift the forbid",
			text: `forbid (principal, action, resource) when { principal is Principal in OpenDustpan::Group::"sre" };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: false, Remove: true}},
		},
		{
			name: "a principal scope of == counts both changes",
			text: `forbid (principal == OpenDustpan::Group::"sre", action, resource);`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "a ! in a when clause makes an add lift the forbid",
			text: `forbid (principal, action, resource) when { !(principal in OpenDustpan::Group::"sre") };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: false}},
		},
		{
			name: "a ! in an unless clause makes a remove lift the forbid",
			text: `forbid (principal, action, resource) unless { !(principal in OpenDustpan::Group::"sre") };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: false, Remove: true}},
		},
		{
			name: "a second ! restores the polarity of the clause",
			text: `forbid (principal, action, resource) when { !!(principal in OpenDustpan::Group::"sre") };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: false, Remove: true}},
		},
		{
			name: "&& keeps the polarity",
			text: `forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" && ` +
				`principal in OpenDustpan::Group::"platform" };`,
			want: map[string]cedargo.GroupDirections{
				"sre": {Add: false, Remove: true}, "platform": {Add: false, Remove: true},
			},
		},
		{
			name: "|| keeps the polarity",
			text: `forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" || ` +
				`principal in OpenDustpan::Group::"platform" };`,
			want: map[string]cedargo.GroupDirections{
				"sre": {Add: true, Remove: false}, "platform": {Add: true, Remove: false},
			},
		},
		{
			name: "an if-then-else counts both changes",
			text: `forbid (principal, action, resource) when { if principal in OpenDustpan::Group::"sre" then true else false };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "== in a clause counts both changes",
			text: `forbid (principal, action, resource) when { principal == OpenDustpan::Group::"sre" };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "!= in a clause counts both changes",
			text: `forbid (principal, action, resource) when { principal != OpenDustpan::Group::"sre" };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "a contains call counts both changes",
			text: `forbid (principal, action, resource) when { OpenDustpan::Group::"sre".contains(principal) };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "the left side of an in counts both changes",
			text: `forbid (principal, action, resource) when { OpenDustpan::Group::"sre" in [OpenDustpan::Group::"sre"] };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "the action scope counts both changes",
			text: `forbid (principal, action in OpenDustpan::Group::"sre", resource);`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "the resource scope counts both changes",
			text: `forbid (principal, action, resource in OpenDustpan::Group::"sre");`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "a set in a clause makes a remove lift the forbid",
			text: `forbid (principal, action, resource) when { principal in [` +
				`OpenDustpan::Group::"sre", OpenDustpan::Group::"platform"] };`,
			want: map[string]cedargo.GroupDirections{
				"sre": {Add: false, Remove: true}, "platform": {Add: false, Remove: true},
			},
		},
		{
			name: "a group of another name is not the group sre",
			text: `forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"platform" };`,
			want: map[string]cedargo.GroupDirections{"platform": {Add: true, Remove: false}},
		},
		{
			name: "a principal entity in the principal scope is not a group",
			text: `forbid (principal == OpenDustpan::Principal::"sre", action, resource);`,
			want: map[string]cedargo.GroupDirections{},
		},
		{
			name: "a clause that names no group names none",
			text: `forbid (principal, action, resource) when { context.principal.kind == "service" };`,
			want: map[string]cedargo.GroupDirections{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cedargo.LimitGroupDirections(tc.text)
			if err != nil {
				t.Fatalf("LimitGroupDirections() = %v, want nil", err)
			}

			if !maps.Equal(got, tc.want) {
				t.Fatalf("LimitGroupDirections() = %v, want %v", got, tc.want)
			}
		})
	}
}

// gateDirectionCase is one answer of GateGroupDirections that a test checks.
type gateDirectionCase struct {
	name      string
	condition string
	unless    string
	want      map[string]cedargo.GroupDirections
	wantErr   bool
	// membership reports whether the error fails the membership rule.
	membership bool
}

// check checks the answer of GateGroupDirections for the case.
func (c gateDirectionCase) check(t *testing.T) {
	t.Helper()

	got, err := cedargo.GateGroupDirections(c.condition, c.unless)
	if !c.wantErr {
		if err != nil {
			t.Fatalf("GateGroupDirections(%q, %q) = %v, want nil", c.condition, c.unless, err)
		}

		if !maps.Equal(got, c.want) {
			t.Fatalf("GateGroupDirections(%q, %q) = %v, want %v", c.condition, c.unless, got, c.want)
		}

		return
	}

	if err == nil {
		t.Fatalf("GateGroupDirections(%q, %q) = %v, nil, want an error", c.condition, c.unless, got)
	}

	if got != nil {
		t.Fatalf("GateGroupDirections(%q, %q) = %v with the error %v, want nil", c.condition, c.unless, got, err)
	}

	if errors.Is(err, gate.ErrLimitMembership) != c.membership {
		t.Fatalf("GateGroupDirections(%q, %q) = %v, want the membership rule %t",
			c.condition, c.unless, err, c.membership)
	}
}

// TestGateGroupDirections checks, for each position of a group in the
// condition or the unless of a gate, the changes of its members that can
// lift the gate.
func TestGateGroupDirections(t *testing.T) {
	t.Parallel()

	cases := []gateDirectionCase{
		{
			name:       "a group in a when clause makes a remove lift the gate",
			condition:  `principal in OpenDustpan::Group::"sre"`,
			unless:     "",
			want:       map[string]cedargo.GroupDirections{"sre": {Add: false, Remove: true}},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a group in an unless clause makes an add lift the gate",
			condition:  "",
			unless:     `principal in OpenDustpan::Group::"sre"`,
			want:       map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: false}},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a ! in a when clause makes an add lift the gate",
			condition:  `!(principal in OpenDustpan::Group::"sre")`,
			unless:     "",
			want:       map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: false}},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a ! in an unless clause makes a remove lift the gate",
			condition:  "",
			unless:     `!(principal in OpenDustpan::Group::"sre")`,
			want:       map[string]cedargo.GroupDirections{"sre": {Add: false, Remove: true}},
			wantErr:    false,
			membership: false,
		},
		{
			name:      "a set of groups makes a remove lift the gate",
			condition: `principal in [OpenDustpan::Group::"sre", OpenDustpan::Group::"platform"]`,
			unless:    "",
			want: map[string]cedargo.GroupDirections{
				"sre": {Add: false, Remove: true}, "platform": {Add: false, Remove: true},
			},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "empty clauses name no group",
			condition:  "",
			unless:     "",
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a clause that does not parse is an error",
			condition:  `principal in`,
			unless:     "",
			want:       nil,
			wantErr:    true,
			membership: false,
		},
		{
			name:       "a right side that holds no group literal names no group",
			condition:  `principal in resource.owners`,
			unless:     "",
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that is the principal variable names no group",
			condition:  `principal in principal`,
			unless:     "",
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that is the resource variable names no group",
			condition:  `principal in resource`,
			unless:     "",
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that is a set of the principal variable names no group",
			condition:  `principal in [principal]`,
			unless:     "",
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that is a principal entity names no group",
			condition:  `principal in OpenDustpan::Principal::"olga"`,
			unless:     "",
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that is the principal variable in the unless names no group",
			condition:  "",
			unless:     `principal in principal`,
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that is the resource variable in the unless names no group",
			condition:  "",
			unless:     `principal in resource`,
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that is a set of the principal variable in the unless names no group",
			condition:  "",
			unless:     `principal in [principal]`,
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that is a principal entity in the unless names no group",
			condition:  "",
			unless:     `principal in OpenDustpan::Principal::"olga"`,
			want:       map[string]cedargo.GroupDirections{},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that names no group does not hide the group of another test",
			condition:  `principal in principal && principal in OpenDustpan::Group::"sre"`,
			unless:     "",
			want:       map[string]cedargo.GroupDirections{"sre": {Add: false, Remove: true}},
			wantErr:    false,
			membership: false,
		},
		{
			name:       "a right side that holds a group in another shape is an error",
			condition:  `principal in (if true then OpenDustpan::Group::"sre" else OpenDustpan::Group::"sre")`,
			unless:     "",
			want:       nil,
			wantErr:    true,
			membership: true,
		},
		{
			name:       "a set that mixes a group and a principal is an error",
			condition:  `principal in [OpenDustpan::Group::"sre", OpenDustpan::Principal::"olga"]`,
			unless:     "",
			want:       nil,
			wantErr:    true,
			membership: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t)
		})
	}
}

// TestLimitGroupDirectionsErrors checks the refusal of a text that the
// engine does not parse, of a text that holds a count of policies other
// than one, and of a membership test of principal that names a value that
// is not a group and not a set of groups. Only the last one wraps
// gate.ErrLimitMembership, which the save and the group guard answer with
// their own code.
func TestLimitGroupDirectionsErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		// membership reports whether the text fails the membership rule
		// rather than a check of the text itself.
		membership bool
	}{
		{
			name:       "a parse error",
			text:       `forbid (principal, action, resource) unless { principal in`,
			membership: false,
		},
		{
			name:       "two policies",
			text:       `forbid (principal, action, resource); forbid (principal, action, resource);`,
			membership: false,
		},
		{
			name:       "a principal entity in a clause",
			text:       `forbid (principal, action, resource) when { principal in OpenDustpan::Principal::"sre" };`,
			membership: true,
		},
		{
			name:       "a principal entity in the principal scope",
			text:       `forbid (principal in OpenDustpan::Principal::"sre", action, resource);`,
			membership: true,
		},
		{
			name:       "an attribute of the resource",
			text:       `forbid (principal, action, resource) when { principal in resource.owners };`,
			membership: true,
		},
		{
			name:       "an attribute of the resource inside an if",
			text:       `forbid (principal, action, resource) when { if principal in resource.owners then true else false };`,
			membership: true,
		},
		{
			name: "a set that mixes a group and a principal",
			text: `forbid (principal, action, resource) when { principal in [` +
				`OpenDustpan::Group::"sre", OpenDustpan::Principal::"x"] };`,
			membership: true,
		},
		{
			name: "a right side that is no group",
			text: `forbid (principal, action, resource) when { principal in ` +
				`(if true then OpenDustpan::Group::"sre" else OpenDustpan::Group::"sre") };`,
			membership: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cedargo.LimitGroupDirections(tc.text)
			if err == nil {
				t.Fatalf("LimitGroupDirections() = %v, nil, want an error", got)
			}

			if got != nil {
				t.Fatalf("LimitGroupDirections() = %v with the error %v, want nil", got, err)
			}

			if errors.Is(err, gate.ErrLimitMembership) != tc.membership {
				t.Fatalf("LimitGroupDirections() = %v, want the membership rule %t", err, tc.membership)
			}
		})
	}
}
