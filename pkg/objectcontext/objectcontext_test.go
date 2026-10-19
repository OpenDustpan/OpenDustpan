package objectcontext_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

//nolint:gochecknoglobals // A fixed test unit that several tests read.
var unit = graph.Unit{Namespace: "acme", Tool: "pulumi", ID: "stk-1"}

func TestRefValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ref  objectcontext.Ref
		ok   bool
	}{
		{"namespace", objectcontext.NamespaceRef("acme"), true},
		{"collection", objectcontext.CollectionRef("acme", "network"), true},
		{"blueprint", objectcontext.BlueprintRef("acme", "service"), true},
		{"unit", objectcontext.UnitRef(unit), true},
		{"resource URN", objectcontext.ResourceRef(unit, "urn:pulumi:prod::web::aws:s3/bucket:Bucket::logs"), true},
		{"resource address", objectcontext.ResourceRef(unit, "module.net.aws_subnet.a[0]"), true},
		{"empty resource key", objectcontext.ResourceRef(unit, ""), false},
		{"resource key with a line feed", objectcontext.ResourceRef(unit, "a\nb"), false},
		{"collection with no name", objectcontext.CollectionRef("acme", ""), false},
		{
			"namespace with a name",
			objectcontext.Ref{
				Kind:      objectcontext.KindNamespace,
				Namespace: "acme",
				Name:      "x",
				Resource:  "",
				Unit:      graph.Unit{Namespace: "", Tool: "", ID: ""},
			},
			false,
		},
		{
			"unit of another namespace",
			objectcontext.Ref{Kind: objectcontext.KindUnit, Namespace: "other", Unit: unit, Name: "", Resource: ""},
			false,
		},
		{
			"unknown kind",
			objectcontext.Ref{
				Kind:      "stack",
				Namespace: "acme",
				Name:      "",
				Resource:  "",
				Unit:      graph.Unit{Namespace: "", Tool: "", ID: ""},
			},
			false,
		},
	}

	for _, tc := range cases {
		err := tc.ref.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v, want ok %v", tc.name, err, tc.ok)
		}

		if err != nil && !errors.Is(err, objectcontext.ErrInvalidObject) {
			t.Errorf("%s: error %v, want ErrInvalidObject", tc.name, err)
		}
	}
}

func TestParseOwner(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want objectcontext.Owner
		ok   bool
	}{
		{"group:platform", objectcontext.Owner{Kind: objectcontext.OwnerGroup, Value: "platform"}, true},
		{"principal:prn-123", objectcontext.Owner{Kind: objectcontext.OwnerPrincipal, Value: "prn-123"}, true},
		{"Payments team", objectcontext.Owner{Kind: objectcontext.OwnerText, Value: "Payments team"}, true},
		{"group:", objectcontext.Owner{Kind: "", Value: ""}, false},
		{"group:has space", objectcontext.Owner{Kind: "", Value: ""}, false},
		{"  ", objectcontext.Owner{Kind: "", Value: ""}, false},
		{strings.Repeat("x", objectcontext.MaxOwnerBytes+1), objectcontext.Owner{Kind: "", Value: ""}, false},
	}

	for _, tc := range cases {
		got, err := objectcontext.ParseOwner(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("ParseOwner(%q) = %+v, %v; want %+v, ok %v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestParseOutput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want error
		name string
		raw  string
	}{
		{nil, "minimal", `{"schemaVersion":1}`},
		{nil, "full", `{"schemaVersion":1,"summary":"Edge network","runbook":"# Run\nSafe.","owners":["group:net"],
			"onCall":"pager:net","links":[{"title":"Docs","url":"https://docs.example.com","kind":"docs"}],
			"resources":{"module.vpc.aws_vpc.main":{"summary":"The VPC","owners":["group:net"]}}}`},
		{nil, "as a JSON string", `"{\"schemaVersion\":1,\"summary\":\"x\"}"`},
		{objectcontext.ErrOutputInvalid, "unknown field", `{"schemaVersion":1,"owner":"group:web"}`},
		{objectcontext.ErrOutputInvalid, "no version", `{"summary":"x"}`},
		{objectcontext.ErrOutputVersion, "newer version", `{"schemaVersion":2}`},
		{objectcontext.ErrOutputInvalid, "link with no URL", `{"schemaVersion":1,"links":[{"title":"x","url":""}]}`},
		{
			objectcontext.ErrOutputInvalid,
			"link to a file",
			`{"schemaVersion":1,"links":[{"title":"x","url":"file:///etc/passwd"}]}`,
		},
		{
			objectcontext.ErrOutputInvalid,
			"unknown link kind",
			`{"schemaVersion":1,"links":[{"title":"x","url":"https://a.b","kind":"chat"}]}`,
		},
		{objectcontext.ErrOutputInvalid, "invalid resource", `{"schemaVersion":1,"resources":{"a":{"owners":[""]}}}`},
		{objectcontext.ErrOutputInvalid, "two values", `{"schemaVersion":1}{"schemaVersion":1}`},
		{
			objectcontext.ErrOutputInvalid,
			"too large",
			`{"schemaVersion":1,"runbook":"` + strings.Repeat("a", objectcontext.MaxOutputBytes) + `"}`,
		},
	}

	for _, tc := range cases {
		_, err := objectcontext.ParseOutput([]byte(tc.raw))
		if !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
			t.Errorf("%s: error %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestOutputEntries(t *testing.T) {
	t.Parallel()

	out, err := objectcontext.ParseOutput([]byte(`{"schemaVersion":1,"summary":"u",
		"resources":{"b":{"summary":"rb"},"a":{"summary":"ra"}}}`))
	if err != nil {
		t.Fatalf("ParseOutput: %v", err)
	}

	entries := out.Entries(unit, "# Readme", "upd-7", now)

	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, e.Object.Key())

		if e.Source != objectcontext.SourceOutput || e.Item != "upd-7" {
			t.Errorf("entry %s: source %q item %q", e.Object.Key(), e.Source, e.Item)
		}
	}

	want := []string{"unit:pulumi:acme/stk-1", "resource:pulumi:acme/stk-1#a", "resource:pulumi:acme/stk-1#b"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys %v, want %v", keys, want)
	}

	if entries[0].Readme != "# Readme" {
		t.Errorf("readme %q, want it on the unit entry", entries[0].Readme)
	}
}

func TestParseReadme(t *testing.T) {
	t.Parallel()

	got, err := objectcontext.ParseReadme([]byte(`"# Web\n\nServes the site."`))
	if err != nil || got != "# Web\n\nServes the site." {
		t.Errorf("ParseReadme = %q, %v", got, err)
	}

	if _, err := objectcontext.ParseReadme([]byte(`{"a":1}`)); !errors.Is(err, objectcontext.ErrOutputInvalid) {
		t.Errorf("error %v, want ErrOutputInvalid for an object", err)
	}
}

func TestPlan(t *testing.T) {
	t.Parallel()

	sections := func(specs []objectcontext.SectionSpec) []objectcontext.Section {
		out := make([]objectcontext.Section, 0, len(specs))
		for _, s := range specs {
			out = append(out, s.Section)
		}

		return out
	}

	cases := []struct {
		want    error
		name    string
		kind    objectcontext.Kind
		include []objectcontext.Section
		sects   []objectcontext.Section
	}{
		{
			name: "unit defaults", kind: objectcontext.KindUnit,
			sects:   []objectcontext.Section{"identity", "context", "readme", "dependencies", "changes", "health"},
			want:    nil,
			include: nil,
		},
		{
			name: "unit with audit and outputs", kind: objectcontext.KindUnit,
			include: []objectcontext.Section{"audit", "outputs", "context"},
			sects:   []objectcontext.Section{"context", "outputs", "audit"},
			want:    nil,
		},
		{
			name: "collection", kind: objectcontext.KindCollection,
			sects:   []objectcontext.Section{"context"},
			want:    nil,
			include: nil,
		},
		{
			name: "resource has no readme", kind: objectcontext.KindResource,
			include: []objectcontext.Section{"readme"}, want: objectcontext.ErrUnknownSection,
			sects: nil,
		},
		{
			name: "unknown section", kind: objectcontext.KindUnit,
			include: []objectcontext.Section{"secrets"}, want: objectcontext.ErrUnknownSection,
			sects: nil,
		},
	}

	for _, tc := range cases {
		got, err := objectcontext.Plan(tc.kind, tc.include)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: error %v, want %v", tc.name, err, tc.want)

			continue
		}

		if s := sections(got); tc.want == nil && !slices.Equal(s, tc.sects) {
			t.Errorf("%s: sections %v, want %v", tc.name, s, tc.sects)
		}
	}
}

// TestSectionsNameAnAction holds the rule that every part of the context read
// API names its authorization action and its object.
func TestSectionsNameAnAction(t *testing.T) {
	t.Parallel()

	seen := map[objectcontext.Section]bool{}

	for _, s := range objectcontext.SectionCatalog() {
		if s.Action == "" || s.Object == "" || len(s.Kinds) == 0 {
			t.Errorf("section %q: action %q, object %q, kinds %v", s.Section, s.Action, s.Object, s.Kinds)
		}

		if seen[s.Section] {
			t.Errorf("section %q is in the catalog twice", s.Section)
		}

		seen[s.Section] = true

		if strings.Contains(string(s.Action), "secret") || s.Action == identity.ActionUnitDecrypt {
			t.Errorf(
				"section %q names a secret action %q; the context read API answers masked values only",
				s.Section,
				s.Action,
			)
		}
	}

	for _, k := range []objectcontext.Kind{
		objectcontext.KindNamespace, objectcontext.KindCollection, objectcontext.KindUnit,
		objectcontext.KindResource, objectcontext.KindBlueprint,
	} {
		if objectcontext.ReadAction(k) == "" || objectcontext.SetAction(k) == "" {
			t.Errorf("kind %q has no read or set action", k)
		}
	}
}
