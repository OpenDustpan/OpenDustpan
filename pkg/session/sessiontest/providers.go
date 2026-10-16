package sessiontest

import (
	"bytes"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// providerEntry returns a valid audit entry of a change of the sign-in
// provider id at the time when.
func providerEntry(id string, when time.Time) audit.Entry {
	e := entry("ses-x", when)
	e.Action = "server:manage-sign-in"
	e.Object = "sign-in-provider:" + id

	return e
}

// newProviderConfig returns a valid provider id, made and changed at
// minute 5.
func newProviderConfig(id string) session.ProviderConfig {
	return session.ProviderConfig{
		CreatedAt:    at(5),
		UpdatedAt:    at(5),
		ID:           id,
		Issuer:       "https://" + id + ".idp.example",
		DisplayName:  "Provider " + id,
		ClientID:     "dustpan-" + id,
		SealedSecret: []byte("sealed secret of " + id),
		Scopes:       []string{"profile", "email"},
		Version:      1,
		AutoCreate:   true,
		Enabled:      true,
	}
}

func equalProviderConfig(t *testing.T, got, want session.ProviderConfig) {
	t.Helper()

	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) || got.ID != want.ID ||
		got.Issuer != want.Issuer || got.DisplayName != want.DisplayName || got.ClientID != want.ClientID ||
		!bytes.Equal(got.SealedSecret, want.SealedSecret) || !slices.Equal(got.Scopes, want.Scopes) ||
		got.Version != want.Version || got.AutoCreate != want.AutoCreate || got.Enabled != want.Enabled {
		t.Fatalf("provider = %+v, want %+v", got, want)
	}
}

func mustCreateProvider(t *testing.T, s Store, p session.ProviderConfig) {
	t.Helper()

	if err := s.CreateProviderConfig(t.Context(), p, providerEntry(p.ID, p.CreatedAt)); err != nil {
		t.Fatalf("create provider %s: %v", p.ID, err)
	}
}

func testProviderRoundTrip(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	list, err := s.ListProviderConfigs(ctx)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("list with no provider = %#v, %v; want an empty list", list, err)
	}

	corp, beta := newProviderConfig("corp"), newProviderConfig("beta")
	corp.Scopes, corp.AutoCreate = nil, false

	mustCreateProvider(t, s, corp)
	mustCreateProvider(t, s, beta)

	got, err := s.GetProviderConfig(ctx, "corp")
	if err != nil {
		t.Fatalf("get provider: %v", err)
	}

	corp.Scopes = []string{}
	equalProviderConfig(t, got, corp)

	list, err = s.ListProviderConfigs(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "beta" || list[1].ID != "corp" {
		t.Fatalf("list = %+v, %v; want beta and corp in that order", list, err)
	}

	equalProviderConfig(t, list[0], beta)

	if err := s.CreateProviderConfig(ctx, beta, providerEntry("beta", at(6))); !errors.Is(
		err, session.ErrProviderExists,
	) {
		t.Fatalf("second create = %v, want ErrProviderExists", err)
	}

	if _, err := s.GetProviderConfig(ctx, "none"); !errors.Is(err, session.ErrProviderNotFound) {
		t.Fatalf("get of an unknown provider = %v, want ErrProviderNotFound", err)
	}
}

func testProviderRefusesInvalid(t *testing.T, s Store) {
	t.Helper()

	bad := newProviderConfig("corp")
	bad.SealedSecret = nil

	if err := s.CreateProviderConfig(t.Context(), bad, providerEntry("corp", at(5))); !errors.Is(
		err, session.ErrInvalidProvider,
	) {
		t.Fatalf("create with no secret = %v, want ErrInvalidProvider", err)
	}

	if _, err := s.GetProviderConfig(t.Context(), "corp"); !errors.Is(err, session.ErrProviderNotFound) {
		t.Fatalf("get after a refused create = %v, want ErrProviderNotFound", err)
	}
}

// testProviderUpdate proves the version check of an update: the next
// version replaces the provider and keeps its creation time, and another
// version changes nothing.
func testProviderUpdate(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	corp := newProviderConfig("corp")
	mustCreateProvider(t, s, corp)

	changed := corp
	changed.CreatedAt = at(1)
	changed.UpdatedAt = at(7)
	changed.DisplayName = "Corp SSO"
	changed.SealedSecret = []byte("sealed new secret")
	changed.Scopes = []string{"groups"}
	changed.Enabled = false
	changed.Version = 2

	if err := s.UpdateProviderConfig(ctx, changed, providerEntry("corp", at(7))); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetProviderConfig(ctx, "corp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	want := changed
	want.CreatedAt = corp.CreatedAt
	equalProviderConfig(t, got, want)

	stale := changed
	stale.DisplayName = "Stale"

	staleErr := s.UpdateProviderConfig(ctx, stale, providerEntry("corp", at(8)))
	if !errors.Is(staleErr, session.ErrProviderChanged) {
		t.Fatalf("update of a read version = %v, want ErrProviderChanged", staleErr)
	}

	kept, err := s.GetProviderConfig(ctx, "corp")
	if err != nil || kept.DisplayName != "Corp SSO" {
		t.Fatalf("display name after a refused update = %q, %v; want Corp SSO", kept.DisplayName, err)
	}

	missing := newProviderConfig("none")
	missing.Version = 2

	if err := s.UpdateProviderConfig(ctx, missing, providerEntry("none", at(8))); !errors.Is(
		err, session.ErrProviderNotFound,
	) {
		t.Fatalf("update of an unknown provider = %v, want ErrProviderNotFound", err)
	}
}

// testProviderDelete proves that a delete refuses a provider ID that a
// principal, also a deleted one, has in its subject, and that a subject of
// another provider with the same start does not count.
func testProviderDelete(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustCreateProvider(t, s, newProviderConfig("corp"))
	mustCreateProvider(t, s, newProviderConfig("co"))
	mustSubjectPrincipal(t, s, "prn-1", "corp:248289761001")

	if err := s.DeleteProviderConfig(ctx, "corp", providerEntry("corp", at(6))); !errors.Is(
		err, session.ErrProviderInUse,
	) {
		t.Fatalf("delete of a provider with a principal = %v, want ErrProviderInUse", err)
	}

	e := entry("ses-x", at(7))
	e.Object = principal.Ref("prn-1")

	if err := s.DeletePrincipal(ctx, "prn-1", e); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	if err := s.DeleteProviderConfig(ctx, "corp", providerEntry("corp", at(8))); !errors.Is(
		err, session.ErrProviderInUse,
	) {
		t.Fatalf("delete of a provider with a deleted principal = %v, want ErrProviderInUse", err)
	}

	if err := s.DeleteProviderConfig(ctx, "co", providerEntry("co", at(8))); err != nil {
		t.Fatalf("delete of a provider with no principal: %v", err)
	}

	if _, err := s.GetProviderConfig(ctx, "co"); !errors.Is(err, session.ErrProviderNotFound) {
		t.Fatalf("get after a delete = %v, want ErrProviderNotFound", err)
	}

	if err := s.DeleteProviderConfig(ctx, "co", providerEntry("co", at(9))); !errors.Is(
		err, session.ErrProviderNotFound,
	) {
		t.Fatalf("second delete = %v, want ErrProviderNotFound", err)
	}
}

// mustSubjectPrincipal creates the person id with subject.
func mustSubjectPrincipal(t *testing.T, s Store, id, subject string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), DisabledAt: time.Time{}, ID: id, Subject: subject,
		DisplayName: "Name of " + id, Kind: principal.KindPerson, Source: principal.SourceOIDC, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	e := entry("ses-x", at(0))
	e.Object = principal.Ref(id)

	if err := s.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

// testProviderChangeAppendsEntry proves that each change appends its
// entry, and that a refused change appends none.
func testProviderChangeAppendsEntry(t *testing.T, s Store) {
	t.Helper()

	entries, ok := s.(auditLister)
	if !ok {
		t.Skip("the store keeps no audit log to read")
	}

	ctx := t.Context()
	corp := newProviderConfig("corp")
	mustCreateProvider(t, s, corp)

	corp.Version, corp.UpdatedAt = 2, at(6)
	if err := s.UpdateProviderConfig(ctx, corp, providerEntry("corp", at(6))); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := s.UpdateProviderConfig(ctx, corp, providerEntry("corp", at(7))); !errors.Is(
		err, session.ErrProviderChanged,
	) {
		t.Fatalf("stale update = %v, want ErrProviderChanged", err)
	}

	if err := s.DeleteProviderConfig(ctx, "corp", providerEntry("corp", at(8))); err != nil {
		t.Fatalf("delete: %v", err)
	}

	listing, err := entries.List(ctx, audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var times []time.Time

	for i := range listing.Entries {
		if listing.Entries[i].Object == "sign-in-provider:corp" {
			times = append(times, listing.Entries[i].At)
		}
	}

	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })

	if want := []time.Time{at(5), at(6), at(8)}; !slices.EqualFunc(times, want, time.Time.Equal) {
		t.Fatalf("provider entries at %v, want the create, the update and the delete: %v", times, want)
	}
}
