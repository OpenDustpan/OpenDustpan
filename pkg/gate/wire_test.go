package gate_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// update rewrites the golden files.
//
//nolint:gochecknoglobals // A test flag is read once; passing it as a parameter adds nothing.
var update = flag.Bool("update", false, "write the golden files of testdata")

// golden compares got with the file name of testdata byte for byte, and
// writes the file with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path) //nolint:gosec // The path is a fixed file of testdata.
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from the JSON form:\n%s", path, got)
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	return append(b, '\n')
}

// plannedFacts returns a fact document of change.planned with a value in
// each group of the decision point.
func plannedFacts() gate.Facts {
	return gate.Facts{
		Schema:    gate.SchemaVersion{Subject: string(gate.PointChangePlanned), Major: 1, Minor: 0},
		Principal: gate.PrincipalFacts{Kind: gate.PrincipalPerson, Credential: gate.CredentialSession},
		Source: gate.SourceFacts{
			Kind: gate.SourceForge, Ref: "refs/heads/main", Protected: true, Commit: "9f2c", UpstreamUnit: "",
		},
		Unit:      gate.UnitFacts{Tool: "pulumi", Collection: "prod", Labels: []string{"team=web"}},
		Operation: gate.OperationFacts{Kind: gate.OperationApply},
		Change: gate.ChangeFacts{
			InputKeys: []string{
				"replicas",
			},
			SecretKeys:    nil,
			TruncatedKeys: nil,
			Paths:         []string{"modules/net/main.tf"},
		},
		Plan: gate.PlanFacts{
			Available: true, Add: 2, Change: 1, Replace: 0, Destroy: 1, ResourceTypes: []string{"aws:s3/bucket:Bucket"},
		},
		Cost:   gate.CostFacts{Available: true, DeltaMonthly: 15000, TotalMonthly: 90000, Currency: "USD"},
		Budget: gate.BudgetFacts{Available: true, Level: gate.BudgetWarning, PercentUsed: 82},
		Drift:  gate.DriftFacts{Available: false, Resources: 0, AgeSeconds: 0},
		ResourcePolicy: gate.ResourcePolicyFacts{
			Available: true, MandatoryFailed: 0, AdvisoryFailed: 1, Unreadable: 0,
		},
		ExternalChecks: gate.ExternalCheckFacts{
			Passed: []string{"scan"}, Failed: nil, Pending: nil, TimedOut: []string{"ticket"},
		},
		Quota:      gate.QuotaFacts{Available: true, MaxPercentUsed: 40, Exceeded: nil},
		Dependents: gate.DependentFacts{Available: true, Count: 1, Units: []string{"opentofu:acme/net"}},
		Paths: gate.PathFacts{
			Available: true, Touches: map[gate.ID]bool{"network": true}, Only: map[gate.ID]bool{"network": true},
		},
		Document: gate.DocumentFacts{Truncated: nil},
		State:    gate.StateFacts{Available: true, Resources: 9, Removed: []string{"never sent"}},
		Profile: gate.ProfileFacts{
			Name: "never sent", Access: gate.ProfileOpen, RefProtected: false, JobOpen: false,
			RoutingChange: false, ImportsAdded: nil,
		},
	}
}

func unitRef(t *testing.T) gate.UnitRef {
	t.Helper()

	obj, ok := unitScope(t).Unit()
	if !ok {
		t.Fatal("the unit scope holds no unit")
	}

	return gate.UnitRef{Object: obj}
}

func unitScope(t *testing.T) gate.Scope {
	t.Helper()

	s, err := gate.UnitScope("acme", "pulumi", "web/prod")
	if err != nil {
		t.Fatal(err)
	}

	return s
}

// fullRecord returns a record with a value in each field.
func fullRecord(t *testing.T) gate.Record {
	t.Helper()

	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	unit := unitScope(t)
	acme := gate.NamespaceScope("acme")

	return gate.Record{
		ID: "gdr-abcdefghij0123456789", Point: gate.PointChangePlanned,
		Change: gate.ChangeRef{Kind: gate.ChangeJob, ID: "job-42"}, Namespace: "acme", At: at, Subject: "alice",
		Schema:   gate.SchemaVersion{Subject: string(gate.PointChangePlanned), Major: 1, Minor: 0},
		PathSets: []gate.PathSetVersion{{Scope: acme, ID: "network", Version: 3}},
		Units: []gate.UnitResult{{
			Unit: unitRef(t), Facts: plannedFacts(), Digest: "5e1f",
			Gates: []gate.PolicyResult{{
				Policy: gate.PolicyID{Scope: unit, ID: "cost-cap"}, Version: 2, Mode: gate.ModeEnforce,
				Outcome: gate.ApprovalOutcome("finance"), Matched: true, Error: false, Diagnostic: "",
				Skipped: false, BrokenBy: "", Message: "cost rises by 15000 cents",
			}},
			Result: gate.ResultAwaitingApproval,
		}},
		Requirements: []gate.Requirement{{
			Name: gate.RuleRequirement("finance"), Version: 1,
			Approvers: gate.Approvers{Groups: []string{"finance"}, Users: []string{}, UnitOwners: false},
			Count:     2, AuthorMayApprove: false, FreshAuth: gate.FreshAuth{Set: true, Seconds: 300},
			TimeoutSeconds: 604800,
			Signatures: []gate.Signature{{
				Subject: "bob", At: at,
				CarriedFrom: &gate.CarriedFrom{Revision: 1, Digest: "77aa", At: at.Add(-time.Hour), Scope: acme},
			}},
			Evaluate: false,
		}, {
			Name: gate.ScopedDefaultRequirement(acme), Version: 4,
			Approvers: gate.Approvers{Groups: []string{}, Users: []string{"carol"}, UnitOwners: true},
			Count:     1, AuthorMayApprove: false, FreshAuth: gate.NoFreshAuth(), TimeoutSeconds: 3600,
			Signatures: []gate.Signature{}, Evaluate: false,
		}},
		Warnings:   []string{"budget at 82 percent"},
		AutoApply:  false,
		Controls:   []gate.FixedControl{{Kind: gate.ControlFreeze, Passed: true, Detail: ""}},
		BreakGlass: "", Result: gate.ResultAwaitingApproval, Count: 1, LastAt: at,
	}
}

func TestRecordGolden(t *testing.T) {
	t.Parallel()

	rec := fullRecord(t)
	b := marshal(t, rec)
	golden(t, "record.json", b)

	var back gate.Record
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}

	again := marshal(t, back)
	if !bytes.Equal(b, again) {
		t.Fatalf("a record does not survive a round trip:\n%s", again)
	}
}

func TestFactsGolden(t *testing.T) {
	t.Parallel()

	b := marshal(t, plannedFacts())
	golden(t, "facts.json", b)

	for _, absent := range []string{`"state"`, `"profile"`, `never sent`, `null`} {
		if bytes.Contains(b, []byte(absent)) {
			t.Fatalf("the facts of change.planned hold %s:\n%s", absent, b)
		}
	}

	var back gate.Facts
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}

	if !bytes.Equal(b, marshal(t, back)) {
		t.Fatal("a fact document does not survive a round trip")
	}
}

func TestFactsOfStateWrite(t *testing.T) {
	t.Parallel()

	f := plannedFacts()
	f.Schema = gate.SchemaVersion{Subject: string(gate.PointStateWrite), Major: 1, Minor: 0}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(marshal(t, f), &doc); err != nil {
		t.Fatal(err)
	}

	want := []string{"schema", "principal", "source", "unit", "operation", "plan", "document", "state"}
	if len(doc) != len(want) {
		t.Fatalf("state.write document has %d members, want %d: %v", len(doc), len(want), doc)
	}

	for _, k := range want {
		if _, ok := doc[k]; !ok {
			t.Fatalf("state.write document has no %q", k)
		}
	}
}

func TestTextForms(t *testing.T) {
	t.Parallel()

	unit := unitScope(t)
	values := []any{
		unit,
		gate.PolicyID{Scope: unit, ID: "cost-cap"},
		gate.OwnersRequirement("network"),
		gate.SchemaVersion{Subject: gate.SchemaAccess, Major: 2, Minor: 1},
		gate.RecordID("gdr-abcdefghij0123456789"),
		unitRef(t),
	}
	texts := []string{
		`"unit/acme/pulumi/web%2Fprod"`, `"unit/acme/pulumi/web%2Fprod/cost-cap"`, `"owners/network"`,
		`"access/2.1"`, `"gdr-abcdefghij0123456789"`,
		`"pulumi:acme/web/prod"`,
	}

	for i, v := range values {
		b, err := json.Marshal(v)
		if err != nil || string(b) != texts[i] {
			t.Fatalf("Marshal(%#v) = %s, %v, want %s", v, b, err, texts[i])
		}

		back := reflect.New(reflect.TypeOf(v))
		if err := json.Unmarshal(b, back.Interface()); err != nil || back.Elem().Interface() != v {
			t.Fatalf("Unmarshal(%s) = %v, %v", b, back.Elem().Interface(), err)
		}
	}
}

func TestTextFormsRefuse(t *testing.T) {
	t.Parallel()

	targets := []any{
		new(gate.Scope), new(gate.PolicyID), new(gate.RequirementName), new(gate.SchemaVersion),
		new(gate.RecordID), new(gate.FreshAuth), new(gate.UnitRef),
	}

	for _, target := range targets {
		if err := json.Unmarshal([]byte(`"web/prod%"`), target); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("Unmarshal into %T = %v, want ErrInvalid", target, err)
		}
	}

	var zero gate.UnitResult

	if _, err := json.Marshal(zero); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Marshal(UnitResult with a zero Unit) = %v, want ErrInvalid", err)
	}

	var fa gate.FreshAuth
	if err := json.Unmarshal([]byte(`30`), &fa); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("freshAuthSeconds 30 = %v, want ErrInvalid", err)
	}
}

func TestEmptyTextForms(t *testing.T) {
	t.Parallel()

	var s gate.Scope
	if err := json.Unmarshal([]byte(`""`), &s); err != nil || !s.IsZero() {
		t.Fatalf("empty scope = %v, %v", s, err)
	}

	var id gate.RecordID
	if err := json.Unmarshal([]byte(`""`), &id); err != nil || id != "" {
		t.Fatalf("empty record ID = %q, %v", id, err)
	}

	var p gate.PolicyID
	if err := json.Unmarshal([]byte(`""`), &p); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("empty policy ID = %v, want ErrInvalid", err)
	}
}

func TestMaxLengths(t *testing.T) {
	t.Parallel()

	ns := strings.Repeat("n", 100)
	tool := strings.Repeat("t", 100)
	name := strings.Repeat("é", 150)

	longest, err := gate.UnitScope(namespace.Name(ns), tool, name)
	if err != nil {
		t.Fatal(err)
	}

	if n := len(longest.Key()); n != gate.MaxScopeKeyLength {
		t.Fatalf("the longest unit scope key has %d bytes, MaxScopeKeyLength is %d", n, gate.MaxScopeKeyLength)
	}

	if _, err := gate.ParseScopeKey(longest.Key()); err != nil {
		t.Fatalf("ParseScopeKey(longest) = %v", err)
	}

	if _, err := gate.ParseScopeKey(longest.Key() + "A"); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("ParseScopeKey(longer than the most) = %v, want ErrInvalid", err)
	}

	long := strings.Repeat("a", gate.MaxPolicyIDLength+1)
	if _, err := gate.ParsePolicyID(long); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("ParsePolicyID(longer than the most) = %v, want ErrInvalid", err)
	}

	id := gate.PolicyID{Scope: longest, ID: gate.ID(strings.Repeat("a", 63))}
	if _, err := gate.ParsePolicyID(id.String()); err != nil || len(id.String()) > gate.MaxPolicyIDLength {
		t.Fatalf("ParsePolicyID(longest unit policy ID) = %v", err)
	}
}

func TestRequestValidate(t *testing.T) {
	t.Parallel()

	facts := plannedFacts()
	access := gate.AccessFacts{
		Schema:         gate.SchemaVersion{Subject: gate.SchemaAccess, Major: 1, Minor: 0},
		Principal:      gate.PrincipalFacts{Kind: gate.PrincipalAgent, Credential: gate.CredentialAccessToken},
		AuthAgeSeconds: 30,
		Unit:           gate.AccessUnitFacts{Collection: "prod", Labels: nil},
	}
	req := func(f *gate.Facts, a *gate.AccessFacts) gate.Request {
		return gate.Request{
			ID: "r1", Principal: "alice", Groups: nil, Action: string(gate.PointChangePlanned),
			Resource: identity.NamespaceObject("acme"), Facts: f, Access: a,
		}
	}

	for _, r := range []gate.Request{req(nil, nil), req(&facts, &access)} {
		if err := r.Validate(); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("Validate() = %v, want ErrInvalid", err)
		}
	}

	for _, r := range []gate.Request{req(&facts, nil), req(nil, &access)} {
		if err := r.Validate(); err != nil {
			t.Fatalf("Validate() = %v", err)
		}
	}
}

func TestReason(t *testing.T) {
	t.Parallel()

	var e audit.Entry

	e.Changes = []audit.Change{{Key: "mode", Old: nil, New: "evaluate", Layer: "", TakesEffect: ""}}

	if gate.Reason(e) != "" {
		t.Fatal("an entry with no reason gives the empty reason")
	}

	e.Changes = append(e.Changes, audit.Change{
		Key: gate.ReasonKey, Old: nil, New: "incident 7 is over", Layer: "", TakesEffect: "",
	})
	if got := gate.Reason(e); got != "incident 7 is over" {
		t.Fatalf("Reason() = %q", got)
	}
}

func TestErrNotFoundText(t *testing.T) {
	t.Parallel()

	if gate.ErrNotFound.Error() != "gate: object not found" {
		t.Fatalf("ErrNotFound = %q", gate.ErrNotFound)
	}
}
