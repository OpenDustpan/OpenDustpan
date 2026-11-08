package cedargo_test

import (
	"maps"
	"testing"

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
			name: "a set that mixes a group and a principal counts both changes",
			text: `forbid (principal, action, resource) when { principal in [` +
				`OpenDustpan::Group::"sre", OpenDustpan::Principal::"x"] };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "a right side that is no group counts both changes",
			text: `forbid (principal, action, resource) when { principal in ` +
				`(if true then OpenDustpan::Group::"sre" else OpenDustpan::Group::"sre") };`,
			want: map[string]cedargo.GroupDirections{"sre": {Add: true, Remove: true}},
		},
		{
			name: "a principal entity in the principal scope is not a group",
			text: `forbid (principal == OpenDustpan::Principal::"sre", action, resource);`,
			want: map[string]cedargo.GroupDirections{},
		},
		{
			name: "a group of another name is not the group sre",
			text: `forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"platform" };`,
			want: map[string]cedargo.GroupDirections{"platform": {Add: true, Remove: false}},
		},
		{
			name: "a principal entity is not a group",
			text: `forbid (principal, action, resource) when { principal in OpenDustpan::Principal::"sre" };`,
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

// TestLimitGroupDirectionsErrors checks the refusal of a text that the
// engine does not parse, and of a text that holds a count of policies other
// than one.
func TestLimitGroupDirectionsErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
	}{
		{
			name: "a parse error",
			text: `forbid (principal, action, resource) unless { principal in`,
		},
		{
			name: "two policies",
			text: `forbid (principal, action, resource); forbid (principal, action, resource);`,
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
		})
	}
}
