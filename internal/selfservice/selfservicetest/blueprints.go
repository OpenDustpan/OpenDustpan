package selfservicetest

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/policy"
	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func blueprint(name, title string) selfservice.Blueprint {
	module := selfservice.Module{
		Source: "registry.example.com/platform/postgres/aws", Version: "2.4.1",
		Connection: "", Repository: "", Directory: "", Revision: "",
	}

	return selfservice.Blueprint{
		Program: selfservice.Program{Module: &module, Component: nil},
		Platform: selfservice.Platform{
			Values:  map[string]json.RawMessage{"backup_retention_days": json.RawMessage(`14`)},
			Profile: "",
			Pool:    "",
		},
		Delivery: selfservice.Delivery{
			Git:     nil,
			Default: selfservice.HomeOpenDustpan,
			Homes:   []selfservice.HomeKind{selfservice.HomeOpenDustpan},
		},
		Name:            name,
		Title:           title,
		Description:     "",
		Tool:            "opentofu",
		ExprVersion:     "",
		UnitName:        "'databases/' + request.name",
		SourcePrincipal: "",
		Inputs: []selfservice.Input{{
			Choice: nil, Name: "size", Title: "", Description: "", Type: selfservice.TypeString, Maps: "instance_size",
			Default: json.RawMessage(`"small"`), Validate: nil, Required: false,
		}},
		Policy:        nil,
		Quotas:        nil,
		SchemaVersion: 1,
	}
}

func bref(name string) selfservice.BlueprintRef {
	return selfservice.BlueprintRef{Namespace: ns, Name: name}
}

func blueprintEntries() []audit.Entry {
	return []audit.Entry{entry(selfservice.ActionBlueprintPublish, "payments/postgres")}
}

func putDraft(t *testing.T, h BlueprintHarness, f fixture, b selfservice.Blueprint, version int64) int64 {
	t.Helper()

	got, err := h.Blueprints.PutDraft(t.Context(), b, version, f.fence, blueprintEntries())
	if err != nil {
		t.Fatalf("PutDraft(%s at %d) = %v", b.Name, version, err)
	}

	return got
}

func publish(t *testing.T, h BlueprintHarness, f fixture, name string, draft int64) selfservice.Version {
	t.Helper()

	b, at, err := h.Blueprints.Draft(t.Context(), bref(name))
	if err != nil || at != draft {
		t.Fatalf("Draft(%s) = version %d, %v, want %d", name, at, err, draft)
	}

	b.ExprVersion = "v1.17.8"
	p := selfservice.Publication{At: time.Now().UTC(), Ref: bref(name), By: "platform", Blueprint: b, DraftVersion: draft}

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
		if !errors.Is(err, selfservice.ErrConflict) {
			t.Fatalf("PutDraft(at %d) = %v, want ErrConflict", stale, err)
		}
	}

	if _, _, err := h.Blueprints.Draft(t.Context(), bref("redis")); !errors.Is(err, selfservice.ErrNotFound) {
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

	if _, err := h.Blueprints.Version(t.Context(), bref("postgres"), 3); !errors.Is(err, selfservice.ErrNotFound) {
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

	p := selfservice.Publication{At: time.Now().UTC(), Ref: bref("postgres"), By: "platform", Blueprint: b, DraftVersion: 1}
	_, err = h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())
	if !errors.Is(err, selfservice.ErrConflict) {
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
	if !errors.Is(err, selfservice.ErrNotFound) {
		t.Fatalf("Retire(unknown) = %v, want ErrNotFound", err)
	}

	err = h.Blueprints.SetExprVersion(
		t.Context(),
		selfservice.ExprVersionChange{Ref: bref("postgres"), Number: 1, Expr: "v1.18.0"},
		f.fence,
		blueprintEntries(),
	)
	if err != nil {
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

	if _, _, err := h.Blueprints.Draft(t.Context(), bref("postgres")); !errors.Is(err, selfservice.ErrNotFound) {
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
	if !errors.Is(err, selfservice.ErrConflict) {
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
	if !errors.Is(err, selfservice.ErrNotFound) {
		t.Fatalf("Retire(unknown) = %v, want ErrNotFound", err)
	}

	count(3)

	setErr := h.Blueprints.SetExprVersion(
		t.Context(),
		selfservice.ExprVersionChange{Ref: bref("postgres"), Number: 1, Expr: "v1.18.0"},
		f.fence,
		blueprintEntries(),
	)
	if setErr != nil {
		t.Fatalf("SetExprVersion() = %v", setErr)
	}

	count(4)

	err = h.Blueprints.SetExprVersion(
		t.Context(),
		selfservice.ExprVersionChange{Ref: bref("postgres"), Number: 2, Expr: "v1.18.0"},
		f.fence,
		blueprintEntries(),
	)
	if !errors.Is(err, selfservice.ErrNotFound) {
		t.Fatalf("SetExprVersion(unknown) = %v, want ErrNotFound", err)
	}

	count(4)
}

func testPublishNoDraft(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	for _, at := range []int64{0, 1} {
		p := selfservice.Publication{
			At: time.Now().UTC(), Ref: bref("postgres"), By: "platform",
			Blueprint: blueprint("postgres", "First"), DraftVersion: at,
		}

		_, err := h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())
		if !errors.Is(err, selfservice.ErrConflict) {
			t.Fatalf("Publish(no draft, at %d) = %v, want ErrConflict", at, err)
		}
	}

	if _, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1); !errors.Is(err, selfservice.ErrNotFound) {
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

	otherRef := selfservice.BlueprintRef{Namespace: other, Name: "postgres"}
	if _, _, readErr := h.Blueprints.Draft(t.Context(), otherRef); !errors.Is(readErr, selfservice.ErrNotFound) {
		t.Fatalf("Draft(other namespace) = %v, want ErrNotFound", readErr)
	}

	if _, readErr := h.Blueprints.Version(t.Context(), otherRef, 1); !errors.Is(readErr, selfservice.ErrNotFound) {
		t.Fatalf("Version(other namespace) = %v, want ErrNotFound", readErr)
	}

	if got, listErr := h.Blueprints.ListBlueprints(t.Context(), other); listErr != nil || len(got) != 0 {
		t.Fatalf("ListBlueprints(other) = %d, %v, want none", len(got), listErr)
	}

	// The same name in the other namespace is another blueprint, at its own version.
	e := entry(selfservice.ActionBlueprintPublish, "billing/postgres")
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

	if _, _, err := h.Blueprints.Draft(t.Context(), selfservice.BlueprintRef{Namespace: other, Name: "redis"}); !errors.Is(
		err, selfservice.ErrNotFound,
	) {
		t.Fatalf("Draft(redis) after a refused write = %v, want ErrNotFound", err)
	}
}

func testBlueprintRoundTrip(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	b := blueprint("postgres", "PostgreSQL")
	b.Description = "A database."
	b.Inputs[0].Validate = []selfservice.Rule{{Rule: "value != ''", Message: "Give a size."}}
	b.Policy = []selfservice.PolicyRule{{
		When: "inputs.size == 'large'", Effect: policy.EffectApprove, Message: "A DBA approves a large database.",
		Groups: []string{"dba"}, Approvals: 1,
	}}
	b.Quotas = []selfservice.QuotaRule{{
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

	for i := range selfservice.MaxBlueprints {
		name := "bp" + strconv.Itoa(i)
		if _, err := h.Blueprints.PutDraft(t.Context(), blueprint(name, "T"), 0, f.fence, nil); err != nil {
			t.Fatalf("PutDraft(%s) = %v", name, err)
		}
	}

	_, err := h.Blueprints.PutDraft(t.Context(), blueprint("over", "T"), 0, f.fence, blueprintEntries())
	if !errors.Is(err, selfservice.ErrLimit) {
		t.Fatalf("PutDraft(over the limit) = %v, want ErrLimit", err)
	}

	if _, _, err := h.Blueprints.Draft(t.Context(), bref("over")); !errors.Is(err, selfservice.ErrNotFound) {
		t.Fatalf("Draft(over) = %v, want ErrNotFound", err)
	}

	// A change of a draft that exists is not a new blueprint.
	if _, err := h.Blueprints.PutDraft(t.Context(), blueprint("bp0", "U"), 1, f.fence, nil); err != nil {
		t.Fatalf("PutDraft(existing, at the limit) = %v", err)
	}
}

func testVersionLimit(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	for i := range selfservice.MaxVersions {
		var at int64
		if i > 0 {
			at = int64(i)
		}

		if _, err := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "T"), at, f.fence, nil); err != nil {
			t.Fatalf("PutDraft(at %d) = %v", at, err)
		}

		b, _, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
		if err != nil {
			t.Fatalf("Draft() = %v", err)
		}

		p := selfservice.Publication{
			At: time.Now().UTC(), Ref: bref("postgres"), By: "platform", Blueprint: b, DraftVersion: at + 1,
		}

		v, pubErr := h.Blueprints.Publish(t.Context(), p, f.fence, nil)
		if pubErr != nil || v.Number != i+1 {
			t.Fatalf("Publish() = %d, %v, want %d", v.Number, pubErr, i+1)
		}
	}

	at := int64(selfservice.MaxVersions)
	if _, err := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "T"), at, f.fence, nil); err != nil {
		t.Fatalf("PutDraft(at %d) = %v", at, err)
	}

	b, _, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
	if err != nil {
		t.Fatalf("Draft() = %v", err)
	}

	p := selfservice.Publication{
		At: time.Now().UTC(), Ref: bref("postgres"), By: "platform", Blueprint: b, DraftVersion: at + 1,
	}

	_, err = h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())
	if !errors.Is(err, selfservice.ErrLimit) {
		t.Fatalf("Publish(over the limit) = %v, want ErrLimit", err)
	}
}

// otherNamespace makes the namespace billing with a published postgres
// blueprint, and returns its fence and the entries of its writes.
func otherNamespace(t *testing.T, h BlueprintHarness) (namespace.Fence, []audit.Entry) {
	t.Helper()

	const other = namespace.Name("billing")

	fence, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace(%s) = %v", other, err)
	}

	e := entry(selfservice.ActionBlueprintPublish, "billing/postgres")
	e.Namespace = other
	entries := []audit.Entry{e}

	_, putErr := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "Billing"), 0, fence, entries)
	if putErr != nil {
		t.Fatalf("PutDraft(%s) = %v", other, putErr)
	}

	b, _, err := h.Blueprints.Draft(t.Context(), selfservice.BlueprintRef{Namespace: other, Name: "postgres"})
	if err != nil {
		t.Fatalf("Draft(%s) = %v", other, err)
	}

	b.ExprVersion = "v1.17.8"
	p := selfservice.Publication{
		At: time.Now().UTC(), Ref: selfservice.BlueprintRef{Namespace: other, Name: "postgres"}, By: "platform",
		Blueprint: b, DraftVersion: 1,
	}

	if _, pubErr := h.Blueprints.Publish(t.Context(), p, fence, entries); pubErr != nil {
		t.Fatalf("Publish(%s) = %v", other, pubErr)
	}

	return fence, entries
}

// testRefOfAnotherNamespace proves that a reference of one namespace never
// writes under the fence of another: Publish, Retire and SetExprVersion each
// refuse it with ErrFenced, and the other namespace keeps its rows and its
// audit entries.
func testRefOfAnotherNamespace(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	otherFence, _ := otherNamespace(t, h)
	other := selfservice.BlueprintRef{Namespace: otherFence.Namespace, Name: "postgres"}

	before, err := h.Domain.AuditEntries(t.Context(), otherFence.Namespace)
	if err != nil {
		t.Fatalf("AuditEntries() = %v", err)
	}

	putDraft(t, h, f, blueprint("postgres", "Payments"), 0)

	b, _, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
	if err != nil {
		t.Fatalf("Draft() = %v", err)
	}

	b.ExprVersion = "v1.17.8"
	// The fence is the one of payments, the reference names billing.
	p := selfservice.Publication{At: time.Now().UTC(), Ref: other, By: "platform", Blueprint: b, DraftVersion: 1}

	_, pubErr := h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())
	retireErr := h.Blueprints.Retire(t.Context(), other, 1, f.fence, blueprintEntries())
	exprErr := h.Blueprints.SetExprVersion(
		t.Context(),
		selfservice.ExprVersionChange{Ref: other, Number: 1, Expr: "v9.9.9"},
		f.fence,
		blueprintEntries(),
	)

	for name, got := range map[string]error{"Publish": pubErr, "Retire": retireErr, "SetExprVersion": exprErr} {
		if !errors.Is(got, namespace.ErrFenced) {
			t.Errorf("%s(ref of another namespace) = %v, want ErrFenced", name, got)
		}
	}

	v, err := h.Blueprints.Version(t.Context(), other, 1)
	if err != nil || v.Retired || v.Blueprint.ExprVersion != "v1.17.8" {
		t.Fatalf(
			"Version(billing, 1) = retired %v, Expr %q, %v, want it unchanged",
			v.Retired,
			v.Blueprint.ExprVersion,
			err,
		)
	}

	if _, verErr := h.Blueprints.Version(t.Context(), bref("postgres"), 1); !errors.Is(verErr, selfservice.ErrNotFound) {
		t.Fatalf("Version(payments, 1) = %v, want ErrNotFound: the publish wrote under the fence", verErr)
	}

	after, err := h.Domain.AuditEntries(t.Context(), otherFence.Namespace)
	if err != nil || len(after) != len(before) {
		t.Fatalf("AuditEntries(billing) = %d entries, %v, want %d", len(after), err, len(before))
	}
}

// testPublishRefusals proves the checks of a publish that run before any row
// is read: the document, its name, its Expr version and the draft version.
// Each refusal leaves no version and no audit entry.
func testPublishRefusals(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	putDraft(t, h, f, blueprint("postgres", "First"), 0)

	good := blueprint("postgres", "First")
	good.ExprVersion = "v1.17.8"

	noTitle := good
	noTitle.Title = ""

	renamed := good
	renamed.Name = "redis"

	badExpr := good
	badExpr.ExprVersion = "v1.17\n8"

	tests := []struct {
		name string
		b    selfservice.Blueprint
		want error
		at   int64
	}{
		{"a document that fails Validate", noTitle, selfservice.ErrInvalid, 1},
		{"a name that is not the name of the reference", renamed, selfservice.ErrInvalid, 1},
		{"an Expr version that is not a version", badExpr, selfservice.ErrInvalid, 1},
		{"draft version 0", good, selfservice.ErrConflict, 0},
	}

	for i := range tests {
		tc := &tests[i]
		p := selfservice.Publication{
			At: time.Now().UTC(), Ref: bref("postgres"), By: "platform", Blueprint: tc.b, DraftVersion: tc.at,
		}

		if _, err := h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries()); !errors.Is(err, tc.want) {
			t.Errorf("Publish(%s) = %v, want %v", tc.name, err, tc.want)
		}
	}

	if _, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1); !errors.Is(err, selfservice.ErrNotFound) {
		t.Fatalf("Version(1) = %v, want ErrNotFound", err)
	}

	if got, err := h.Domain.AuditEntries(t.Context(), ns); err != nil || len(got) != 1 {
		t.Fatalf("AuditEntries() = %d entries, %v, want only the one of the draft", len(got), err)
	}
}

// testBadEntries proves that each write refuses an audit entry that fails
// its checks and changes nothing.
func testBadEntries(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	putDraft(t, h, f, blueprint("postgres", "First"), 0)
	publish(t, h, f, "postgres", 1)

	bad := []audit.Entry{entry(selfservice.ActionBlueprintPublish, "payments/postgres")}
	bad[0].Subject = ""

	b, _, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
	if err != nil {
		t.Fatalf("Draft() = %v", err)
	}

	b.ExprVersion = "v1.17.8"
	next := selfservice.Publication{
		At:           time.Now().UTC(),
		Ref:          bref("postgres"),
		By:           "platform",
		Blueprint:    b,
		DraftVersion: 2,
	}

	_, putErr := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "x"), 1, f.fence, bad)
	_, pubErr := h.Blueprints.Publish(t.Context(), next, f.fence, bad)
	retireErr := h.Blueprints.Retire(t.Context(), bref("postgres"), 1, f.fence, bad)
	exprErr := h.Blueprints.SetExprVersion(
		t.Context(),
		selfservice.ExprVersionChange{Ref: bref("postgres"), Number: 1, Expr: "v1.18.0"},
		f.fence,
		bad,
	)

	for name, got := range map[string]error{
		"PutDraft": putErr, "Publish": pubErr, "Retire": retireErr, "SetExprVersion": exprErr,
	} {
		if !errors.Is(got, audit.ErrInvalidEntry) {
			t.Errorf("%s(invalid entry) = %v, want ErrInvalidEntry", name, got)
		}
	}

	read, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1)
	if err != nil || read.Retired || read.Blueprint.ExprVersion != "v1.17.8" {
		t.Fatalf(
			"Version(1) = retired %v, Expr %q, %v, want it unchanged",
			read.Retired,
			read.Blueprint.ExprVersion,
			err,
		)
	}

	if _, _, err := h.Blueprints.Draft(t.Context(), bref("postgres")); err != nil {
		t.Fatalf("Draft() = %v", err)
	}

	if entries, err := h.Domain.AuditEntries(t.Context(), ns); err != nil || len(entries) != 2 {
		t.Fatalf("AuditEntries() = %d entries, %v, want the 2 of the draft and the publish", len(entries), err)
	}
}

// testBadVersionInput proves that Retire and SetExprVersion refuse a version
// number below 1 with ErrNotFound, and SetExprVersion refuses an Expr version
// that is not a version with ErrInvalid.
func testBadVersionInput(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	putDraft(t, h, f, blueprint("postgres", "First"), 0)
	publish(t, h, f, "postgres", 1)

	for _, n := range []int{0, -1} {
		retireErr := h.Blueprints.Retire(t.Context(), bref("postgres"), n, f.fence, blueprintEntries())
		if !errors.Is(retireErr, selfservice.ErrNotFound) {
			t.Errorf("Retire(version %d) = %v, want ErrNotFound", n, retireErr)
		}

		exprErr := h.Blueprints.SetExprVersion(
			t.Context(),
			selfservice.ExprVersionChange{Ref: bref("postgres"), Number: n, Expr: "v1.18.0"},
			f.fence,
			blueprintEntries(),
		)
		if !errors.Is(exprErr, selfservice.ErrNotFound) {
			t.Errorf("SetExprVersion(version %d) = %v, want ErrNotFound", n, exprErr)
		}
	}

	for _, expr := range []string{"", "v1 8", "v1.8\n"} {
		err := h.Blueprints.SetExprVersion(
			t.Context(),
			selfservice.ExprVersionChange{Ref: bref("postgres"), Number: 1, Expr: expr},
			f.fence,
			blueprintEntries(),
		)
		if !errors.Is(err, selfservice.ErrInvalid) {
			t.Errorf("SetExprVersion(%q) = %v, want ErrInvalid", expr, err)
		}
	}

	got, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1)
	if err != nil || got.Retired || got.Blueprint.ExprVersion != "v1.17.8" {
		t.Fatalf("Version(1) = retired %v, Expr %q, %v, want it unchanged", got.Retired, got.Blueprint.ExprVersion, err)
	}

	if entries, auditErr := h.Domain.AuditEntries(t.Context(), ns); auditErr != nil || len(entries) != 2 {
		t.Fatalf("AuditEntries() = %d entries, %v, want the 2 of the draft and the publish", len(entries), auditErr)
	}
}

// testPublishStoredDraft proves that a publish records the stored draft and
// that one draft version gives one version. A retire is for one version, so
// a publish after a retire is allowed.
func testPublishStoredDraft(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	putDraft(t, h, f, blueprint("postgres", "First"), 0)

	changed := blueprint("postgres", "Not the draft")
	changed.ExprVersion = "v1.17.8"
	p := selfservice.Publication{
		At: time.Now().UTC(), Ref: bref("postgres"), By: "platform", Blueprint: changed, DraftVersion: 1,
	}

	_, err := h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())
	if !errors.Is(err, selfservice.ErrInvalid) {
		t.Fatalf("Publish(a document that is not the draft) = %v, want ErrInvalid", err)
	}

	v := publish(t, h, f, "postgres", 1)
	if v.DraftVersion != 1 {
		t.Fatalf("Publish() = draft version %d, want 1", v.DraftVersion)
	}

	b, _, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
	if err != nil {
		t.Fatalf("Draft() = %v", err)
	}

	b.ExprVersion = "v1.17.8"
	again := selfservice.Publication{
		At:           time.Now().UTC(),
		Ref:          bref("postgres"),
		By:           "platform",
		Blueprint:    b,
		DraftVersion: 1,
	}

	_, pubErr := h.Blueprints.Publish(t.Context(), again, f.fence, blueprintEntries())
	if !errors.Is(pubErr, selfservice.ErrAlreadyPublished) {
		t.Fatalf("Publish(the same draft again) = %v, want ErrAlreadyPublished", pubErr)
	}

	retireErr := h.Blueprints.Retire(t.Context(), bref("postgres"), 1, f.fence, blueprintEntries())
	if retireErr != nil {
		t.Fatalf("Retire() = %v", retireErr)
	}

	putDraft(t, h, f, blueprint("postgres", "Second"), 1)

	if v2 := publish(t, h, f, "postgres", 2); v2.Number != 2 || v2.DraftVersion != 2 || v2.Retired {
		t.Fatalf("Publish(after a retire) = %+v, want version 2 of draft 2, not retired", v2)
	}

	if got, err := h.Blueprints.Version(t.Context(), bref("postgres"), 1); err != nil || !got.Retired {
		t.Fatalf("Version(1) = retired %v, %v, want retired", got.Retired, err)
	}
}

// race runs fn in n goroutines at the same time and returns the number of
// calls that gave no error and the errors of the others.
func race(n int, fn func() error) (int, []error) {
	results := make(chan error, n)

	var wg sync.WaitGroup

	for range n {
		wg.Go(func() { results <- fn() })
	}

	wg.Wait()
	close(results)

	winners := 0

	var others []error

	for err := range results {
		if err == nil {
			winners++
		} else {
			others = append(others, err)
		}
	}

	return winners, others
}

// wantOneWinner fails the test unless winners is 1 and each other error
// matches want.
func wantOneWinner(t *testing.T, what string, winners int, others []error, want error) {
	t.Helper()

	if winners != 1 {
		t.Fatalf("%s: %d won, want 1 (others: %v)", what, winners, others)
	}

	for _, err := range others {
		if !errors.Is(err, want) {
			t.Fatalf("%s: a call that lost = %v, want %v", what, err, want)
		}
	}
}

const racers = 12

// testPublishRace proves that concurrent publishes of one draft version give
// one winner, in each of 12 rounds, so the versions are 1 to 12 with one
// audit entry each.
func testPublishRace(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	var at int64

	for round := 1; round <= racers; round++ {
		next, err := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "T"), at, f.fence, nil)
		if err != nil {
			t.Fatalf("PutDraft(at %d) = %v", at, err)
		}

		at = next

		b, _, err := h.Blueprints.Draft(t.Context(), bref("postgres"))
		if err != nil {
			t.Fatalf("Draft() = %v", err)
		}

		b.ExprVersion = "v1.17.8"
		p := selfservice.Publication{
			At:           time.Now().UTC(),
			Ref:          bref("postgres"),
			By:           "platform",
			Blueprint:    b,
			DraftVersion: at,
		}

		winners, others := race(racers, func() error {
			_, pubErr := h.Blueprints.Publish(t.Context(), p, f.fence, blueprintEntries())

			return pubErr
		})
		wantOneWinner(t, "publish of round "+strconv.Itoa(round), winners, others, selfservice.ErrAlreadyPublished)
	}

	for n := 1; n <= racers; n++ {
		if v, err := h.Blueprints.Version(t.Context(), bref("postgres"), n); err != nil || v.DraftVersion != int64(n) {
			t.Fatalf("Version(%d) = draft %d, %v, want draft %d", n, v.DraftVersion, err, n)
		}
	}

	if _, err := h.Blueprints.Version(t.Context(), bref("postgres"), racers+1); !errors.Is(err, selfservice.ErrNotFound) {
		t.Fatalf("Version(%d) = %v, want ErrNotFound", racers+1, err)
	}

	if entries, err := h.Domain.AuditEntries(t.Context(), ns); err != nil || len(entries) != racers {
		t.Fatalf("AuditEntries() = %d entries, %v, want %d, one for each publish", len(entries), err, racers)
	}
}

// testDraftRace proves that concurrent writes of the draft at one version,
// and of a new blueprint, give one winner each.
func testDraftRace(t *testing.T, h BlueprintHarness, f fixture) {
	t.Helper()

	winners, others := race(racers, func() error {
		_, err := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "New"), 0, f.fence, blueprintEntries())

		return err
	})
	wantOneWinner(t, "write of a new blueprint", winners, others, selfservice.ErrConflict)

	winners, others = race(racers, func() error {
		_, err := h.Blueprints.PutDraft(t.Context(), blueprint("postgres", "Race"), 1, f.fence, blueprintEntries())

		return err
	})
	wantOneWinner(t, "write of the draft at one version", winners, others, selfservice.ErrConflict)

	if got, v, err := h.Blueprints.Draft(t.Context(), bref("postgres")); err != nil || v != 2 || got.Title != "Race" {
		t.Fatalf("Draft() = %q at %d, %v, want Race at 2", got.Title, v, err)
	}

	if entries, err := h.Domain.AuditEntries(t.Context(), ns); err != nil || len(entries) != 2 {
		t.Fatalf("AuditEntries() = %d entries, %v, want 2, one for each winner", len(entries), err)
	}
}
