package objectcontext_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// fieldsOf returns Fields with the given values.
func fieldsOf(summary, onCall string, owners []string, links []objectcontext.Link) objectcontext.Fields {
	return objectcontext.Fields{Summary: summary, Runbook: "", Owners: owners, OnCall: onCall, Links: links}
}

// apiEntry returns the API entry of ref with fields.
func apiEntry(ref objectcontext.Ref, fields objectcontext.Fields) *objectcontext.Entry {
	return &objectcontext.Entry{
		UpdatedAt: time.Time{}, Object: ref, Source: objectcontext.SourceAPI, Readme: "", Item: "", Warning: "",
		UpdatedBy: "", Fields: fields,
	}
}

func TestChangesOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want          map[string][2]string
		name          string
		before, after objectcontext.Fields
		fields        []objectcontext.Field
	}{
		{
			name:   "a summary keeps its text out of the entry",
			before: fieldsOf("ab", "", nil, nil),
			after:  fieldsOf("a long secret summary", "", nil, nil),
			fields: []objectcontext.Field{objectcontext.FieldSummary},
			want:   map[string][2]string{"summary": {"2 bytes", "21 bytes"}},
		},
		{
			name:   "owners and on-call hold their values",
			before: fieldsOf("", "", []string{"group:a"}, nil),
			after:  fieldsOf("", "pager:x", []string{"group:a", "group:b"}, nil),
			fields: []objectcontext.Field{objectcontext.FieldOwners, objectcontext.FieldOnCall},
			want: map[string][2]string{
				"owners": {"group:a", "group:a, group:b"},
				"onCall": {"", "pager:x"},
			},
		},
		{
			name:   "a field that does not change is left out",
			before: fieldsOf("", "pager:x", nil, nil),
			after:  fieldsOf("", "pager:x", nil, nil),
			fields: []objectcontext.Field{objectcontext.FieldOnCall},
			want:   map[string][2]string{},
		},
		{
			name:   "links count",
			before: fieldsOf("", "", nil, nil),
			after:  fieldsOf("", "", nil, []objectcontext.Link{{Title: "t", URL: "https://x.test", Kind: ""}}),
			fields: []objectcontext.Field{objectcontext.FieldLinks},
			want:   map[string][2]string{"links": {"0 links", "1 links"}},
		},
		{
			name:   "a field that the patch does not name is left out",
			before: fieldsOf("a", "", nil, nil),
			after:  fieldsOf("b", "x", nil, nil),
			fields: []objectcontext.Field{objectcontext.FieldOnCall},
			want:   map[string][2]string{"onCall": {"", "x"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := objectcontext.ChangesOf(tc.before, tc.after, tc.fields)
			if len(got) != len(tc.want) {
				t.Fatalf("changes = %+v, want %v", got, tc.want)
			}

			for _, c := range got {
				want, ok := tc.want[c.Key]
				if !ok || c.Old == nil || *c.Old != want[0] || c.New != want[1] {
					t.Errorf("change %+v, want %v", c, tc.want)
				}
			}
		})
	}
}

func TestOwnerSet(t *testing.T) {
	t.Parallel()

	got := objectcontext.OwnerSet(fieldsOf("", "", []string{"group:b", "group:a", "group:b"}, nil))
	if strings.Join(got, ",") != "group:a,group:b" {
		t.Errorf("OwnerSet = %v, want the sorted distinct owners", got)
	}
}

// resolvedValueJSON is the wire form of one resolved value that the test reads.
type resolvedValueJSON struct {
	From  map[string]any  `json:"from"`
	Value json.RawMessage `json:"value"`
}

// resolvedJSON is the wire form of a resolved context that the test reads.
type resolvedJSON struct {
	Owners  resolvedValueJSON `json:"owners"`
	OnCall  resolvedValueJSON `json:"onCall"`
	Summary resolvedValueJSON `json:"summary"`
}

// TestResolvedJSON proves the wire form of a resolved context: the owners
// carry their provenance, and a layer that the caller cannot read names its
// kind and its source only.
func TestResolvedJSON(t *testing.T) {
	t.Parallel()

	ns := objectcontext.NamespaceRef("acme")
	col := objectcontext.CollectionRef("acme", "network")
	layers := []objectcontext.Layer{
		{Object: objectcontext.UnitRef(unit), API: nil, Output: nil, Hidden: false},
		{
			Object: col, Hidden: true, Output: nil,
			API: apiEntry(col, fieldsOf("", "", []string{"group:network"}, nil)),
		},
		{
			Object: ns, Hidden: false, Output: nil,
			API: apiEntry(ns, fieldsOf("", "pager:ops", nil, nil)),
		},
	}

	resolved, err := objectcontext.Resolve(layers)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	raw, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got resolvedJSON
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal %s: %v", raw, err)
	}

	if string(got.Owners.Value) != `["group:network"]` || got.Owners.From["object"] != "collection" ||
		got.Owners.From["source"] != "api" || got.Owners.From["ref"] != nil {
		t.Errorf("owners = %+v, want the collection without its name", got.Owners)
	}

	if string(got.OnCall.Value) != `"pager:ops"` || got.OnCall.From["object"] != "namespace" ||
		got.OnCall.From["ref"] == nil {
		t.Errorf("on-call = %+v, want the namespace with its reference", got.OnCall)
	}

	if got.Summary.From != nil {
		t.Errorf("summary from = %v, want null for a value that no layer sets", got.Summary.From)
	}
}
