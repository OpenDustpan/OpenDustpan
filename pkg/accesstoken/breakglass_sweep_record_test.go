package accesstoken_test

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// exemptReason is the reason of the audit record of a break-glass account
// that the inactivity sweep skips.
const exemptReason = "break-glass account is exempt"

// exemptRecords returns the audit entries of the scope @server whose reason
// is the exemption reason and whose object is object.
func exemptRecords(t *testing.T, f *fixture, object string) []audit.Entry {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.DefaultPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list @server entries: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Object == object && e.Reason == exemptReason {
			out = append(out, *e)
		}
	}

	return out
}

// accountsOnly turns the account part of the inactivity disable on and the
// token part off.
func accountsOnly() accesstoken.PolicyChange {
	return accesstoken.PolicyChange{
		MaxLifetime:       nil,
		Inactivity:        nil,
		DisableTokens:     new(false),
		DisableAccounts:   new(true),
		DisableBreakGlass: nil,
		ExchangeLifetime:  nil,
	}
}

// TestBreakGlassSweepWritesExemptRecord proves that the inactivity sweep
// keeps an inactive break-glass account enabled and writes one audit record
// of the shape of the sweep for it: kind change, outcome success, in scope
// @server, the principal reference of the account as object, the action of
// the sweep, and the reason "break-glass account is exempt".
func TestBreakGlassSweepWritesExemptRecord(t *testing.T) {
	t.Parallel()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	if _, err := f.svc.SetPolicy(ctx, caller(), accountsOnly()); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	at := sweepStart()
	bg := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-bg-sweep",
		Subject: "bg@example.com", DisplayName: "bg@example.com", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}

	if err := f.store.CreatePrincipal(ctx, bg, storetest.ServerChange(at, principal.Ref(bg.ID))); err != nil {
		t.Fatalf("create break-glass account: %v", err)
	}

	res, err := f.svc.SweepInactive(ctx, accesstoken.SweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if slices.Contains(res.Principals, bg.ID) {
		t.Fatalf("sweep = %+v, want the break-glass account %q not disabled", res, bg.ID)
	}

	p, err := f.principals.GetPrincipal(ctx, bg.ID)
	if err != nil || !p.DisabledAt.IsZero() || !p.BreakGlass {
		t.Fatalf("break-glass account = %+v, %v; want it enabled with the flag", p, err)
	}

	records := exemptRecords(t, &f, principal.Ref(bg.ID))
	if len(records) != 1 {
		t.Fatalf("exemption records for %s = %d, want 1", principal.Ref(bg.ID), len(records))
	}

	checkExemptRecordShape(t, records[0])
}

// checkExemptRecordShape fails when e is not one change of outcome success
// in the scope @server with the action of the sweep.
func checkExemptRecordShape(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Namespace != audit.ServerScope || e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("exemption record = %+v, want a change of outcome success in @server", e)
	}

	if e.Action != string(identity.ActionServerManageTokenPolicy) {
		t.Fatalf("exemption record action = %q, want %q", e.Action, identity.ActionServerManageTokenPolicy)
	}
}

// TestBreakGlassSweepWritesNoExemptRecordWithoutSkip proves that a sweep
// that skips no break-glass account writes no exemption record.
func TestBreakGlassSweepWritesNoExemptRecordWithoutSkip(t *testing.T) {
	t.Parallel()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	if _, err := f.svc.SetPolicy(ctx, caller(), accountsOnly()); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	if _, err := f.svc.SweepInactive(ctx, accesstoken.SweepEntry()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if records := exemptRecords(t, &f, principal.Ref(f.person.ID)); len(records) != 0 {
		t.Fatalf("exemption records = %d, want none without a skipped account", len(records))
	}
}

// TestSweepWithNoBreakGlassAccountWritesNoExemptRecord proves that a sweep
// with no break-glass account in the store disables the inactive ordinary
// account and writes no audit record with the exemption reason.
func TestSweepWithNoBreakGlassAccountWritesNoExemptRecord(t *testing.T) {
	t.Parallel()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	if _, err := f.svc.SetPolicy(ctx, caller(), accountsOnly()); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	res, err := f.svc.SweepInactive(ctx, accesstoken.SweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// The sweep must run. Without this check the case could pass on an empty run.
	if !slices.Contains(res.Principals, f.person.ID) {
		t.Fatalf("sweep = %+v, want the inactive account %q disabled", res, f.person.ID)
	}

	listing, err := f.store.List(ctx, audit.ServerScope, audit.Page{Size: audit.DefaultPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list @server entries: %v", err)
	}

	exempt := 0

	for i := range listing.Entries {
		if listing.Entries[i].Reason == exemptReason {
			exempt++
		}
	}

	if exempt != 0 {
		t.Fatalf("exemption records = %d, want none without a break-glass account", exempt)
	}
}
