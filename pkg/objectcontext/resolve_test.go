package objectcontext_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/objectcontext"
)

//nolint:gochecknoglobals // A fixed test time that several tests read.
var now = time.UnixMilli(1_800_000_000_000).UTC()

//nolint:gochecknoglobals // The fixed test references of one context chain that several tests read.
var (
	nsRef   = objectcontext.NamespaceRef("acme")
	collRef = objectcontext.CollectionRef("acme", "web")
	bpRef   = objectcontext.BlueprintRef("acme", "service")
	unitRef = objectcontext.UnitRef(unit)
	resRef  = objectcontext.ResourceRef(unit, "module.db.aws_db_instance.main")
)

func entry(ref objectcontext.Ref, src objectcontext.Source, f objectcontext.Fields) *objectcontext.Entry {
	return &objectcontext.Entry{
		Object:    ref,
		Source:    src,
		Fields:    f,
		UpdatedAt: now,
		Readme:    "",
		Item:      "",
		Warning:   "",
		UpdatedBy: "",
	}
}

func api(ref objectcontext.Ref, f objectcontext.Fields) *objectcontext.Entry {
	return entry(ref, objectcontext.SourceAPI, f)
}

func out(ref objectcontext.Ref, f objectcontext.Fields) *objectcontext.Entry {
	return entry(ref, objectcontext.SourceOutput, f)
}

// unset returns the zero value of T: the value of a field that no layer
// sets.
func unset[T any]() T {
	var zero T

	return zero
}

func link(title, url string) objectcontext.Link {
	return objectcontext.Link{Title: title, URL: url, Kind: ""}
}

type resolveCase struct {
	name     string
	layers   []objectcontext.Layer
	summary  objectcontext.Text
	owners   objectcontext.Owners
	onCall   objectcontext.Text
	links    []string
	shadowed []objectcontext.Shadow
	readme   string
	warnings int
}

func from(ref objectcontext.Ref, src objectcontext.Source) objectcontext.Provenance {
	return objectcontext.Provenance{Object: ref, Source: src}
}

func (tc *resolveCase) check(t *testing.T, r *objectcontext.Resolved) {
	t.Helper()

	if r.Summary != tc.summary {
		t.Errorf("summary %+v, want %+v", r.Summary, tc.summary)
	}

	if !slices.Equal(r.Owners.Value, tc.owners.Value) || r.Owners.From != tc.owners.From {
		t.Errorf("owners %+v, want %+v", r.Owners, tc.owners)
	}

	if r.OnCall != tc.onCall {
		t.Errorf("on-call %+v, want %+v", r.OnCall, tc.onCall)
	}

	urls := make([]string, 0, len(r.Links))
	for i := range r.Links {
		urls = append(urls, r.Links[i].URL)
	}

	if !slices.Equal(urls, tc.links) {
		t.Errorf("links %v, want %v", urls, tc.links)
	}

	if !slices.Equal(r.Shadowed, tc.shadowed) {
		t.Errorf("shadowed %+v, want %+v", r.Shadowed, tc.shadowed)
	}

	if r.Readme != tc.readme || len(r.Warnings) != tc.warnings {
		t.Errorf("readme %q warnings %d, want %q and %d", r.Readme, len(r.Warnings), tc.readme, tc.warnings)
	}
}

func resolveCases() []resolveCase {
	return []resolveCase{
		{
			name: "output wins over the API in one layer",
			layers: []objectcontext.Layer{
				{
					Object: unitRef,
					Hidden: false,
					API: api(
						unitRef,
						objectcontext.Fields{
							Summary: "from API",
							OnCall:  "pager:api",
							Runbook: "",
							Owners:  nil,
							Links:   nil,
						},
					),
					Output: out(
						unitRef,
						objectcontext.Fields{Summary: "from code", Runbook: "", Owners: nil, OnCall: "", Links: nil},
					),
				},
			},
			summary:  objectcontext.Text{Value: "from code", From: from(unitRef, objectcontext.SourceOutput)},
			onCall:   objectcontext.Text{Value: "pager:api", From: from(unitRef, objectcontext.SourceAPI)},
			shadowed: []objectcontext.Shadow{{Object: unitRef, Field: objectcontext.FieldSummary}},
			owners:   unset[objectcontext.Owners](),
			links:    nil,
			readme:   "",
			warnings: 0,
		},
		{
			name: "owners and on-call come from the nearest layer, never the blueprint",
			layers: []objectcontext.Layer{
				{Object: unitRef, API: nil, Output: nil, Hidden: false},
				{
					Object: bpRef,
					Hidden: false,
					API: api(
						bpRef,
						objectcontext.Fields{
							Owners:  []string{"group:platform"},
							OnCall:  "pager:platform",
							Summary: "",
							Runbook: "",
							Links:   nil,
						},
					),
					Output: nil,
				},
				{
					Object: collRef,
					Hidden: false,
					API: api(
						collRef,
						objectcontext.Fields{
							Owners:  []string{"group:web"},
							Summary: "",
							Runbook: "",
							OnCall:  "",
							Links:   nil,
						},
					),
					Output: nil,
				},
				{
					Object: nsRef,
					Hidden: false,
					API: api(
						nsRef,
						objectcontext.Fields{
							Owners:  []string{"group:all"},
							OnCall:  "pager:ops",
							Summary: "",
							Runbook: "",
							Links:   nil,
						},
					),
					Output: nil,
				},
			},
			owners:   objectcontext.Owners{Value: []string{"group:web"}, From: from(collRef, objectcontext.SourceAPI)},
			onCall:   objectcontext.Text{Value: "pager:ops", From: from(nsRef, objectcontext.SourceAPI)},
			summary:  unset[objectcontext.Text](),
			links:    nil,
			shadowed: nil,
			readme:   "",
			warnings: 0,
		},
		{
			name: "summary does not come from a wider layer",
			layers: []objectcontext.Layer{
				{Object: unitRef, API: nil, Output: nil, Hidden: false},
				{
					Object: collRef,
					Hidden: false,
					API: api(
						collRef,
						objectcontext.Fields{
							Summary: "All web units",
							Runbook: "",
							Owners:  nil,
							OnCall:  "",
							Links:   nil,
						},
					),
					Output: nil,
				},
			},
			summary:  unset[objectcontext.Text](),
			owners:   unset[objectcontext.Owners](),
			onCall:   unset[objectcontext.Text](),
			links:    nil,
			shadowed: nil,
			readme:   "",
			warnings: 0,
		},
		{
			name: "links add up from every layer with no second URL",
			layers: []objectcontext.Layer{
				{
					Object: resRef,
					Hidden: false,
					Output: out(
						resRef,
						objectcontext.Fields{
							Links:   []objectcontext.Link{link("DB", "https://db.example.com")},
							Summary: "",
							Runbook: "",
							Owners:  nil,
							OnCall:  "",
						},
					),
					API: nil,
				},
				{Object: unitRef, Hidden: false, Output: out(unitRef, objectcontext.Fields{Links: []objectcontext.Link{
					link("Dash", "https://dash.example.com"), link("DB again", "https://db.example.com"),
				}, Summary: "", Runbook: "", Owners: nil, OnCall: ""}), API: nil},
				{
					Object: bpRef,
					Hidden: false,
					API: api(
						bpRef,
						objectcontext.Fields{
							Links:   []objectcontext.Link{link("Blueprint", "https://bp.example.com")},
							Summary: "",
							Runbook: "",
							Owners:  nil,
							OnCall:  "",
						},
					),
					Output: nil,
				},
			},
			links:    []string{"https://db.example.com", "https://dash.example.com", "https://bp.example.com"},
			summary:  unset[objectcontext.Text](),
			owners:   unset[objectcontext.Owners](),
			onCall:   unset[objectcontext.Text](),
			shadowed: nil,
			readme:   "",
			warnings: 0,
		},
		{
			name: "a resource takes the owners of its unit",
			layers: []objectcontext.Layer{
				{
					Object: resRef,
					Hidden: false,
					Output: out(
						resRef,
						objectcontext.Fields{
							Summary: "Main database",
							Runbook: "",
							Owners:  nil,
							OnCall:  "",
							Links:   nil,
						},
					),
					API: nil,
				},
				{
					Object: unitRef,
					Hidden: false,
					Output: out(
						unitRef,
						objectcontext.Fields{
							Owners:  []string{"group:data"},
							Summary: "",
							Runbook: "",
							OnCall:  "",
							Links:   nil,
						},
					),
					API: nil,
				},
			},
			summary: objectcontext.Text{Value: "Main database", From: from(resRef, objectcontext.SourceOutput)},
			owners: objectcontext.Owners{
				Value: []string{"group:data"},
				From:  from(unitRef, objectcontext.SourceOutput),
			},
			onCall:   unset[objectcontext.Text](),
			links:    nil,
			shadowed: nil,
			readme:   "",
			warnings: 0,
		},
		{
			name: "readme and warnings of the unit",
			layers: []objectcontext.Layer{
				{
					Object: unitRef,
					Hidden: false,
					Output: &objectcontext.Entry{
						Object:    unitRef,
						Source:    objectcontext.SourceOutput,
						Readme:    "# Web",
						Warning:   "unknown field",
						UpdatedAt: time.Time{},
						Item:      "",
						UpdatedBy: "",
						Fields:    objectcontext.Fields{Summary: "", Runbook: "", Owners: nil, OnCall: "", Links: nil},
					},
					API: nil,
				},
			},
			readme:   "# Web",
			warnings: 1,
			summary:  unset[objectcontext.Text](),
			owners:   unset[objectcontext.Owners](),
			onCall:   unset[objectcontext.Text](),
			links:    nil,
			shadowed: nil,
		},
		hiddenLayerCase(),
	}
}

// hiddenLayerCase is a caller who reads the unit and the namespace, and does
// not read the collection. The collection still gives the owners and the
// on-call, and nothing else.
func hiddenLayerCase() resolveCase {
	return resolveCase{
		name: "a hidden layer gives its owners and on-call only",
		layers: []objectcontext.Layer{
			{Object: unitRef, API: nil, Output: nil, Hidden: false},
			{
				Object: collRef,
				Hidden: true,
				API: api(collRef, objectcontext.Fields{
					Summary: "", Runbook: "", Owners: []string{"group:web"}, OnCall: "pager:web",
					Links: []objectcontext.Link{link("Private", "https://wiki.example.com/web")},
				}),
				Output: out(collRef, objectcontext.Fields{
					Summary: "", Runbook: "", Owners: []string{"group:web-code"}, OnCall: "", Links: nil,
				}),
			},
			{
				Object: nsRef,
				Hidden: false,
				API: api(nsRef, objectcontext.Fields{
					Summary: "", Runbook: "", Owners: []string{"group:all"}, OnCall: "pager:ops",
					Links: []objectcontext.Link{link("Status", "https://status.example.com")},
				}),
				Output: nil,
			},
		},
		owners: objectcontext.Owners{
			Value: []string{"group:web-code"}, From: from(collRef, objectcontext.SourceOutput),
		},
		onCall:   objectcontext.Text{Value: "pager:web", From: from(collRef, objectcontext.SourceAPI)},
		links:    []string{"https://status.example.com"},
		summary:  unset[objectcontext.Text](),
		shadowed: nil,
		readme:   "",
		warnings: 0,
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	for _, tc := range resolveCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, err := objectcontext.Resolve(tc.layers)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			tc.check(t, &r)
		})
	}
}

func TestResolveChainErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		layers []objectcontext.Layer
	}{
		{"empty", nil},
		{
			"out of order",
			[]objectcontext.Layer{
				{Object: collRef, API: nil, Output: nil, Hidden: false},
				{Object: unitRef, API: nil, Output: nil, Hidden: false},
			},
		},
		{
			"kind twice",
			[]objectcontext.Layer{
				{Object: unitRef, API: nil, Output: nil, Hidden: false},
				{Object: unitRef, API: nil, Output: nil, Hidden: false},
			},
		},
		{
			"entry of another object",
			[]objectcontext.Layer{
				{
					Object: unitRef,
					Hidden: false,
					API: api(
						collRef,
						objectcontext.Fields{Summary: "", Runbook: "", Owners: nil, OnCall: "", Links: nil},
					),
					Output: nil,
				},
			},
		},
	}

	for _, tc := range cases {
		if _, err := objectcontext.Resolve(tc.layers); !errors.Is(err, objectcontext.ErrInvalidObject) {
			t.Errorf("%s: error %v, want ErrInvalidObject", tc.name, err)
		}
	}
}

func TestCheckPatch(t *testing.T) {
	t.Parallel()

	str := func(s string) *string { return &s }
	owners := func(o ...string) *[]string { return &o }

	fromCode := out(
		unitRef,
		objectcontext.Fields{Owners: []string{"group:web"}, Summary: "code", Runbook: "", OnCall: "", Links: nil},
	)

	cases := []struct {
		want   error
		output *objectcontext.Entry
		name   string
		patch  objectcontext.Patch
	}{
		{
			nil,
			nil,
			"no output",
			objectcontext.Patch{Summary: str("x"), Runbook: nil, OnCall: nil, Owners: nil, Links: nil},
		},
		{
			nil,
			fromCode,
			"a field the code does not set",
			objectcontext.Patch{OnCall: str("pager:web"), Summary: nil, Runbook: nil, Owners: nil, Links: nil},
		},
		{
			objectcontext.ErrFieldFromCode,
			fromCode,
			"a field the code sets",
			objectcontext.Patch{Owners: owners("group:ops"), Summary: nil, Runbook: nil, OnCall: nil, Links: nil},
		},
		{
			nil,
			fromCode,
			"clear a shadowed field",
			objectcontext.Patch{Summary: str(""), Runbook: nil, OnCall: nil, Owners: nil, Links: nil},
		},
		{
			objectcontext.ErrInvalidContext,
			nil,
			"invalid owner",
			objectcontext.Patch{Owners: owners("group:"), Summary: nil, Runbook: nil, OnCall: nil, Links: nil},
		},
	}

	for _, tc := range cases {
		err := objectcontext.CheckPatch(tc.output, tc.patch)
		if !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
			t.Errorf("%s: error %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestPatchApply(t *testing.T) {
	t.Parallel()

	str := func(s string) *string { return &s }
	start := objectcontext.Fields{Summary: "a", OnCall: "pager:a", Owners: []string{"group:a"}, Runbook: "", Links: nil}

	got, err := objectcontext.Patch{
		Summary: str(""),
		OnCall:  str("pager:b"),
		Runbook: nil,
		Owners:  nil,
		Links:   nil,
	}.Apply(
		start,
	)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got.Summary != "" || got.OnCall != "pager:b" || !slices.Equal(got.Owners, start.Owners) {
		t.Errorf("Apply = %+v", got)
	}

	if f := (objectcontext.Patch{Summary: str(""), Runbook: nil, OnCall: nil, Owners: nil, Links: nil}).Fields(); !slices.Equal(
		f,
		[]objectcontext.Field{objectcontext.FieldSummary},
	) {
		t.Errorf("Fields = %v", f)
	}
}
