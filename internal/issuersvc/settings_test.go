package issuersvc_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

func str(s string) *string { return &s }

func flag(b bool) *bool { return &b }

var errNoFresh = errors.New("the sign-in is too old")

func okFresh() error { return nil }

func TestSettingsDefaultsShowTheirEffectiveValues(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	st, err := h.svc.Get(t.Context(), h.reader, h.ns.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	set := st.Settings
	if set.IssuerURL != "" || !set.AllowBlockSubjects || set.IncludeActorName || set.Version != 0 ||
		!slices.Equal(set.SubjectTemplate, []string{"namespace", "collection", "tool", "unit", "access"}) {
		t.Errorf("default settings = %+v", set)
	}

	if st.EffectiveIssuerURL != h.issuer(h.ns) || !st.HostConfigured || len(st.Keys) != 0 {
		t.Errorf("state = %+v", st)
	}
}

func TestPatchNeedsAFreshStepForAChangeThatReducesSecurity(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()
	url := "https://files.example.com/acme"

	needsFresh := map[string]profile.SettingsPatch{
		"issuerURL":                       {IssuerURL: &url},
		"subject template":                {SubjectTemplate: &[]string{"namespace", "unit"}},
		"includeActorName to true":        {IncludeActorName: flag(true)},
		"subject template to the default": {SubjectTemplate: &[]string{}},
	}

	version := int64(0)

	for name, p := range needsFresh {
		if name == "subject template to the default" {
			continue
		}

		_, err := h.svc.Patch(ctx, h.admin, h.ns.Name, p, version, nil)
		wantErr(t, name+" with no fresh check", err, profile.ErrFreshRequired)

		_, err = h.svc.Patch(ctx, h.admin, h.ns.Name, p, version, func() error { return errNoFresh })
		wantErr(t, name+" with an old sign-in", err, errNoFresh)

		got, err := h.svc.Patch(ctx, h.admin, h.ns.Name, p, version, okFresh)
		if err != nil {
			t.Fatalf("%s with a fresh step: %v", name, err)
		}

		version = got.Version
	}

	// allowBlockSubjects from true to false makes security stronger: no step.
	got, err := h.svc.Patch(ctx, h.admin, h.ns.Name, profile.SettingsPatch{AllowBlockSubjects: flag(false)}, version, nil)
	if err != nil {
		t.Fatalf("allowBlockSubjects to false: %v", err)
	}

	version = got.Version

	// and from false back to true needs one.
	_, err = h.svc.Patch(ctx, h.admin, h.ns.Name, profile.SettingsPatch{AllowBlockSubjects: flag(true)}, version, nil)
	wantErr(t, "allowBlockSubjects to true", err, profile.ErrFreshRequired)

	// includeActorName to false is allowed with no step.
	if _, err = h.svc.Patch(ctx, h.admin, h.ns.Name, profile.SettingsPatch{IncludeActorName: flag(false)}, version, nil); err != nil {
		t.Errorf("includeActorName to false: %v", err)
	}
}

func TestPatchKeepsWhatItDoesNotName(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	got, err := h.svc.Patch(ctx, h.admin, h.ns.Name, profile.SettingsPatch{AllowBlockSubjects: flag(false)}, 0, nil)
	if err != nil || got.Version != 1 || got.UpdatedBy != alice || got.UpdatedAt.IsZero() {
		t.Fatalf("Patch = %+v, %v", got, err)
	}

	if got.IncludeActorName || got.IssuerURL != "" || len(got.SubjectTemplate) != 5 {
		t.Errorf("a patch changed a field it did not name: %+v", got)
	}

	_, err = h.svc.Patch(ctx, h.admin, h.ns.Name, profile.SettingsPatch{IncludeActorName: flag(false)}, 0, nil)
	wantErr(t, "Patch on a stale version", err, profile.ErrConflict)
}

func TestPatchRefusesABadValueAndChangesNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	for name, p := range map[string]profile.SettingsPatch{
		"http URL":          {IssuerURL: str("http://files.example.com/acme")},
		"no scheme":         {IssuerURL: str("files.example.com")},
		"query":             {IssuerURL: str("https://files.example.com/acme?x=1")},
		"fragment":          {IssuerURL: str("https://files.example.com/acme#x")},
		"trailing slash":    {IssuerURL: str("https://files.example.com/acme/")},
		"user information":  {IssuerURL: str("https://user:pw@files.example.com/acme")},
		"space":             {IssuerURL: str("https://files.example.com/a b")},
		"newline":           {IssuerURL: str("https://files.example.com/a\nb")},
		"no host":           {IssuerURL: str("https:///acme")},
		"javascript scheme": {IssuerURL: str("javascript:alert(1)")},
		"too long":          {IssuerURL: str("https://files.example.com/" + strings.Repeat("a", 2100))},
		"unknown key":       {SubjectTemplate: &[]string{"namespace", "email"}},
		"twice":             {SubjectTemplate: &[]string{"namespace", "unit", "unit"}},
		"no namespace":      {SubjectTemplate: &[]string{"unit", "access"}},
		"a per-run value":   {SubjectTemplate: &[]string{"namespace", "job"}},
		"commit in subject": {SubjectTemplate: &[]string{"namespace", "commit"}},
	} {
		_, err := h.svc.Patch(ctx, h.admin, h.ns.Name, p, 0, okFresh)
		wantErr(t, name, err, profile.ErrInvalid)
	}

	set, _ := h.store.Issuer().GetIssuerSettings(ctx, h.ns.Name)
	if set.Version != 0 {
		t.Error("a refused patch stored settings")
	}
}

func TestPatchStoresTheTemplateInTheOrderOfTheSubject(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	got, err := h.svc.Patch(t.Context(), h.admin, h.ns.Name,
		profile.SettingsPatch{SubjectTemplate: &[]string{"access", "unit", "namespace_id", "ref_protected"}}, 0, okFresh)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"namespace_id", "unit", "access", "ref_protected"}; !slices.Equal(got.SubjectTemplate, want) {
		t.Errorf("template = %v, want %v", got.SubjectTemplate, want)
	}
}

func TestEachSettingsChangeWritesAnAuditEntry(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	if _, err := h.svc.Patch(t.Context(), h.admin, h.ns.Name,
		profile.SettingsPatch{IssuerURL: str("https://files.example.com/acme")}, 0, okFresh); err != nil {
		t.Fatal(err)
	}

	list, err := h.store.List(t.Context(), h.ns.Name, audit.Page{Number: 1, Size: 10})
	if err != nil || len(list.Entries) != 1 {
		t.Fatalf("entries = %v, %v", list, err)
	}

	e := list.Entries[0]
	if e.Subject != alice || e.Action != "namespace:manage-issuer" || e.Kind != audit.KindChange || e.Version != "1" ||
		len(e.Changes) != 1 || e.Changes[0].Key != "issuerURL" || e.Changes[0].New != "https://files.example.com/acme" {
		t.Errorf("entry = %+v", e)
	}
}

func TestDiscoveryAndJWKSAnswerOnlyPublicData(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()
	h.clock.Advance(90 * day)
	h.rotateAll()

	d, err := h.svc.Discovery(t.Context(), h.ns.ID)
	if err != nil {
		t.Fatalf("Discovery: %v", err)
	}

	if d.Issuer != h.issuer(h.ns) || d.JWKSURI != h.issuer(h.ns)+"/jwks" ||
		!slices.Equal(d.ResponseTypesSupported, []string{"id_token"}) ||
		!slices.Equal(d.SubjectTypesSupported, []string{"public"}) ||
		!slices.Equal(d.IDTokenSigningAlgValuesSupported, []string{"RS256"}) ||
		!slices.Equal(d.ScopesSupported, []string{"openid"}) {
		t.Errorf("discovery = %+v", d)
	}

	for _, c := range []string{"sub", "aud", "exp", "iat", "iss", "jti", "nbf", "namespace", "access", "unit_id", "ref_protected"} {
		if !slices.Contains(d.ClaimsSupported, c) {
			t.Errorf("claims_supported lacks %q", c)
		}
	}

	raw, _ := json.Marshal(d)

	var members map[string]json.RawMessage
	if err = json.Unmarshal(raw, &members); err != nil || len(members) != 7 {
		t.Errorf("discovery members = %d, %v", len(members), err)
	}

	var set struct {
		Keys []map[string]string `json:"keys"`
	}

	if err = json.Unmarshal(h.jwks(h.ns), &set); err != nil || len(set.Keys) != 2 {
		t.Fatalf("JWKS = %v, %v", set, err)
	}

	for _, k := range set.Keys {
		got := make([]string, 0, len(k))
		for name := range k {
			got = append(got, name)
		}

		slices.Sort(got)

		if !slices.Equal(got, []string{"alg", "e", "kid", "kty", "n", "use"}) || k["kty"] != "RSA" || k["alg"] != "RS256" ||
			k["use"] != "sig" || k["e"] != "AQAB" {
			t.Errorf("JWK members = %v", k)
		}
	}
}

func TestAnUnknownNamespaceIDIsNotFoundAndAnEmptyOneHasNoKeys(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.svc.Discovery(t.Context(), namespace.NewID())
	wantErr(t, "Discovery of an unknown ID", err, namespace.ErrNotFound)

	_, err = h.svc.JWKS(t.Context(), namespace.NewID())
	wantErr(t, "JWKS of an unknown ID", err, namespace.ErrNotFound)

	if got := string(h.jwks(h.ns)); got != `{"keys":[]}` {
		t.Errorf("JWKS of a namespace with no key = %s", got)
	}

	if _, err = h.svc.Discovery(t.Context(), h.ns.ID); err != nil {
		t.Errorf("a namespace with no key has no discovery document: %v", err)
	}
}

func TestTheIssuerURLFollowsTheStableIDAndNeverTheName(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()

	d, err := h.svc.Discovery(t.Context(), h.ns.ID)
	if err != nil || strings.Contains(d.Issuer, "acme") || !strings.HasSuffix(d.Issuer, h.ns.ID.String()) {
		t.Errorf("issuer = %q, %v", d.Issuer, err)
	}
}
