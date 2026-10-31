package catalogstore_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/catalogstore"
)

const ns = namespace.Name("payments")

func blueprint(name string) catalog.Blueprint {
	module := catalog.Module{
		Source: "registry.example.com/platform/postgres/aws", Version: "2.4.1",
		Connection: "", Repository: "", Directory: "", Revision: "",
	}

	return catalog.Blueprint{
		Program: catalog.Program{Module: &module, Component: nil},
		Platform: catalog.Platform{
			Values: map[string]json.RawMessage{"backup_retention_days": json.RawMessage(`14`)}, Profile: "", Pool: "",
		},
		Delivery: catalog.Delivery{
			Git: nil, Default: catalog.HomeOpenDustpan, Homes: []catalog.HomeKind{catalog.HomeOpenDustpan},
		},
		Name: name, Title: "PostgreSQL", Description: "", Tool: "opentofu", ExprVersion: "",
		UnitName: "'databases/' + request.name", SourcePrincipal: "",
		Inputs: []catalog.Input{{
			Choice: nil, Name: "size", Title: "", Description: "", Type: catalog.TypeString, Maps: "instance_size",
			Default: json.RawMessage(`"small"`), Validate: nil, Required: false,
		}},
		Policy: nil, Quotas: nil, SchemaVersion: 1,
	}
}

func fence() namespace.Fence {
	return namespace.Fence{Namespace: ns, FailoverVersion: 1}
}

func versionRow(t *testing.T, mutate func(r *catalogstore.VersionRow)) catalogstore.VersionRow {
	t.Helper()

	b := blueprint("postgres")

	doc, err := catalogstore.EncodeVersion(&b)
	if err != nil {
		t.Fatalf("EncodeVersion() = %v", err)
	}

	row := catalogstore.VersionRow{
		Namespace: ns.String(), Name: "postgres", DocumentJSON: doc, ExprVersion: "v1.17.8", PublishedBy: "platform",
		PublishedAtMs: 1_700_000_000_000, Number: 1, DraftVersion: 1, Retired: 0,
	}
	mutate(&row)

	return row
}

func TestVersionRoundTrip(t *testing.T) {
	t.Parallel()

	v, err := catalogstore.Version(versionRow(t, func(*catalogstore.VersionRow) {}))
	if err != nil {
		t.Fatalf("Version() = %v", err)
	}

	if v.Blueprint.ExprVersion != "v1.17.8" || v.Number != 1 || v.Retired || v.Namespace != ns ||
		!v.PublishedAt.Equal(time.UnixMilli(1_700_000_000_000)) {
		t.Fatalf("Version() = %+v", v)
	}
}

func TestVersionRefusesHostileRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(r *catalogstore.VersionRow)
		name   string
	}{
		{func(r *catalogstore.VersionRow) { r.DocumentJSON = "{" }, "a document that is not JSON"},
		{
			func(r *catalogstore.VersionRow) { r.DocumentJSON = `{"unknown":1}` },
			"a field that the blueprint does not know",
		},
		{func(r *catalogstore.VersionRow) { r.DocumentJSON += " {}" }, "text after the document"},
		{func(r *catalogstore.VersionRow) { r.Name = "other" }, "a name that is not the name of the document"},
		{func(r *catalogstore.VersionRow) { r.Number = 0 }, "number 0"},
		{func(r *catalogstore.VersionRow) { r.Number = catalog.MaxVersions + 1 }, "a number above the limit"},
		{func(r *catalogstore.VersionRow) { r.DraftVersion = 0 }, "draft version 0"},
		{func(r *catalogstore.VersionRow) { r.Retired = 2 }, "a retired flag of 2"},
		{func(r *catalogstore.VersionRow) { r.Retired = -1 }, "a retired flag of -1"},
		{func(r *catalogstore.VersionRow) {
			r.DocumentJSON = strings.Replace(r.DocumentJSON, `"exprVersion"`, `"x"`, 1)
			r.DocumentJSON = strings.Replace(r.DocumentJSON, `"name":"postgres"`, `"name":"Postgres"`, 1)
		}, "a name that is not valid"},
		{func(r *catalogstore.VersionRow) {
			r.DocumentJSON = strings.Replace(
				r.DocumentJSON,
				`"title":"PostgreSQL"`,
				`"title":"PostgreSQL","exprVersion":"v9"`,
				1,
			)
		}, "an Expr version inside the document"},
		{func(r *catalogstore.VersionRow) {
			r.DocumentJSON = strings.Replace(r.DocumentJSON, `"schemaVersion":1`, `"schemaVersion":99`, 1)
		}, "a schema version above the supported one"},
		{
			func(r *catalogstore.VersionRow) { r.DocumentJSON = strings.Repeat(" ", catalog.MaxBlueprintBytes+1) },
			"a document above the limit",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := catalogstore.Version(versionRow(t, tc.mutate))
			if !errors.Is(err, catalogstore.ErrCorrupt) {
				t.Fatalf("Version() = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestDraftRefusesHostileRows(t *testing.T) {
	t.Parallel()

	ref := catalog.BlueprintRef{Namespace: ns, Name: "postgres"}
	b := blueprint("postgres")

	good, err := catalogstore.EncodeDraft(&b)
	if err != nil {
		t.Fatalf("EncodeDraft() = %v", err)
	}

	if got, draftErr := catalogstore.Draft(ref, good); draftErr != nil || got.Name != "postgres" {
		t.Fatalf("Draft(good) = %q, %v", got.Name, draftErr)
	}

	tests := []struct {
		name string
		doc  string
	}{
		{"a document that is not JSON", "{"},
		{"a field that the blueprint does not know", `{"name":"postgres","unknown":1}`},
		{"text after the document", good + " {}"},
		{"a document of another blueprint", strings.Replace(good, `"name":"postgres"`, `"name":"redis"`, 1)},
		{"a document above the limit", strings.Repeat(" ", catalog.MaxBlueprintBytes+1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, draftErr := catalogstore.Draft(ref, tc.doc); !errors.Is(draftErr, catalogstore.ErrCorrupt) {
				t.Fatalf("Draft() = %v, want ErrCorrupt", draftErr)
			}
		})
	}
}

func TestEncodeRefusesLargeDocuments(t *testing.T) {
	t.Parallel()

	b := blueprint("postgres")
	b.Description = strings.Repeat("a", catalog.MaxBlueprintBytes)

	if _, err := catalogstore.EncodeDraft(&b); !errors.Is(err, catalog.ErrLimit) {
		t.Fatalf("EncodeDraft() = %v, want ErrLimit", err)
	}

	if _, err := catalogstore.EncodeVersion(&b); !errors.Is(err, catalog.ErrLimit) {
		t.Fatalf("EncodeVersion() = %v, want ErrLimit", err)
	}
}

func entryOf(n namespace.Name) audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: n, Subject: "alice", IDPSubject: "", TokenID: "",
		Action: string(catalog.ActionBlueprintPublish), Object: "payments/postgres", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

func TestCheckDraftWrite(t *testing.T) {
	t.Parallel()

	good := blueprint("postgres")
	badName := blueprint("Postgres; DROP TABLE")

	tests := []struct {
		name    string
		b       *catalog.Blueprint
		entries []audit.Entry
		want    error
		version int64
	}{
		{"a valid write", &good, []audit.Entry{entryOf(ns)}, nil, 0},
		{"a name that is not valid", &badName, nil, catalog.ErrInvalid, 0},
		{"a negative version", &good, nil, catalog.ErrInvalid, -1},
		{"an entry of another namespace", &good, []audit.Entry{entryOf("billing")}, audit.ErrInvalidEntry, 0},
		{"an entry that fails its checks", &good, []audit.Entry{entryOf("")}, audit.ErrInvalidEntry, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := catalogstore.CheckDraftWrite(tc.b, tc.version, fence(), tc.entries)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("CheckDraftWrite() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCheckPublication(t *testing.T) {
	t.Parallel()

	good := func() catalog.Publication {
		return catalog.Publication{
			At: time.Now().UTC(), Ref: catalog.BlueprintRef{Namespace: ns, Name: "postgres"}, By: "platform",
			Blueprint: blueprint("postgres"), DraftVersion: 1,
		}
	}

	tests := []struct {
		mutate func(p *catalog.Publication)
		want   error
		name   string
	}{
		{func(*catalog.Publication) {}, nil, "a valid publication"},
		{
			func(p *catalog.Publication) { p.Ref.Namespace = "billing" },
			namespace.ErrFenced,
			"a reference of another namespace",
		},
		{
			func(p *catalog.Publication) { p.Ref.Name = "redis" },
			catalog.ErrInvalid,
			"a reference that names another blueprint",
		},
		{func(p *catalog.Publication) { p.DraftVersion = 0 }, catalog.ErrConflict, "draft version 0"},
		{func(p *catalog.Publication) { p.Blueprint.Title = "" }, catalog.ErrInvalid, "a document that fails Validate"},
		{
			func(p *catalog.Publication) { p.Blueprint.ExprVersion = "v1\n2" },
			catalog.ErrInvalid,
			"an Expr version with a control byte",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := good()
			tc.mutate(&p)

			err := catalogstore.CheckPublication(&p, fence(), nil)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("CheckPublication() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCheckRefAndExprVersion(t *testing.T) {
	t.Parallel()

	ref := catalog.BlueprintRef{Namespace: ns, Name: "postgres"}

	if err := catalogstore.CheckRef(ref, 1, fence(), nil); err != nil {
		t.Fatalf("CheckRef(valid) = %v", err)
	}

	other := catalog.BlueprintRef{Namespace: "billing", Name: "postgres"}

	if err := catalogstore.CheckRef(other, 1, fence(), nil); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("CheckRef(other namespace) = %v, want ErrFenced", err)
	}

	if err := catalogstore.CheckRef(ref, 0, fence(), nil); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("CheckRef(version 0) = %v, want ErrNotFound", err)
	}

	for _, v := range []string{"", strings.Repeat("v", 65), "v1 .17", "v1.17\n", "v1.17;", "é"} {
		if err := catalogstore.CheckExprVersion(v); !errors.Is(err, catalog.ErrInvalid) {
			t.Errorf("CheckExprVersion(%q) = %v, want ErrInvalid", v, err)
		}
	}

	if err := catalogstore.CheckExprVersion("v1.17.8-rc_1+b2"); err != nil {
		t.Errorf("CheckExprVersion(valid) = %v", err)
	}
}

func TestCheckStoredDraft(t *testing.T) {
	t.Parallel()

	ref := catalog.BlueprintRef{Namespace: ns, Name: "postgres"}
	stored := blueprint("postgres")

	doc, err := catalogstore.EncodeDraft(&stored)
	if err != nil {
		t.Fatalf("EncodeDraft() = %v", err)
	}

	same := blueprint("postgres")
	same.ExprVersion = "v1.17.8"

	if checkErr := catalogstore.CheckStoredDraft(ref, doc, &same); checkErr != nil {
		t.Fatalf("CheckStoredDraft(the draft with an Expr version) = %v, want nil", checkErr)
	}

	changed := blueprint("postgres")
	changed.Title = "Not the draft"

	if checkErr := catalogstore.CheckStoredDraft(ref, doc, &changed); !errors.Is(checkErr, catalog.ErrInvalid) {
		t.Fatalf("CheckStoredDraft(another document) = %v, want ErrInvalid", checkErr)
	}

	if checkErr := catalogstore.CheckStoredDraft(ref, "{", &same); !errors.Is(checkErr, catalogstore.ErrCorrupt) {
		t.Fatalf("CheckStoredDraft(a stored draft that is not JSON) = %v, want ErrCorrupt", checkErr)
	}
}
