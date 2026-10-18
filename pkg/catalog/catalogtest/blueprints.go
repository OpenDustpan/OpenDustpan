package catalogtest

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/catalog"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func blueprint(name, title string) catalog.Blueprint {
	module := catalog.Module{
		Source: "registry.example.com/platform/postgres/aws", Version: "2.4.1",
		Connection: "", Repository: "", Directory: "", Revision: "",
	}

	return catalog.Blueprint{
		Program: catalog.Program{Module: &module, Component: nil},
		Platform: catalog.Platform{
			Values:  map[string]json.RawMessage{"backup_retention_days": json.RawMessage(`14`)},
			Profile: "",
			Pool:    "",
		},
		Delivery: catalog.Delivery{
			Git:     nil,
			Default: catalog.HomeOpenDustpan,
			Homes:   []catalog.HomeKind{catalog.HomeOpenDustpan},
		},
		Name:            name,
		Title:           title,
		Description:     "",
		Tool:            "opentofu",
		ExprVersion:     "",
		UnitName:        "'databases/' + request.name",
		SourcePrincipal: "",
		Inputs: []catalog.Input{{
			Choice: nil, Name: "size", Title: "", Description: "", Type: catalog.TypeString, Maps: "instance_size",
			Default: json.RawMessage(`"small"`), Validate: nil, Required: false,
		}},
		Policy:        nil,
		Quotas:        nil,
		SchemaVersion: 1,
	}
}

func bref(name string) catalog.BlueprintRef {
	return catalog.BlueprintRef{Namespace: ns, Name: name}
}

func blueprintEntries() []audit.Entry {
	return []audit.Entry{entry(catalog.ActionBlueprintPublish, "payments/postgres")}
}

func putDraft(t *testing.T, h Harness, f fixture, b catalog.Blueprint, version int64) int64 {
	t.Helper()

	got, err := h.Blueprints.PutDraft(t.Context(), b, version, f.fence, blueprintEntries())
	if err != nil {
		t.Fatalf("PutDraft(%s at %d) = %v", b.Name, version, err)
	}

	return got
}

func publish(t *testing.T, h Harness, f fixture, name string, draft int64) catalog.Version {
	t.Helper()

	b, at, err := h.Blueprints.Draft(t.Context(), bref(name))
	if err != nil || at != draft {
		t.Fatalf("Draft(%s) = version %d, %v, want %d", name, at, err, draft)
	}

	b.ExprVersion = "v1.17.8"
	p := catalog.Publication{At: time.Now().UTC(), Ref: bref(name), By: "platform", Blueprint: b, DraftVersion: draft}

	v, err := h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())
	if err != nil {
		t.Fatalf("Publish(%s) = %v", name, err)
	}

	return v
}

func testDraft(t *testing.T, h Harness, f fixture) {
	t.Helper()

	if v := putDraft(t, h, f, blueprint("postgres", "PostgreSQL"), 0); v != 1 {
		t.Fatalf("PutDraft(new) = %d, want 1", v)
	}

	if v := putDraft(t, h, f, blueprint("postgres", "PostgreSQL database"), 1); v != 2 {
		t.Fatalf("PutDraft(at 1) = %d, want 2", v)
	}

	b, v, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
	if err != nil || v != 2 || b.Title != "PostgreSQL database" {
		t.Fatalf("Draft() = %q at %d, %v", b.Title, v, err)
	}

	for _, stale := range []int64{0, 1, 3} {
		_, err := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "x"), stale, f.fence, blueprintEntries())
		if !errors.Is(err, catalog.ErrConflict) {
			t.Fatalf("PutDraft(at %d) = %v, want ErrConflict", stale, err)
		}
	}

	if _, _, err := h.Blueprints.Draft(t.Context(), bref("redis")); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("Draft(unknown) = %v, want ErrNotFound", err)
	}
}

func testPublish(t *testing.T, h Harness, f fixture) {
	t.Helper()

	putDraft(t, h, f, blueprint("postgres", "First"), 0)
	v1 := publish(t, h, f, "postgres", 1)
	putDraft(t, h, f, blueprint("postgres", "Second"), 1)
	v2 := publish(t, h, f, "postgres", 2)

	if v1.Number != 1 || v2.Number != 2 || v2.Blueprint.ExprVersion != "v1.17.8" || v2.PublishedBy != "platform" {
		t.Fatalf("Publish() = %d and %+v, want 1 and 2", v1.Number, v2)
	}

	got, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1)
	if err != nil || got.Blueprint.Title != "First" || got.Retired {
		t.Fatalf("Version(1) = %q, %v, want the first title", got.Blueprint.Title, err)
	}

	if _, err := h.Blueprints.Version(t.Context(), bref("postgres"), 3); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("Version(3) = %v, want ErrNotFound", err)
	}
}

func testPublishStale(t *testing.T, h Harness, f fixture) {
	t.Helper()

	putDraft(t, h, f, blueprint("postgres", "First"), 0)
	b, _, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
	if err != nil {
		t.Fatalf("Draft() = %v", err)
	}

	// A person changes the draft after the check of the publish.
	putDraft(t, h, f, blueprint("postgres", "Changed"), 1)

	p := catalog.Publication{At: time.Now().UTC(), Ref: bref("postgres"), By: "platform", Blueprint: b, DraftVersion: 1}
	_, err = h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())
	if !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("Publish(stale draft) = %v, want ErrConflict", err)
	}
}

func testRetire(t *testing.T, h Harness, f fixture) {
	t.Helper()

	putDraft(t, h, f, blueprint("postgres", "First"), 0)
	publish(t, h, f, "postgres", 1)

	for range 2 {
		if err := h.Blueprints.Retire(t.Context(), bref("postgres"), 1, f.fence, blueprintEntries()); err != nil {
			t.Fatalf("Retire() = %v", err)
		}
	}

	if got, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1); err != nil || !got.Retired {
		t.Fatalf("Version(1) = retired %v, %v, want retired", got.Retired, err)
	}

	err := h.Blueprints.Retire(t.Context(), bref("postgres"), 2, f.fence, nil)
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("Retire(unknown) = %v, want ErrNotFound", err)
	}

	if err := h.Blueprints.SetExprVersion(t.Context(), bref("postgres"), 1, "v1.18.0", f.fence); err != nil {
		t.Fatalf("SetExprVersion() = %v", err)
	}

	if got, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1); err != nil ||
		got.Blueprint.ExprVersion != "v1.18.0" {
		t.Fatalf("Version(1) = Expr %q, %v, want v1.18.0", got.Blueprint.ExprVersion, err)
	}
}

func testListBlueprints(t *testing.T, h Harness, f fixture) {
	t.Helper()

	putDraft(t, h, f, blueprint("redis", "Redis"), 0)
	publish(t, h, f, "redis", 1)
	putDraft(t, h, f, blueprint("postgres", "First"), 0)
	publish(t, h, f, "postgres", 1)
	putDraft(t, h, f, blueprint("postgres", "Second"), 1)
	publish(t, h, f, "postgres", 2)
	// A draft with no published version is not in the list.
	putDraft(t, h, f, blueprint("kafka", "Kafka"), 0)

	got, err := h.Blueprints.ListBlueprints(t.Context(), ns)
	if err != nil || len(got) != 2 {
		t.Fatalf("List() = %d blueprints, %v, want 2", len(got), err)
	}

	if got[0].Blueprint.Name != "postgres" || got[0].Number != 2 || got[1].Blueprint.Name != "redis" {
		t.Fatalf(
			"List() = %s %d, %s, want postgres 2, redis",
			got[0].Blueprint.Name,
			got[0].Number,
			got[1].Blueprint.Name,
		)
	}
}

func testBlueprintFenced(t *testing.T, h Harness, f fixture) {
	t.Helper()

	if _, err := h.Domain.BumpFence(t.Context(), f.fence); err != nil {
		t.Fatalf("BumpFence() = %v", err)
	}

	_, err := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "x"), 0, f.fence, blueprintEntries())
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("PutDraft(stale fence) = %v, want ErrFenced", err)
	}

	if _, _, err := h.Blueprints.Draft(t.Context(), bref("postgres")); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("Draft() after a fenced write = %v, want ErrNotFound", err)
	}
}
