package catalogtest

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/internal/policy"
	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/audit"
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

func putDraft(t *testing.T, h BlueprintHarness, f fixture, b catalog.Blueprint, version int64) int64 {
	t.Helper()

	got, err := h.Blueprints.PutDraft(t.Context(), b, version, f.fence, blueprintEntries())
	if err != nil {
		t.Fatalf("PutDraft(%s at %d) = %v", b.Name, version, err)
	}

	return got
}

func publish(t *testing.T, h BlueprintHarness, f fixture, name string, draft int64) catalog.Version {
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

func testDraft(t *testing.T, h BlueprintHarness, f fixture) {
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

func testPublish(t *testing.T, h BlueprintHarness, f fixture) {
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

func testPublishStale(t *testing.T, h BlueprintHarness, f fixture) {
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

func testRetire(t *testing.T, h BlueprintHarness, f fixture) {
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

func testListBlueprints(t *testing.T, h BlueprintHarness, f fixture) {
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

func testBlueprintFenced(t *testing.T, h BlueprintHarness, f fixture) {
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

func testBlueprintAudit(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	count := func(want int) {
		t.Helper()

		got, err := h.Domain.AuditEntries(t.Context(), ns)
		if err != nil || len(got) != want {
			t.Fatalf("AuditEntries() = %d entries, %v, want %d", len(got), err, want)
		}
	}

	putDraft(t, h, f, blueprint("postgres", "First"), 0)
	count(1)

	// A refused write appends no entry.
	_, err := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "x"), 0, f.fence, blueprintEntries())
	if !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("PutDraft(stale) = %v, want ErrConflict", err)
	}

	count(1)

	publish(t, h, f, "postgres", 1)
	count(2)

	retireErr := h.Blueprints.Retire(t.Context(), bref("postgres"), 1, f.fence, blueprintEntries())
	if retireErr != nil {
		t.Fatalf("Retire() = %v", retireErr)
	}

	count(3)

	err = h.Blueprints.Retire(t.Context(), bref("postgres"), 2, f.fence, blueprintEntries())
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("Retire(unknown) = %v, want ErrNotFound", err)
	}

	count(3)
}

func testPublishNoDraft(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	for _, at := range []int64{0, 1} {
		p := catalog.Publication{
			At: time.Now().UTC(), Ref: bref("postgres"), By: "platform",
			Blueprint: blueprint("postgres", "First"), DraftVersion: at,
		}

		_, err := h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())
		if !errors.Is(err, catalog.ErrConflict) {
			t.Fatalf("Publish(no draft, at %d) = %v, want ErrConflict", at, err)
		}
	}

	if _, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("Version(1) = %v, want ErrNotFound", err)
	}
}

func testBlueprintNamespaces(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	const other = namespace.Name("billing")

	otherFence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	putDraft(t, h, f, blueprint("postgres", "Payments"), 0)
	publish(t, h, f, "postgres", 1)

	otherRef := catalog.BlueprintRef{Namespace: other, Name: "postgres"}
	if _, _, readErr := h.Blueprints.Draft(t.Context(), otherRef); !errors.Is(readErr, catalog.ErrNotFound) {
		t.Fatalf("Draft(other namespace) = %v, want ErrNotFound", readErr)
	}

	if _, readErr := h.Blueprints.Version(t.Context(), otherRef, 1); !errors.Is(readErr, catalog.ErrNotFound) {
		t.Fatalf("Version(other namespace) = %v, want ErrNotFound", readErr)
	}

	if got, listErr := h.Blueprints.ListBlueprints(t.Context(), other); listErr != nil || len(got) != 0 {
		t.Fatalf("ListBlueprints(other) = %d, %v, want none", len(got), listErr)
	}

	// The same name in the other namespace is another blueprint, at its own version.
	e := entry(catalog.ActionBlueprintPublish, "billing/postgres")
	e.Namespace = other

	v, err := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "Billing"), 0, otherFence, []audit.Entry{e})
	if err != nil || v != 1 {
		t.Fatalf("PutDraft(other namespace) = %d, %v, want 1", v, err)
	}

	if got, _, readErr := h.Blueprints.Draft(t.Context(), bref("postgres")); readErr != nil || got.Title != "Payments" {
		t.Fatalf("Draft() = %q, %v, want the first namespace title", got.Title, readErr)
	}

	// An audit entry of another namespace writes nothing.
	_, err = h.Blueprints.PutDraft(t.Context(), blueprint("redis", "x"), 0, otherFence, blueprintEntries())
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("PutDraft(entry of another namespace) = %v, want ErrInvalidEntry", err)
	}

	if _, _, err := h.Blueprints.Draft(t.Context(), catalog.BlueprintRef{Namespace: other, Name: "redis"}); !errors.Is(
		err, catalog.ErrNotFound,
	) {
		t.Fatalf("Draft(redis) after a refused write = %v, want ErrNotFound", err)
	}
}

func testBlueprintRoundTrip(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	b := blueprint("postgres", "PostgreSQL")
	b.Description = "A database."
	b.Inputs[0].Validate = []catalog.Rule{{Rule: "value != ''", Message: "Give a size."}}
	b.Policy = []catalog.PolicyRule{{
		When: "inputs.size == 'large'", Effect: policy.EffectApprove, Message: "A DBA approves a large database.",
		Groups: []string{"dba"}, Approvals: 1,
	}}
	b.Quotas = []catalog.QuotaRule{{
		Kind: quota.KindUnits, Scope: quota.ScopeNamespace, Window: "", Rule: "", Message: "", Max: 50,
	}}

	putDraft(t, h, f, b, 0)

	got, _, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
	if err != nil || !reflect.DeepEqual(got, b) {
		t.Fatalf("Draft() = %+v, %v, want %+v", got, err, b)
	}

	v := publish(t, h, f, "postgres", 1)
	b.ExprVersion = "v1.17.8"

	if !reflect.DeepEqual(v.Blueprint, b) {
		t.Fatalf("Publish() = %+v, want %+v", v.Blueprint, b)
	}

	read, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1)
	if err != nil || !reflect.DeepEqual(read.Blueprint, b) || read.PublishedBy != "platform" ||
		!read.PublishedAt.Equal(v.PublishedAt) || read.Namespace != ns || read.Number != 1 {
		t.Fatalf("Version(1) = %+v, %v, want %+v", read, err, v)
	}
}

func testBlueprintLimit(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	for i := range catalog.MaxBlueprints {
		name := "bp" + strconv.Itoa(i)
		if _, err := h.Blueprints.PutDraft(t.Context(), blueprint(name, "T"), 0, f.fence, nil); err != nil {
			t.Fatalf("PutDraft(%s) = %v", name, err)
		}
	}

	_, err := h.Blueprints.PutDraft(t.Context(), blueprint("over", "T"), 0, f.fence, blueprintEntries())
	if !errors.Is(err, catalog.ErrLimit) {
		t.Fatalf("PutDraft(over the limit) = %v, want ErrLimit", err)
	}

	if _, _, err := h.Blueprints.Draft(t.Context(), bref("over")); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("Draft(over) = %v, want ErrNotFound", err)
	}

	// A change of a draft that exists is not a new blueprint.
	if _, err := h.Blueprints.PutDraft(t.Context(), blueprint("bp0", "U"), 1, f.fence, nil); err != nil {
		t.Fatalf("PutDraft(existing, at the limit) = %v", err)
	}
}

func testVersionLimit(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	putDraft(t, h, f, blueprint("postgres", "T"), 0)

	b, _, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
	if err != nil {
		t.Fatalf("Draft() = %v", err)
	}

	p := catalog.Publication{At: time.Now().UTC(), Ref: bref("postgres"), By: "platform", Blueprint: b, DraftVersion: 1}

	for i := range catalog.MaxVersions {
		v, pubErr := h.Blueprints.Publish(t.Context(), p, f.fence, nil)
		if pubErr != nil || v.Number != i+1 {
			t.Fatalf("Publish() = %d, %v, want %d", v.Number, pubErr, i+1)
		}
	}

	_, err = h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())
	if !errors.Is(err, catalog.ErrLimit) {
		t.Fatalf("Publish(over the limit) = %v, want ErrLimit", err)
	}
}
