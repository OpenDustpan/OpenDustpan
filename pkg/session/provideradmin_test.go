package session_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/session"
)

// memProviderConfigs is a ProviderConfigStore in memory. inUse lists the
// provider IDs that have principals.
type memProviderConfigs struct {
	providers map[string]session.ProviderConfig
	entries   []audit.Entry
	inUse     []string
}

func newMemProviderConfigs() *memProviderConfigs {
	return &memProviderConfigs{providers: map[string]session.ProviderConfig{}, entries: nil, inUse: nil}
}

func (m *memProviderConfigs) CreateProviderConfig(_ context.Context, p session.ProviderConfig, e audit.Entry) error {
	if err := p.Validate(); err != nil {
		return err
	}

	if _, ok := m.providers[p.ID]; ok {
		return session.ErrProviderExists
	}

	m.providers[p.ID] = p
	m.entries = append(m.entries, e)

	return nil
}

func (m *memProviderConfigs) GetProviderConfig(_ context.Context, id string) (session.ProviderConfig, error) {
	p, ok := m.providers[id]
	if !ok {
		return session.ProviderConfig{}, session.ErrProviderNotFound
	}

	return p, nil
}

func (m *memProviderConfigs) ListProviderConfigs(_ context.Context) ([]session.ProviderConfig, error) {
	out := make([]session.ProviderConfig, 0, len(m.providers))
	for id := range m.providers {
		out = append(out, m.providers[id])
	}

	slices.SortFunc(out, func(a, b session.ProviderConfig) int { return strings.Compare(a.ID, b.ID) })

	return out, nil
}

func (m *memProviderConfigs) UpdateProviderConfig(_ context.Context, p session.ProviderConfig, e audit.Entry) error {
	old, ok := m.providers[p.ID]

	switch {
	case !ok:
		return session.ErrProviderNotFound
	case old.Version != p.Version-1:
		return session.ErrProviderChanged
	}

	p.CreatedAt = old.CreatedAt
	m.providers[p.ID] = p
	m.entries = append(m.entries, e)

	return nil
}

func (m *memProviderConfigs) DeleteProviderConfig(_ context.Context, id string, e audit.Entry) error {
	if _, ok := m.providers[id]; !ok {
		return session.ErrProviderNotFound
	}

	if slices.Contains(m.inUse, id) {
		return session.ErrProviderInUse
	}

	delete(m.providers, id)
	m.entries = append(m.entries, e)

	return nil
}

var errOtherScope = errors.New("value of another scope")

// xorSealer seals by a reversible change with the scope in front, so a
// test can see that the store never holds the plaintext.
type xorSealer struct{}

func (xorSealer) Seal(_ context.Context, id string, p []byte) ([]byte, error) {
	out := []byte(id + "|")
	for _, b := range p {
		out = append(out, b^0x5a)
	}

	return out, nil
}

func (xorSealer) Open(_ context.Context, id string, c []byte) ([]byte, error) {
	rest, ok := bytes.CutPrefix(c, []byte(id+"|"))
	if !ok {
		return nil, errOtherScope
	}

	out := make([]byte, 0, len(rest))
	for _, b := range rest {
		out = append(out, b^0x5a)
	}

	return out, nil
}

type adminFixture struct {
	admin  *session.ProviderAdmin
	store  *memProviderConfigs
	binder *memoryProviders
}

func newAdminFixture(t *testing.T, static ...string) adminFixture {
	t.Helper()

	store := newMemProviderConfigs()
	binder := &memoryProviders{err: nil, issuers: map[string]string{}}
	at := time.Date(2026, time.October, 16, 9, 0, 0, 0, time.UTC)

	admin, err := session.NewProviderAdmin(session.ProviderAdminConfig{
		Store: store, Binder: binder, Secrets: xorSealer{}, Now: func() time.Time { return at }, Static: static,
	})
	if err != nil {
		t.Fatalf("provider admin: %v", err)
	}

	return adminFixture{admin: admin, store: store, binder: binder}
}

// sessionCallerEntry is the entry of a caller with a console session.
func sessionCallerEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "corp:alice", IDPSubject: "", TokenID: "",
		Action: "server:manage-sign-in", Object: "", Version: "", ValuePath: "", ValueCount: nil,
		Changes:  []audit.Change{{Key: "sessionId", Old: nil, New: "ses-1", Layer: "", TakesEffect: ""}},
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelConsole, Kind: "", Outcome: "",
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

func corpInput() session.ProviderInput {
	return session.ProviderInput{
		ID: "corp", Mode: "", Issuer: "https://idp.example/", DisplayName: "Corp", ClientID: "dustpan",
		ClientSecret: "client-secret-value", Scopes: []string{"profile", "email", "profile"}, OAuth: noOAuth(),
		AutoCreate: true, Enabled: true,
	}
}

func changeKeys(e audit.Entry) map[string]audit.Change {
	out := map[string]audit.Change{}
	for _, c := range e.Changes {
		out[c.Key] = c
	}

	return out
}

func TestProviderAdminCreate(t *testing.T) {
	t.Parallel()

	f := newAdminFixture(t)

	p, err := f.admin.Create(t.Context(), sessionCallerEntry(), corpInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	checkStoredCorp(t, f, p)
	checkCreateEntry(t, f.store.entries[0])
}

// checkStoredCorp checks the stored provider corp of corpInput.
func checkStoredCorp(t *testing.T, f adminFixture, p session.ProviderConfig) {
	t.Helper()

	stored := f.store.providers["corp"]
	if bytes.Contains(stored.SealedSecret, []byte("client-secret-value")) || p.Issuer != "https://idp.example/" ||
		!slices.Equal(stored.Scopes, []string{"profile", "email"}) || stored.Version != 1 {
		t.Fatalf("stored provider = %+v, want a sealed secret, the issuer as given and two scopes", stored)
	}

	if secret, err := f.admin.ClientSecret(t.Context(), stored); err != nil || secret != "client-secret-value" {
		t.Fatalf("client secret = %q, %v; want the secret", secret, err)
	}

	if f.binder.issuers["corp"] != "https://idp.example" {
		t.Fatalf("bound issuer = %q, want the normalized issuer", f.binder.issuers["corp"])
	}
}

// checkCreateEntry checks the entry of the create of corpInput.
func checkCreateEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	changes := changeKeys(e)
	if changes["sessionId"].New != "ses-1" || changes["clientSecret"].New != "set" ||
		changes["issuer"].New != "https://idp.example/" || e.Object != "sign-in-provider:corp" {
		t.Fatalf("entry = %+v, want the caller session, the values and the secret as set", e)
	}

	for _, c := range e.Changes {
		if strings.Contains(c.New, "client-secret-value") {
			t.Fatalf("entry records the client secret in %q", c.Key)
		}
	}
}

func TestProviderAdminCreateRefusals(t *testing.T) {
	t.Parallel()

	f := newAdminFixture(t, "corp")

	_, err := f.admin.Create(t.Context(), sessionCallerEntry(), corpInput())
	if !errors.Is(err, session.ErrProviderStatic) || !strings.Contains(err.Error(), "so change it there") {
		t.Fatalf("create of a static ID = %v, want ErrProviderStatic", err)
	}

	other := corpInput()
	other.ID = "beta"
	f.binder.issuers["beta"] = "https://old.example"

	if _, err := f.admin.Create(t.Context(), sessionCallerEntry(), other); !errors.Is(err, session.ErrIssuerChanged) {
		t.Fatalf("create of an ID with another bound issuer = %v, want ErrIssuerChanged", err)
	}

	bad := corpInput()
	bad.ID, bad.ClientSecret = "gamma", ""

	if _, err := f.admin.Create(t.Context(), sessionCallerEntry(), bad); !errors.Is(err, session.ErrInvalidProvider) {
		t.Fatalf("create with no secret = %v, want ErrInvalidProvider", err)
	}

	bad = corpInput()
	bad.ID, bad.Issuer = "delta", "not a url"

	if _, err := f.admin.Create(t.Context(), sessionCallerEntry(), bad); !errors.Is(err, session.ErrInvalidProvider) {
		t.Fatalf("create with a bad issuer = %v, want ErrInvalidProvider", err)
	}

	if _, ok := f.binder.issuers["delta"]; ok {
		t.Fatal("a refused create bound its issuer")
	}
}

func TestProviderAdminUpdate(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := newAdminFixture(t)

	if _, err := f.admin.Create(ctx, sessionCallerEntry(), corpInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	name, secret, disabled := "Corp SSO", "new-secret", false

	p, err := f.admin.Update(ctx, sessionCallerEntry(), "corp", session.ProviderChange{
		Mode: nil, Issuer: nil, DisplayName: &name, ClientID: nil, ClientSecret: &secret, Scopes: nil,
		OAuth: noOAuthChange(), AutoCreate: nil, Enabled: &disabled, Version: 1,
	})
	if err != nil || p.Version != 2 || p.DisplayName != name || p.Enabled {
		t.Fatalf("update = %+v, %v; want version 2, the new name, disabled", p, err)
	}

	if got, secretErr := f.admin.ClientSecret(ctx, f.store.providers["corp"]); secretErr != nil || got != secret {
		t.Fatalf("client secret after the update = %q, %v; want the new secret", got, secretErr)
	}

	checkUpdateEntry(t, f.store.entries[1], name)

	stale := session.ProviderChange{
		Mode: nil, Issuer: nil, DisplayName: &name, ClientID: nil, ClientSecret: nil, Scopes: nil,
		OAuth: noOAuthChange(), AutoCreate: nil, Enabled: nil, Version: 1,
	}
	if _, err := f.admin.Update(ctx, sessionCallerEntry(), "corp", stale); !errors.Is(err, session.ErrProviderChanged) {
		t.Fatalf("update of version 1 = %v, want ErrProviderChanged", err)
	}

	same, moved := "HTTPS://IdP.example", "https://other.example"

	for _, tc := range []struct {
		issuer *string
		want   error
	}{{&same, nil}, {&moved, session.ErrIssuerChanged}} {
		current := f.store.providers["corp"].Version
		c := session.ProviderChange{
			Mode: nil, Issuer: tc.issuer, DisplayName: nil, ClientID: nil, ClientSecret: nil, Scopes: nil,
			OAuth: noOAuthChange(), AutoCreate: nil, Enabled: nil, Version: current,
		}

		if _, err := f.admin.Update(ctx, sessionCallerEntry(), "corp", c); !errors.Is(err, tc.want) {
			t.Errorf("update to the issuer %s = %v, want %v", *tc.issuer, err, tc.want)
		}
	}
}

// checkUpdateEntry checks the entry of an update that set the display
// name to name and set a new secret.
func checkUpdateEntry(t *testing.T, e audit.Entry, name string) {
	t.Helper()

	changes := changeKeys(e)
	if c := changes["displayName"]; c.Old == nil || *c.Old != "Corp" || c.New != name {
		t.Fatalf("display name change = %+v, want Corp to %s", c, name)
	}

	if c := changes["clientSecret"]; c.Old == nil || *c.Old != "set" || c.New != "set" {
		t.Fatalf("secret change = %+v, want set to set", c)
	}

	if _, ok := changes["issuer"]; ok {
		t.Fatal("the entry records an issuer that did not change")
	}
}

func TestProviderAdminDelete(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := newAdminFixture(t, "static")

	if _, err := f.admin.Create(ctx, sessionCallerEntry(), corpInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	f.store.inUse = []string{"corp"}

	err := f.admin.Delete(ctx, sessionCallerEntry(), "corp")
	if err == nil {
		t.Fatal("delete of a provider with principals succeeded, want ErrProviderInUse")
	}

	want := `sign-in provider "corp" has principals, so disable it with PATCH and "enabled": false instead of a delete`
	if !errors.Is(err, session.ErrProviderInUse) || err.Error() != want {
		t.Fatalf("delete of a provider with principals = %v, want %q", err, want)
	}

	if err := f.admin.Delete(ctx, sessionCallerEntry(), "static"); !errors.Is(err, session.ErrProviderStatic) {
		t.Fatalf("delete of a static provider = %v, want ErrProviderStatic", err)
	}

	f.store.inUse = nil

	if err := f.admin.Delete(ctx, sessionCallerEntry(), "corp"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// errNoKey is the error of failSealer.
var errNoKey = errors.New("no key-encryption key")

// failSealer is a SecretSealer with no key-encryption key.
type failSealer struct{}

func (failSealer) Seal(context.Context, string, []byte) ([]byte, error) {
	return nil, errNoKey
}

func (failSealer) Open(context.Context, string, []byte) ([]byte, error) {
	return nil, errNoKey
}

// TestProviderAdminCreateWithNoKeyBindsNothing proves that a create that
// fails to seal the client secret binds no issuer to the provider ID and
// stores no provider.
func TestProviderAdminCreateWithNoKeyBindsNothing(t *testing.T) {
	t.Parallel()

	store := newMemProviderConfigs()
	binder := &memoryProviders{err: nil, issuers: map[string]string{}}

	admin, err := session.NewProviderAdmin(session.ProviderAdminConfig{
		Store: store, Binder: binder, Secrets: failSealer{}, Now: time.Now, Static: nil,
	})
	if err != nil {
		t.Fatalf("provider admin: %v", err)
	}

	if _, err := admin.Create(t.Context(), sessionCallerEntry(), corpInput()); !errors.Is(err, errNoKey) {
		t.Fatalf("create with no key-encryption key = %v, want the seal error", err)
	}

	if issuer, ok := binder.issuers["corp"]; ok {
		t.Fatalf("a failed create bound the issuer %q to corp, want no binding", issuer)
	}

	if _, err := store.GetProviderConfig(t.Context(), "corp"); !errors.Is(err, session.ErrProviderNotFound) {
		t.Fatalf("get after a failed create = %v, want ErrProviderNotFound", err)
	}
}
