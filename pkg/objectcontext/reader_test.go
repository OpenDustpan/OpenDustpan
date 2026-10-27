package objectcontext_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// checkedAt returns the time of the drift check of the fake.
func checkedAt() *time.Time {
	at := now.Add(-time.Hour)

	return &at
}

var errBroken = errors.New("broken source")

// fakeSources implements every source port of the read API. denied holds the
// kinds of chain layers and the actions that the caller does not hold.
// hiddenUnits holds the units that the caller cannot read.
type fakeSources struct {
	entries     []objectcontext.Entry
	denied      map[string]bool
	hiddenUnits map[graph.Unit]bool
	deps        objectcontext.DependenciesData
	outputs     objectcontext.OutputsData
	jobs        []objectcontext.JobSummary
	historyErr  error
	driftData   objectcontext.DriftData
	driftErr    error
}

// chainOf returns the chain of the fixture unit, or of its resource.
func chainOf(object objectcontext.Ref) []objectcontext.Ref {
	tail := []objectcontext.Ref{unitRef, collRef, nsRef}
	if object.Kind == objectcontext.KindResource {
		return append([]objectcontext.Ref{object}, tail...)
	}

	if object.Kind == objectcontext.KindCollection {
		return []objectcontext.Ref{collRef, nsRef}
	}

	return tail
}

func (f *fakeSources) Chain(_ context.Context, object objectcontext.Ref) ([]objectcontext.Ref, error) {
	if object.Unit != unit && object.Kind != objectcontext.KindCollection {
		return nil, objectcontext.ErrNotFound
	}

	return chainOf(object), nil
}

func (f *fakeSources) Identity(_ context.Context, object objectcontext.Ref) (objectcontext.IdentityData, error) {
	return objectcontext.IdentityData{
		Name: "web-prod", DisplayName: "", Noun: "stack", Collection: "web", Blueprint: "", Type: "",
		Key: object.Resource,
	}, nil
}

func (f *fakeSources) Outputs(context.Context, graph.Unit, int) (objectcontext.OutputsData, error) {
	return f.outputs, nil
}

func (f *fakeSources) Health(context.Context, graph.Unit) (objectcontext.HealthData, error) {
	return objectcontext.HealthData{
		LatestItemAt: now, NewestJob: nil, OpenFindings: nil, Status: "succeeded", LockedBy: "", Locked: false,
	}, nil
}

func (f *fakeSources) Entries(_ context.Context, objects []objectcontext.Ref) ([]objectcontext.Entry, error) {
	var out []objectcontext.Entry

	for i := range f.entries {
		if slices.Contains(objects, f.entries[i].Object) {
			out = append(out, f.entries[i])
		}
	}

	return out, nil
}

func (f *fakeSources) Neighbors(context.Context, graph.Unit, int) (objectcontext.DependenciesData, error) {
	return f.deps, nil
}

func (f *fakeSources) Changes(context.Context, objectcontext.Ref, int, int) (objectcontext.ChangesData, error) {
	return objectcontext.ChangesData{Items: nil, Jobs: nil, Resources: nil, Truncated: false}, f.historyErr
}

func (f *fakeSources) Jobs(_ context.Context, _ graph.Unit, limit int) ([]objectcontext.JobSummary, error) {
	if len(f.jobs) == 0 {
		return nil, objectcontext.ErrUnavailable
	}

	return f.jobs[:min(limit, len(f.jobs))], nil
}

func (f *fakeSources) OpenCounts(context.Context, graph.Unit) (map[string]int64, error) {
	return map[string]int64{"managed-twice": 1}, nil
}

func (f *fakeSources) Drift(context.Context, graph.Unit) (objectcontext.DriftData, error) {
	return f.driftData, f.driftErr
}

func (f *fakeSources) Layers(_ context.Context, chain []objectcontext.Ref) ([]bool, error) {
	out := make([]bool, len(chain))
	for i, r := range chain {
		out[i] = !f.denied[string(r.Kind)]
	}

	return out, nil
}

func (f *fakeSources) Holds(_ context.Context, a identity.Action, _ objectcontext.Ref) (bool, error) {
	return !f.denied[string(a)], nil
}

func (f *fakeSources) Readable(_ context.Context, units []graph.Unit) (map[graph.Unit]bool, error) {
	out := map[graph.Unit]bool{}

	for _, u := range units {
		if !f.hiddenUnits[u] {
			out[u] = true
		}
	}

	return out, nil
}

func (f *fakeSources) AuditEntries(context.Context, graph.Unit, int) ([]audit.Entry, error) {
	return nil, nil
}

// auditPort adapts the fake to AuditReader, whose method name Entries the
// fake already uses for ContextReader.
type auditPort struct{ f *fakeSources }

func (p auditPort) Entries(ctx context.Context, u graph.Unit, limit int) ([]audit.Entry, error) {
	return p.f.AuditEntries(ctx, u, limit)
}

func edge(src, dep graph.Unit) graph.Edge {
	return graph.Edge{
		LastSeen: now, Source: src, Dependent: dep, Kind: "output-read", Evidence: "job", Ambiguous: false,
	}
}

func baseFake() *fakeSources {
	other := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-2"}

	return &fakeSources{
		entries: []objectcontext.Entry{
			*out(unitRef, objectcontext.Fields{Summary: "The shop.", Runbook: "", Owners: nil, OnCall: "", Links: nil}),
			*api(collRef, objectcontext.Fields{
				Summary: "", Runbook: "", Owners: []string{"group:web"}, OnCall: "", Links: nil,
			}),
			*api(nsRef, objectcontext.Fields{Summary: "", Runbook: "", Owners: nil, OnCall: "pager:ops", Links: nil}),
		},
		denied:      map[string]bool{},
		hiddenUnits: map[graph.Unit]bool{},
		deps: objectcontext.DependenciesData{
			Upstream: []graph.Edge{edge(other, unit)}, Downstream: nil, PrincipalReaders: 0, Truncated: false,
		},
		outputs: objectcontext.OutputsData{
			Outputs: map[string]json.RawMessage{"url": json.RawMessage(`"https://shop.example.com"`)}, Truncated: false,
		},
		jobs:       nil,
		historyErr: nil,
		driftData: objectcontext.DriftData{
			CheckedAt: checkedAt(), Status: "drifted", JobID: "job-9", Truncated: false,
			Addresses: []string{"aws_s3_bucket.logs", "aws_s3_bucket.assets"},
		},
		driftErr: nil,
	}
}

func allSources(f *fakeSources) objectcontext.Sources {
	return objectcontext.Sources{
		Inventory:    f,
		Context:      f,
		Access:       f,
		Dependencies: f,
		History:      f,
		Jobs:         f,
		Findings:     f,
		Drift:        f,
		Audit:        auditPort{f},
	}
}

type readCase struct {
	name    string
	setup   func(*fakeSources, *objectcontext.Sources)
	object  objectcontext.Ref
	include []objectcontext.Section
	want    map[objectcontext.Section]objectcontext.Status
	err     error
	check   func(t *testing.T, doc objectcontext.Document)
}

func readCases() []readCase {
	nothing := func(*fakeSources, *objectcontext.Sources) {}

	return []readCase{
		{
			name: "the default sections with every action", setup: nothing, object: unitRef, include: nil,
			want: map[objectcontext.Section]objectcontext.Status{
				"identity": "ok", "context": "ok", "readme": "unavailable", "dependencies": "ok",
				"changes": "ok", "health": "ok",
			},
			err: nil, check: nil,
		},
		{
			name: "no collection:read: the layer is denied, its owners stay",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.denied[string(objectcontext.KindCollection)] = true
			},
			object: unitRef, include: []objectcontext.Section{"context"},
			want: map[objectcontext.Section]objectcontext.Status{"context": "partial"}, err: nil,
			check: checkDeniedCollection,
		},
		{
			name: "no unit:read: not found",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.denied[string(objectcontext.KindUnit)] = true
			},
			object: unitRef, include: nil, want: nil, err: objectcontext.ErrNotFound, check: nil,
		},
		{
			name: "no namespace:read-audit: the audit section is denied",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.denied[string(identity.ActionNamespaceReadAudit)] = true
			},
			object: unitRef, include: []objectcontext.Section{"audit", "identity"},
			want: map[objectcontext.Section]objectcontext.Status{"audit": "denied", "identity": "ok"}, err: nil,
			check: nil,
		},
		{
			name: "features that are not on are unavailable",
			setup: func(_ *fakeSources, s *objectcontext.Sources) {
				s.Dependencies, s.History, s.Jobs, s.Findings, s.Audit = nil, nil, nil, nil, nil
			},
			object: unitRef, include: []objectcontext.Section{"dependencies", "changes", "audit", "health"},
			want: map[objectcontext.Section]objectcontext.Status{
				"dependencies": "unavailable", "changes": "unavailable", "audit": "unavailable", "health": "ok",
			},
			err: nil, check: nil,
		},
		{
			name: "a hidden neighbor is counted, not named",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.hiddenUnits[f.deps.Upstream[0].Source] = true
			},
			object: unitRef, include: []objectcontext.Section{"dependencies"},
			want: map[objectcontext.Section]objectcontext.Status{"dependencies": "partial"}, err: nil,
			check: checkHiddenNeighbor,
		},
		{
			name: "cut outputs are partial",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.outputs.Truncated = true
			},
			object: unitRef, include: []objectcontext.Section{"outputs"},
			want: map[objectcontext.Section]objectcontext.Status{"outputs": "partial"}, err: nil, check: nil,
		},
		{
			name: "the newest job goes into health",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.jobs = []objectcontext.JobSummary{
					{At: now, ID: "job-2", Operation: "apply", Status: "failed", Actor: ""},
				}
			},
			object: unitRef, include: []objectcontext.Section{"health"},
			want: map[objectcontext.Section]objectcontext.Status{"health": "ok"}, err: nil, check: checkHealth,
		},
		{
			name: "a broken source fails the call",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.historyErr = errBroken
			},
			object: unitRef, include: []objectcontext.Section{"changes"}, want: nil, err: errBroken, check: nil,
		},
		{
			name: "a source with no data is unavailable",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.historyErr = objectcontext.ErrUnavailable
			},
			object: unitRef, include: []objectcontext.Section{"changes"},
			want: map[objectcontext.Section]objectcontext.Status{"changes": "unavailable"}, err: nil, check: nil,
		},
		{
			name: "a drifted unit answers its addresses", setup: nothing, object: unitRef,
			include: []objectcontext.Section{"drift"},
			want:    map[objectcontext.Section]objectcontext.Status{"drift": "ok"}, err: nil,
			check: checkDrift(objectcontext.DriftData{
				CheckedAt: checkedAt(), Status: "drifted", JobID: "job-9", Truncated: false,
				Addresses: []string{"aws_s3_bucket.logs", "aws_s3_bucket.assets"},
			}),
		},
		{
			name: "a unit with no check is unknown",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.driftData = objectcontext.DriftData{
					CheckedAt: nil, Status: "unknown", JobID: "", Addresses: nil, Truncated: false,
				}
			},
			object: unitRef, include: []objectcontext.Section{"drift"},
			want: map[objectcontext.Section]objectcontext.Status{"drift": "ok"}, err: nil,
			check: checkDrift(objectcontext.DriftData{
				CheckedAt: nil, Status: "unknown", JobID: "", Addresses: []string{}, Truncated: false,
			}),
		},
		{
			name: "no unit:read on the unit: the drift section is denied, like any other",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.denied[string(identity.ActionUnitRead)] = true
			},
			object: unitRef, include: []objectcontext.Section{"drift", "health"},
			want: map[objectcontext.Section]objectcontext.Status{"drift": "denied", "health": "denied"}, err: nil,
			check: checkDriftHidden,
		},
		{
			name: "no drift service: the section is unavailable, the call goes on",
			setup: func(_ *fakeSources, s *objectcontext.Sources) {
				s.Drift = nil
			},
			object: unitRef, include: []objectcontext.Section{"drift", "health"},
			want: map[objectcontext.Section]objectcontext.Status{"drift": "unavailable", "health": "ok"}, err: nil,
			check: checkDriftHidden,
		},
		{
			name: "a source that has no data is unavailable",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.driftErr = objectcontext.ErrUnavailable
			},
			object: unitRef, include: []objectcontext.Section{"drift"},
			want: map[objectcontext.Section]objectcontext.Status{"drift": "unavailable"}, err: nil,
			check: checkDriftHidden,
		},
		{
			name: "a broken drift source fails the call",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.driftErr = errBroken
			},
			object: unitRef, include: []objectcontext.Section{"drift"}, want: nil, err: errBroken, check: nil,
		},
		{
			name: "more addresses than the limit are cut and flagged",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.driftData.Addresses = manyAddresses(objectcontext.MaxReadDriftAddresses + 1)
			},
			object: unitRef, include: []objectcontext.Section{"drift"},
			want: map[objectcontext.Section]objectcontext.Status{"drift": "partial"}, err: nil,
			check: checkDriftCut,
		},
		{
			name: "a check that cut its own list is partial",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.driftData.Truncated = true
			},
			object: unitRef, include: []objectcontext.Section{"drift"},
			want: map[objectcontext.Section]objectcontext.Status{"drift": "partial"}, err: nil, check: nil,
		},
		{
			name: "a resource that the unit lists shows its own entry alone",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.driftData.Addresses = []string{"aws_s3_bucket.logs", resRef.Resource}
			},
			object: resRef, include: []objectcontext.Section{"drift"},
			want: map[objectcontext.Section]objectcontext.Status{"drift": "ok"}, err: nil,
			check: checkDriftAddresses([]string{resRef.Resource}),
		},
		{
			name: "a resource that the unit does not list shows no address",
			setup: func(f *fakeSources, _ *objectcontext.Sources) {
				f.driftData.Addresses = []string{"aws_s3_bucket.other"}
			},
			object: resRef, include: []objectcontext.Section{"drift"},
			want: map[objectcontext.Section]objectcontext.Status{"drift": "ok"}, err: nil,
			check: checkDriftAddresses([]string{}),
		},
		{
			name: "a section that does not apply", setup: nothing, object: collRef,
			include: []objectcontext.Section{"health"}, want: nil, err: objectcontext.ErrUnknownSection, check: nil,
		},
		{
			name: "a collection answers its context only", setup: nothing, object: collRef, include: nil,
			want: map[objectcontext.Section]objectcontext.Status{"context": "ok"}, err: nil, check: nil,
		},
		{
			name: "a resource answers its default sections", setup: nothing, object: resRef, include: nil,
			want: map[objectcontext.Section]objectcontext.Status{
				"identity": "ok", "context": "ok", "dependencies": "ok", "changes": "ok", "health": "ok",
			},
			err: nil, check: nil,
		},
	}
}

// manyAddresses returns n distinct resource addresses.
func manyAddresses(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "aws_s3_bucket.b" + strconv.Itoa(i)
	}

	return out
}

// checkDrift checks that the drift section holds exactly want.
func checkDrift(want objectcontext.DriftData) func(*testing.T, objectcontext.Document) {
	return func(t *testing.T, doc objectcontext.Document) {
		t.Helper()

		got, ok := doc.Sections[objectcontext.SectionDrift].Data.(objectcontext.DriftData)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("drift data %+v, want %+v", doc.Sections[objectcontext.SectionDrift].Data, want)
		}
	}
}

// checkDriftAddresses checks the addresses of the drift section.
func checkDriftAddresses(want []string) func(*testing.T, objectcontext.Document) {
	return func(t *testing.T, doc objectcontext.Document) {
		t.Helper()

		got, ok := doc.Sections[objectcontext.SectionDrift].Data.(objectcontext.DriftData)
		if !ok || !slices.Equal(got.Addresses, want) || got.Addresses == nil {
			t.Errorf("drift data %+v, want the addresses %v", doc.Sections[objectcontext.SectionDrift].Data, want)
		}
	}
}

// checkDriftHidden checks that the drift section holds no data.
func checkDriftHidden(t *testing.T, doc objectcontext.Document) {
	t.Helper()

	if a := doc.Sections[objectcontext.SectionDrift]; a.Data != nil {
		t.Errorf("drift answer %+v, want no data", a)
	}
}

// checkDriftCut checks that the section holds the limit and says that it cut.
func checkDriftCut(t *testing.T, doc objectcontext.Document) {
	t.Helper()

	got, ok := doc.Sections[objectcontext.SectionDrift].Data.(objectcontext.DriftData)
	if !ok || len(got.Addresses) != objectcontext.MaxReadDriftAddresses || !got.Truncated {
		t.Errorf("drift data %+v, want %d addresses and truncated", doc.Sections[objectcontext.SectionDrift].Data,
			objectcontext.MaxReadDriftAddresses)
	}
}

func checkDeniedCollection(t *testing.T, doc objectcontext.Document) {
	t.Helper()

	a := doc.Sections[objectcontext.SectionContext]

	wantLayers := []objectcontext.LayerStatus{
		{Object: objectcontext.KindUnit, Action: identity.ActionUnitRead, Status: objectcontext.StatusOK},
		{
			Object: objectcontext.KindCollection,
			Action: identity.ActionCollectionRead,
			Status: objectcontext.StatusDenied,
		},
		{Object: objectcontext.KindNamespace, Action: identity.ActionNamespaceRead, Status: objectcontext.StatusOK},
	}
	if !slices.Equal(a.Layers, wantLayers) || a.Hidden != 1 {
		t.Errorf("layers %+v hidden %d, want %+v and 1", a.Layers, a.Hidden, wantLayers)
	}

	data, ok := a.Data.(objectcontext.ContextData)
	if !ok {
		t.Fatalf("data %T, want ContextData", a.Data)

		return
	}

	if !slices.Equal(data.Resolved.Owners.Value, []string{"group:web"}) || data.Resolved.OnCall.Value != "pager:ops" {
		t.Errorf("resolved %+v, want the owners of the collection and the on-call of the namespace", data.Resolved)
	}

	for i := range data.Entries {
		if data.Entries[i].Object.Kind == objectcontext.KindCollection {
			t.Errorf("entries %+v hold the entry of the denied collection", data.Entries)
		}
	}
}

func checkHiddenNeighbor(t *testing.T, doc objectcontext.Document) {
	t.Helper()

	a := doc.Sections[objectcontext.SectionDependencies]

	data, ok := a.Data.(objectcontext.DependenciesData)
	if !ok || a.Hidden != 1 || len(data.Upstream) != 0 {
		t.Errorf("dependencies %+v hidden %d, want no edge and 1 hidden", a.Data, a.Hidden)
	}
}

func checkHealth(t *testing.T, doc objectcontext.Document) {
	t.Helper()

	data, ok := doc.Sections[objectcontext.SectionHealth].Data.(objectcontext.HealthData)
	if !ok || data.NewestJob == nil || data.NewestJob.ID != "job-2" || data.OpenFindings["managed-twice"] != 1 {
		t.Errorf("health %+v, want the newest job and the open findings", data)
	}
}

func TestRead(t *testing.T) {
	t.Parallel()

	for _, tc := range readCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := baseFake()
			src := allSources(f)
			tc.setup(f, &src)

			r, err := objectcontext.NewReader(src)
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}

			doc, err := r.Read(t.Context(), objectcontext.Request{Object: tc.object, Include: tc.include})
			if !errors.Is(err, tc.err) {
				t.Fatalf("Read error %v, want %v", err, tc.err)
			}

			if tc.err != nil {
				return
			}

			checkStatuses(t, doc, tc.want)

			if tc.check != nil {
				tc.check(t, doc)
			}
		})
	}
}

func checkStatuses(t *testing.T, doc objectcontext.Document, want map[objectcontext.Section]objectcontext.Status) {
	t.Helper()

	if doc.SchemaVersion != objectcontext.SchemaVersion || len(doc.Sections) != len(want) {
		t.Errorf("document version %d with %d sections, want %d sections", doc.SchemaVersion, len(doc.Sections),
			len(want))
	}

	for sec, st := range want {
		a, ok := doc.Sections[sec]
		if !ok || a.Status != st {
			t.Errorf("section %s: %+v, want status %s", sec, a, st)
		}

		if ok && (a.Action == "" || a.Object == "") {
			t.Errorf("section %s names no action or object: %+v", sec, a)
		}
	}
}

func TestNewReaderNeedsPorts(t *testing.T) {
	t.Parallel()

	f := baseFake()

	_, err := objectcontext.NewReader(objectcontext.Sources{
		Inventory:    f,
		Context:      nil,
		Access:       f,
		Dependencies: nil,
		History:      nil,
		Jobs:         nil,
		Findings:     nil,
		Drift:        nil,
		Audit:        nil,
	})
	if !errors.Is(err, objectcontext.ErrInvalidObject) {
		t.Errorf("NewReader with no context port: %v, want ErrInvalidObject", err)
	}
}

// TestDocumentJSON checks the envelope of a section on the wire.
func TestDocumentJSON(t *testing.T) {
	t.Parallel()

	a := objectcontext.Answer{
		Data: nil, Action: identity.ActionUnitRead, Object: objectcontext.RoleEachNamed,
		Status: objectcontext.StatusPartial, Layers: nil, Hidden: 2,
	}

	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"action":"unit:read","object":"each-named-unit","status":"partial","hidden":2}`
	if string(raw) != want {
		t.Errorf("answer %s, want %s", raw, want)
	}
}
