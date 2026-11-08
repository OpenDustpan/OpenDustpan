package selfservicesvc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservicesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const draftJSON = `{
	"schemaVersion": 1, "name": "a", "title": "Database", "tool": "opentofu",
	"program": {"module": {"source": "registry.example.com/platform/database/aws", "version": "1.2.3"}},
	"unitName": "'databases/' + request.name",
	"inputs": [
		{"name": "size", "type": "integer", "maps": "size", "validate": [
			{"rule": "value >= 1", "message": "Give at least 1."},
			{"rule": "value <= 10", "message": "Give at most 10."}]},
		{"name": "label", "type": "string", "maps": "label", "validate": [
			{"rule": "value != ''", "message": "Give a label."}]}
	],
	"policy": [{"when": "inputs.size > 5", "effect": "deny"},
		{"when": "inputs.size == 5", "effect": "approve", "approvals": 1}],
	"quotas": [{"kind": "units", "scope": "namespace", "max": 20},
		{"kind": "expr", "rule": "usage['units'] < 20", "message": "The limit is 20."},
		{"kind": "expr", "rule": "usage['units'] >= 0", "message": "The count must not be negative."}],
	"delivery": {"homes": ["opendustpan"], "default": "opendustpan"}
}`

var (
	errAuthorizer = errors.New("authorizer unavailable")
	errEvaluator  = errors.New("evaluator unavailable")
	errStore      = errors.New("blueprint store unavailable")
	errAudit      = errors.New("audit store unavailable")
)

type compilation struct {
	source string
	kind   selfservice.ResultKind
}

type evaluator struct {
	failSource string
	err        error
	calls      []compilation
}

func (*evaluator) Version() string { return "v1.17.8" }

func (e *evaluator) Compile(source string, kind selfservice.ResultKind) error {
	e.calls = append(e.calls, compilation{source: source, kind: kind})
	if e.err != nil && (e.failSource == "" || e.failSource == source) {
		return fmt.Errorf("compile refused: %w", e.err)
	}

	return nil
}

func (*evaluator) Bool(context.Context, string, *selfservice.Env) (bool, error) {
	return false, fmt.Errorf("unexpected evaluation: %w", selfservice.ErrInvalid)
}

func (*evaluator) String(context.Context, string, *selfservice.Env) (string, error) {
	return "", fmt.Errorf("unexpected evaluation: %w", selfservice.ErrInvalid)
}

type authorization struct {
	principal identity.Principal
	action    identity.Action
	object    selfservice.Object
}

type authorizer struct {
	err       error
	onlyRead  bool
	blueprint string
	calls     []authorization
}

func (a *authorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, object selfservice.Object,
) error {
	a.calls = append(a.calls, authorization{principal: p, action: action, object: object})
	if a.err != nil {
		return a.err
	}

	if (a.onlyRead && action != selfservice.ActionBlueprintRead) ||
		(a.blueprint != "" && object.Blueprint != a.blueprint) {
		return fmt.Errorf("blueprint access: %w", identity.ErrDenied)
	}

	return nil
}

type clock struct {
	at time.Time
}

func (c *clock) Now() time.Time { return c.at }

type fences struct {
	value namespace.Fence
	err   error
}

func (f *fences) Fence(context.Context, namespace.Name) (namespace.Fence, error) {
	return f.value, f.err
}

type auditor struct {
	entries []audit.Entry
	fences  []namespace.Fence
	err     error
}

func (a *auditor) Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := ctx.Err(); err != nil {
		return audit.Entry{}, fmt.Errorf("test audit context: %w", err)
	}

	a.entries = append(a.entries, e)
	a.fences = append(a.fences, fence)
	if a.err != nil {
		return audit.Entry{}, a.err
	}

	if err := e.Validate(); err != nil {
		return audit.Entry{}, fmt.Errorf("validate test audit entry: %w", err)
	}

	return e, nil
}

type store struct {
	draft        selfservice.Blueprint
	draftVersion int64
	published    selfservice.Version
	fail         string
	calls        []string
	writes       int
	publication  selfservice.Publication
	fence        namespace.Fence
	entries      []audit.Entry
	ref          selfservice.BlueprintRef
	number       int
}

func (s *store) Draft(_ context.Context, ref selfservice.BlueprintRef) (selfservice.Blueprint, int64, error) {
	s.ref = ref
	if err := s.called("Draft"); err != nil {
		return selfservice.Blueprint{}, 0, err
	}

	return s.draft, s.draftVersion, nil
}

func (s *store) PutDraft(
	_ context.Context, b selfservice.Blueprint, version int64, fence namespace.Fence, entries []audit.Entry,
) (int64, error) {
	if err := s.called("PutDraft"); err != nil {
		return 0, err
	}

	s.writes++
	s.draft, s.draftVersion = b, version+1
	s.fence, s.entries = fence, entries

	return s.draftVersion, nil
}

func (s *store) Publish(
	_ context.Context, p selfservice.Publication, fence namespace.Fence, entries []audit.Entry,
) (selfservice.Version, error) {
	if err := s.called("Publish"); err != nil {
		return selfservice.Version{}, err
	}

	s.writes++
	s.publication, s.fence, s.entries = p, fence, entries
	s.published = selfservice.Version{
		PublishedAt: p.At, Blueprint: p.Blueprint, Namespace: p.Ref.Namespace, PublishedBy: p.By,
		Number: 2, DraftVersion: p.DraftVersion, Retired: false,
	}

	return s.published, nil
}

func (s *store) ListBlueprints(_ context.Context, ns namespace.Name) ([]selfservice.Version, error) {
	s.ref = selfservice.BlueprintRef{Namespace: ns, Name: ""}
	if err := s.called("ListBlueprints"); err != nil {
		return nil, err
	}

	return []selfservice.Version{s.published}, nil
}

func (s *store) Version(
	_ context.Context, ref selfservice.BlueprintRef, n int,
) (selfservice.Version, error) {
	s.ref, s.number = ref, n
	if err := s.called("Version"); err != nil {
		return selfservice.Version{}, err
	}

	return s.published, nil
}

func (s *store) Retire(
	_ context.Context, ref selfservice.BlueprintRef, n int, fence namespace.Fence, entries []audit.Entry,
) error {
	if err := s.called("Retire"); err != nil {
		return err
	}

	s.writes++
	s.ref, s.number, s.fence, s.entries = ref, n, fence, entries
	s.published.Retired = true

	return nil
}

func (*store) SetExprVersion(
	context.Context, selfservice.ExprVersionChange, namespace.Fence, []audit.Entry,
) error {
	return fmt.Errorf("unexpected expression version write: %w", selfservice.ErrInvalid)
}

func (s *store) called(method string) error {
	s.calls = append(s.calls, method)
	if s.fail == method {
		return errStore
	}

	return nil
}

type fixture struct {
	service    *selfservicesvc.Blueprints
	store      *store
	authorizer *authorizer
	evaluator  *evaluator
	clock      *clock
	audit      *auditor
	fences     *fences
	principal  identity.Principal
	ref        selfservice.BlueprintRef
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	var f fixture
	f.store = new(store)
	f.authorizer = new(authorizer)
	f.evaluator = new(evaluator)
	f.audit = new(auditor)
	f.fences = new(fences)
	f.clock = &clock{at: time.Unix(1_790_000_000, 0).UTC()}
	f.ref = selfservice.BlueprintRef{Namespace: "payments", Name: "a"}
	f.principal.Subject, f.principal.TokenID, f.principal.Kind = "platform", "token-1", identity.KindPerson
	f.fences.value = namespace.Fence{Namespace: f.ref.Namespace, FailoverVersion: 7}
	if err := json.Unmarshal([]byte(draftJSON), &f.store.draft); err != nil {
		t.Fatal(err)
	}
	f.store.draftVersion = 3
	f.store.published = selfservice.Version{
		PublishedAt: f.clock.at, Blueprint: f.store.draft, Namespace: f.ref.Namespace, PublishedBy: "platform",
		Number: 1, DraftVersion: 2, Retired: false,
	}
	var err error
	f.service, err = selfservicesvc.New(f.config())
	if err != nil {
		t.Fatal(err)
	}

	return &f
}

func (f *fixture) config() selfservicesvc.Config {
	return selfservicesvc.Config{
		Store: f.store, Authorizer: f.authorizer, Evaluator: f.evaluator,
		Clock: f.clock, Audit: f.audit, Fences: f.fences,
	}
}

func (f *fixture) run(ctx context.Context, op selfservice.Op) error {
	switch op {
	case selfservice.OpListBlueprints:
		_, err := f.service.ListBlueprints(ctx, f.principal, f.ref.Namespace)
		return err
	case selfservice.OpReadBlueprint:
		_, _, err := f.service.ReadDraft(ctx, f.principal, f.ref)
		return err
	case selfservice.OpWriteDraft:
		_, err := f.service.WriteDraft(ctx, f.principal, f.ref, f.store.draft, f.store.draftVersion)
		return err
	case selfservice.OpPublish:
		_, err := f.service.Publish(ctx, f.principal, f.ref, f.store.draftVersion)
		return err
	case selfservice.OpRetire:
		return f.service.Retire(ctx, f.principal, f.ref, 1)
	case selfservice.OpOptions, selfservice.OpCheck, selfservice.OpSubmit, selfservice.OpListRequests,
		selfservice.OpReadRequest, selfservice.OpCancel, selfservice.OpRender, selfservice.OpManagePolicy:
		return fmt.Errorf("unsupported test operation: %w", selfservice.ErrInvalid)
	default:
		return fmt.Errorf("unexpected test operation: %w", selfservice.ErrInvalid)
	}
}

func checkEntry(t *testing.T, f *fixture, entry audit.Entry, outcome audit.Outcome) {
	t.Helper()

	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	if entry.Outcome != outcome || entry.Subject != f.principal.Subject || entry.TokenID != f.principal.TokenID ||
		entry.PrincipalKind != string(f.principal.Kind) || entry.Namespace != f.ref.Namespace ||
		!entry.At.Equal(f.clock.at) || entry.ValueCount != nil || len(entry.Changes) != 0 {
		t.Fatalf("audit metadata = %+v", entry)
	}
}

func checkFailure(t *testing.T, f *fixture, err, want error, outcome audit.Outcome) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if f.store.writes != 0 {
		t.Fatalf("stored %d changes after a failure", f.store.writes)
	}
	if len(f.audit.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(f.audit.entries))
	}
	checkEntry(t, f, f.audit.entries[0], outcome)
	if f.audit.fences[0] != f.fences.value {
		t.Fatalf("audit fence = %+v", f.audit.fences[0])
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		remove func(*selfservicesvc.Config)
	}{
		{"store", func(c *selfservicesvc.Config) { c.Store = nil }},
		{"typed store", func(c *selfservicesvc.Config) { c.Store = (*store)(nil) }},
		{"authorizer", func(c *selfservicesvc.Config) { c.Authorizer = nil }},
		{"typed authorizer", func(c *selfservicesvc.Config) { c.Authorizer = (*authorizer)(nil) }},
		{"evaluator", func(c *selfservicesvc.Config) { c.Evaluator = nil }},
		{"typed evaluator", func(c *selfservicesvc.Config) { c.Evaluator = (*evaluator)(nil) }},
		{"clock", func(c *selfservicesvc.Config) { c.Clock = nil }},
		{"typed clock", func(c *selfservicesvc.Config) { c.Clock = (*clock)(nil) }},
		{"audit", func(c *selfservicesvc.Config) { c.Audit = nil }},
		{"typed audit", func(c *selfservicesvc.Config) { c.Audit = (*auditor)(nil) }},
		{"fences", func(c *selfservicesvc.Config) { c.Fences = nil }},
		{"typed fences", func(c *selfservicesvc.Config) { c.Fences = (*fences)(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			cfg := f.config()
			tc.remove(&cfg)
			got, err := selfservicesvc.New(cfg)
			if got != nil || !errors.Is(err, selfservicesvc.ErrMissingDependency) {
				t.Fatalf("New() = %v, %v", got, err)
			}
		})
	}
}

func TestParseDraft(t *testing.T) {
	t.Parallel()

	exact := append([]byte(draftJSON), bytes.Repeat([]byte(" "), selfservice.MaxBlueprintBytes-len(draftJSON))...)
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"valid", []byte(draftJSON), nil},
		{"exact limit", exact, nil},
		{"oversized", append(bytes.Clone(exact), ' '), selfservice.ErrLimit},
		{"unknown field", []byte(strings.Replace(draftJSON, `"title":`, `"unknown":`, 1)), selfservice.ErrInvalid},
		{"nested unknown field", []byte(strings.Replace(draftJSON, `"maps":`, `"unknown":`, 1)), selfservice.ErrInvalid},
		{
			"duplicate key", []byte(strings.Replace(draftJSON, `"name": "a"`, `"name": "a", "name": "a"`, 1)),
			selfservice.ErrInvalid,
		},
		{
			"nested duplicate", []byte(strings.Replace(draftJSON, `"maps": "size"`, `"maps": "size", "maps": "size"`, 1)),
			selfservice.ErrInvalid,
		},
		{
			"invalid key", []byte(strings.Replace(draftJSON, `"maps": "size"`, `"maps": "source"`, 1)),
			selfservice.ErrInvalid,
		},
		{"trailing document", []byte(draftJSON + "{}"), selfservice.ErrInvalid},
		{"malformed", []byte("{"), selfservice.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			b, err := selfservicesvc.ParseDraft(tc.data)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseDraft() = %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				var zero selfservice.Blueprint
				if !reflect.DeepEqual(b, zero) || len(f.store.calls) != 0 || f.store.writes != 0 {
					t.Fatal("a refused document returned data or reached the store")
				}
				return
			}
			if !reflect.DeepEqual(b, f.store.draft) {
				t.Fatal("parsed blueprint differs from the document")
			}
		})
	}
}

func TestReadResults(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		read func(*testing.T, *fixture)
		kind selfservice.ObjectKind
	}{
		{"list", checkListResult, selfservice.ObjectNamespace},
		{"draft", checkDraftResult, selfservice.ObjectBlueprint},
		{"version", checkVersionResult, selfservice.ObjectBlueprint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.authorizer.onlyRead = true
			tc.read(t, f)
			checkReadAuthorization(t, f, tc.kind)
			if len(f.audit.entries) != 0 || f.store.writes != 0 {
				t.Fatal("successful read stored an entry or a change")
			}
		})
	}
}

func checkListResult(t *testing.T, f *fixture) {
	t.Helper()
	got, err := f.service.ListBlueprints(t.Context(), f.principal, f.ref.Namespace)
	if err != nil || !reflect.DeepEqual(got, []selfservice.Version{f.store.published}) {
		t.Fatalf("ListBlueprints() = %v, %v", got, err)
	}
}

func checkDraftResult(t *testing.T, f *fixture) {
	t.Helper()
	got, version, err := f.service.ReadDraft(t.Context(), f.principal, f.ref)
	if err != nil || version != 3 || !reflect.DeepEqual(got, f.store.draft) {
		t.Fatalf("ReadDraft() = %v, %d, %v", got, version, err)
	}
}

func checkVersionResult(t *testing.T, f *fixture) {
	t.Helper()
	got, err := f.service.ReadVersion(t.Context(), f.principal, f.ref, 1)
	if err != nil || !reflect.DeepEqual(got, f.store.published) || f.store.number != 1 {
		t.Fatalf("ReadVersion() = %v, %v", got, err)
	}
}

func checkReadAuthorization(t *testing.T, f *fixture, kind selfservice.ObjectKind) {
	t.Helper()
	calls := f.authorizer.calls
	if len(calls) != 1 || calls[0].action != selfservice.ActionBlueprintRead ||
		calls[0].principal != f.principal || calls[0].object.Namespace != f.ref.Namespace ||
		calls[0].object.Kind != kind {
		t.Fatalf("read authorization = %+v", calls)
	}
	if kind == selfservice.ObjectNamespace {
		if f.store.ref.Namespace != f.ref.Namespace {
			t.Fatal("list did not use the namespace")
		}
	} else if calls[0].object.Blueprint != f.ref.Name || f.store.ref != f.ref {
		t.Fatal("read did not use the blueprint")
	}
}

func TestWriteDraft(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	before := f.store.draft
	got, err := f.service.WriteDraft(t.Context(), f.principal, f.ref, before, 3)
	if err != nil || got != 4 || f.store.draftVersion != 4 || !reflect.DeepEqual(before, f.store.draft) {
		t.Fatalf("WriteDraft() = %d, %v", got, err)
	}
	checkWrite(t, f, selfservice.ActionBlueprintPublish)
}

func TestRetire(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := f.service.Retire(t.Context(), f.principal, f.ref, 1); err != nil {
		t.Fatal(err)
	}
	checkWrite(t, f, selfservice.ActionBlueprintRetire)
	if f.store.number != 1 || f.store.ref != f.ref || !f.store.published.Retired ||
		f.store.entries[0].Version != "1" {
		t.Fatal("Retire did not use the requested version")
	}
}

func checkWrite(t *testing.T, f *fixture, action identity.Action) {
	t.Helper()
	if f.store.writes != 1 || f.store.fence != f.fences.value || len(f.store.entries) != 1 {
		t.Fatal("write did not carry its fence and audit entry")
	}
	if len(f.evaluator.calls) != 0 || len(f.audit.entries) != 0 {
		t.Fatal("draft write or retire compiled expressions or appended an external success entry")
	}
	checkEntry(t, f, f.store.entries[0], audit.OutcomeSuccess)
	if f.store.entries[0].Action != string(action) || f.store.entries[0].Object != f.ref.String() ||
		len(f.authorizer.calls) != 1 || f.authorizer.calls[0].action != action ||
		f.authorizer.calls[0].object.Blueprint != "a" {
		t.Fatal("write used the wrong action or blueprint")
	}
}

func expectedCompilations() []compilation {
	return []compilation{
		{source: "'databases/' + request.name", kind: selfservice.ResultString},
		{source: "value >= 1", kind: selfservice.ResultBool},
		{source: "value <= 10", kind: selfservice.ResultBool},
		{source: "value != ''", kind: selfservice.ResultBool},
		{source: "inputs.size > 5", kind: selfservice.ResultBool},
		{source: "inputs.size == 5", kind: selfservice.ResultBool},
		{source: "usage['units'] < 20", kind: selfservice.ResultBool},
		{source: "usage['units'] >= 0", kind: selfservice.ResultBool},
	}
}

func TestPublish(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.authorizer.blueprint = "a"
	original := f.store.draft
	got, err := f.service.Publish(t.Context(), f.principal, f.ref, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := original
	want.ExprVersion = selfservice.ExprVersionName(f.evaluator.Version())
	checkPublication(t, f, &want)
	if !reflect.DeepEqual(f.store.draft, original) || !reflect.DeepEqual(got, f.store.published) ||
		!reflect.DeepEqual(f.evaluator.calls, expectedCompilations()) {
		t.Fatal("publication differs from the checked stored draft")
	}
	if !reflect.DeepEqual(f.store.calls, []string{"Draft", "Publish"}) ||
		f.store.fence != f.fences.value || len(f.store.entries) != 1 {
		t.Fatal("publish did not read before writing with a fence and audit entry")
	}
	checkEntry(t, f, f.store.entries[0], audit.OutcomeSuccess)
	if f.store.entries[0].Action != string(selfservice.ActionBlueprintPublish) ||
		f.store.entries[0].Object != f.ref.String() {
		t.Fatal("publish audit action or object differs")
	}
}

func checkPublication(t *testing.T, f *fixture, want *selfservice.Blueprint) {
	t.Helper()
	p := f.store.publication
	if !reflect.DeepEqual(p.Blueprint, *want) ||
		p.Ref != f.ref || p.DraftVersion != 3 || p.By != f.principal.Subject || !p.At.Equal(f.clock.at) ||
		f.store.writes != 1 {
		t.Fatal("publication differs from the checked stored draft")
	}
}

func TestPublishCompileFailures(t *testing.T) {
	t.Parallel()

	for _, call := range expectedCompilations() {
		for _, cause := range []error{selfservice.ErrInvalid, selfservice.ErrLimit, errEvaluator} {
			t.Run(call.source+"/"+cause.Error(), func(t *testing.T) {
				t.Parallel()
				f := newFixture(t)
				f.evaluator.failSource, f.evaluator.err = call.source, cause
				_, err := f.service.Publish(t.Context(), f.principal, f.ref, 3)
				checkFailure(t, f, err, cause, audit.OutcomeFailure)
				if !reflect.DeepEqual(f.store.calls, []string{"Draft"}) {
					t.Fatalf("store calls after compile failure = %v", f.store.calls)
				}
			})
		}
	}
}

func TestPublishDraftConflict(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.draft.Title = ""
	_, err := f.service.Publish(t.Context(), f.principal, f.ref, 2)
	checkFailure(t, f, err, selfservice.ErrConflict, audit.OutcomeFailure)
	if len(f.evaluator.calls) != 0 || !reflect.DeepEqual(f.store.calls, []string{"Draft"}) {
		t.Fatal("a stale draft reached compilation or publication")
	}
}

func TestInvalidDrafts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		change func(*selfservice.Blueprint)
		want   error
	}{
		{"missing title", func(b *selfservice.Blueprint) { b.Title = "" }, selfservice.ErrInvalid},
		{"invalid key", func(b *selfservice.Blueprint) { b.Inputs[0].Maps = "source" }, selfservice.ErrInvalid},
		{"two homes", func(b *selfservice.Blueprint) { b.Inputs[1].Maps = "size" }, selfservice.ErrOneHome},
		{"different name", func(b *selfservice.Blueprint) { b.Name = "b" }, selfservice.ErrInvalid},
		{"too many inputs", func(b *selfservice.Blueprint) {
			b.Inputs = make([]selfservice.Input, selfservice.MaxInputs+1)
		}, selfservice.ErrLimit},
	} {
		for _, op := range []selfservice.Op{selfservice.OpWriteDraft, selfservice.OpPublish} {
			t.Run(tc.name+"/"+string(op), func(t *testing.T) {
				t.Parallel()
				f := newFixture(t)
				tc.change(&f.store.draft)
				err := f.run(t.Context(), op)
				checkFailure(t, f, err, tc.want, audit.OutcomeFailure)
				if len(f.evaluator.calls) != 0 {
					t.Fatal("invalid stored draft reached compilation")
				}
				checkNoWriteCall(t, f.store.calls)
			})
		}
	}
}

func checkNoWriteCall(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		if call != "Draft" {
			t.Fatalf("invalid draft reached %s", call)
		}
	}
}

func TestAuthorization(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		op        selfservice.Op
		cause     error
		onlyRead  bool
		blueprint string
		target    string
		want      error
	}{
		{"read cannot write", selfservice.OpWriteDraft, nil, true, "", "a", identity.ErrDenied},
		{"read cannot publish", selfservice.OpPublish, nil, true, "", "a", identity.ErrDenied},
		{"read cannot retire", selfservice.OpRetire, nil, true, "", "a", identity.ErrDenied},
		{"grant on another blueprint", selfservice.OpPublish, nil, false, "a", "b", identity.ErrDenied},
		{"hidden draft", selfservice.OpReadBlueprint, identity.ErrDenied, false, "", "a", selfservice.ErrNotFound},
		{"hidden list", selfservice.OpListBlueprints, identity.ErrDenied, false, "", "a", selfservice.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.authorizer.err, f.authorizer.onlyRead, f.authorizer.blueprint = tc.cause, tc.onlyRead, tc.blueprint
			f.ref.Name = tc.target
			err := f.run(t.Context(), tc.op)
			checkFailure(t, f, err, tc.want, audit.OutcomeDenied)
			if len(f.store.calls) != 0 || len(f.evaluator.calls) != 0 {
				t.Fatal("a denied operation reached the blueprint store or evaluator")
			}
			if errors.Is(tc.want, selfservice.ErrNotFound) && errors.Is(err, identity.ErrDenied) {
				t.Fatal("hidden read disclosed its denial")
			}
		})
	}
}

func TestReadVersionDenied(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.authorizer.err = identity.ErrDenied
	got, err := f.service.ReadVersion(t.Context(), f.principal, f.ref, 1)
	checkFailure(t, f, err, selfservice.ErrNotFound, audit.OutcomeDenied)
	var zero selfservice.Version
	if len(f.store.calls) != 0 || !reflect.DeepEqual(got, zero) || errors.Is(err, identity.ErrDenied) {
		t.Fatal("denied version read reached the store or disclosed data")
	}
}

func TestAuthorizerFailsClosed(t *testing.T) {
	t.Parallel()
	for _, op := range []selfservice.Op{
		selfservice.OpListBlueprints, selfservice.OpReadBlueprint, selfservice.OpWriteDraft,
		selfservice.OpPublish, selfservice.OpRetire,
	} {
		t.Run(string(op), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.authorizer.err = errAuthorizer
			err := f.run(t.Context(), op)
			checkFailure(t, f, err, errAuthorizer, audit.OutcomeFailure)
			if errors.Is(err, selfservice.ErrNotFound) || len(f.store.calls) != 0 || len(f.evaluator.calls) != 0 {
				t.Fatal("authorizer failure was hidden or reached another dependency")
			}
		})
	}
	t.Run("read version", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.authorizer.err = errAuthorizer
		_, err := f.service.ReadVersion(t.Context(), f.principal, f.ref, 1)
		checkFailure(t, f, err, errAuthorizer, audit.OutcomeFailure)
		if len(f.store.calls) != 0 {
			t.Fatal("authorizer failure reached the store")
		}
	})
}

func TestStoreFailsClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   selfservice.Op
		fail string
	}{
		{selfservice.OpListBlueprints, "ListBlueprints"},
		{selfservice.OpReadBlueprint, "Draft"},
		{selfservice.OpWriteDraft, "PutDraft"},
		{selfservice.OpPublish, "Draft"},
		{selfservice.OpPublish, "Publish"},
		{selfservice.OpRetire, "Retire"},
	} {
		t.Run(string(tc.op)+"/"+tc.fail, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.store.fail = tc.fail
			err := f.run(t.Context(), tc.op)
			checkFailure(t, f, err, errStore, audit.OutcomeFailure)
			if tc.fail == "Draft" && len(f.evaluator.calls) != 0 {
				t.Fatal("failed store read reached compilation")
			}
		})
	}
	t.Run("read version", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.store.fail = "Version"
		_, err := f.service.ReadVersion(t.Context(), f.principal, f.ref, 1)
		checkFailure(t, f, err, errStore, audit.OutcomeFailure)
	})
}

func TestPublishSourcePrincipal(t *testing.T) {
	t.Parallel()
	for _, denied := range []bool{false, true} {
		t.Run(fmt.Sprintf("denied=%t", denied), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.store.draft.SourcePrincipal = "reader"
			f.store.draft.Inputs[1].Choice = &selfservice.Choice{
				Values: nil, Source: &selfservice.Source{
					Label: nil, Value: selfservice.OutputRef{Output: "name", Path: ""},
					Blueprint: "network", Collection: "", Where: nil,
				},
			}
			if denied {
				f.authorizer.blueprint = "a"
			}
			err := f.run(t.Context(), selfservice.OpPublish)
			if denied {
				checkFailure(t, f, err, identity.ErrDenied, audit.OutcomeDenied)
			} else if err != nil {
				t.Fatal(err)
			}
			if denied && len(f.evaluator.calls) != 0 {
				t.Fatal("source principal denial reached compilation")
			}
			checkSourceAuthorization(t, f)
		})
	}
}

func checkSourceAuthorization(t *testing.T, f *fixture) {
	t.Helper()
	calls := f.authorizer.calls
	if len(calls) != 2 || calls[1].action != identity.ActionPrincipalManageTokens ||
		calls[1].object.Kind != selfservice.ObjectPrincipal || calls[1].object.Unit.PrincipalID() != "reader" {
		t.Fatalf("source principal authorization = %+v", calls)
	}
}

func TestAuditFailureRetainsError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.authorizer.err, f.audit.err = errAuthorizer, errAudit
	err := f.run(t.Context(), selfservice.OpPublish)
	checkFailure(t, f, err, errAuthorizer, audit.OutcomeFailure)
	if !errors.Is(err, errAudit) || len(f.store.calls) != 0 {
		t.Fatal("audit failure replaced the cause or allowed a write")
	}
}

func TestCanceledRequestStillAudits(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.authorizer.err = errAuthorizer
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := f.run(ctx, selfservice.OpPublish)
	checkFailure(t, f, err, errAuthorizer, audit.OutcomeFailure)
}

func TestFenceFailsClosed(t *testing.T) {
	t.Parallel()
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprintf("mismatch=%t", mismatch), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			want := errStore
			f.fences.err = errStore
			if mismatch {
				f.fences.err = nil
				f.fences.value.Namespace = "other"
				want = namespace.ErrFenced
			}
			err := f.run(t.Context(), selfservice.OpWriteDraft)
			if !errors.Is(err, want) || len(f.store.calls) != 0 || f.store.writes != 0 {
				t.Fatalf("fence failure = %v, store calls = %v", err, f.store.calls)
			}
		})
	}
}

func TestInvalidReferenceFailsClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ref  selfservice.BlueprintRef
	}{
		{"namespace", selfservice.BlueprintRef{Namespace: "", Name: "a"}},
		{"blueprint", selfservice.BlueprintRef{Namespace: "payments", Name: "../b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.ref = tc.ref
			err := f.run(t.Context(), selfservice.OpWriteDraft)
			if !errors.Is(err, selfservice.ErrInvalid) || len(f.store.calls) != 0 || len(f.authorizer.calls) != 0 {
				t.Fatalf("invalid reference = %v, store calls = %v", err, f.store.calls)
			}
		})
	}
}
