package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// ErrProviderStatic reports a change of a provider ID that the flags, the
// environment or the configuration file of the server set: the API never
// changes or shadows such a provider.
var ErrProviderStatic = errors.New("sign-in provider comes from the configuration of the server")

// ProviderRefPrefix starts the audit object of a sign-in provider.
const ProviderRefPrefix = "sign-in-provider:"

// ProviderRef returns the audit object of the sign-in provider id.
func ProviderRef(id string) string {
	return ProviderRefPrefix + id
}

// secretScopePrefix starts the installation scope of the client secret of
// a provider.
const secretScopePrefix = "sign-in-"

// SecretScope returns the installation scope of the client secret of the
// provider id.
func SecretScope(id string) string {
	return secretScopePrefix + id
}

// SecretSealer seals and opens values of installation scopes.
// *secret.InstallationSecrets meets it. Open returns an error for a value
// that does not open.
type SecretSealer interface {
	Seal(ctx context.Context, id string, plaintext []byte) ([]byte, error)
	Open(ctx context.Context, id string, ciphertext []byte) ([]byte, error)
}

// ProviderInput is a new sign-in provider. ClientSecret is the secret
// itself: the service seals it and never stores or records it.
type ProviderInput struct {
	ID           string
	Issuer       string
	DisplayName  string
	ClientID     string
	ClientSecret string
	Scopes       []string
	AutoCreate   bool
	Enabled      bool
}

// ProviderChange is a change of a sign-in provider. A nil field keeps its
// value. Version is the version that the change read.
type ProviderChange struct {
	Issuer       *string
	DisplayName  *string
	ClientID     *string
	ClientSecret *string
	Scopes       *[]string
	AutoCreate   *bool
	Enabled      *bool
	Version      int64
}

// ProviderAdminConfig holds the dependencies of ProviderAdmin. Static
// lists the provider IDs that the configuration of the server sets.
type ProviderAdminConfig struct {
	Store   ProviderConfigStore
	Binder  ProviderStore
	Secrets SecretSealer
	Now     func() time.Time
	Static  []string
}

// ProviderAdmin manages the sign-in providers that the API keeps. It binds
// each provider ID to its issuer before it stores the provider, as a start
// of the server does for a provider of the configuration. It stores the
// issuer as given, because the sign-in client compares it with the issuer
// of the discovery document, and compares issuers in the form of
// NormalizeIssuer.
type ProviderAdmin struct {
	store   ProviderConfigStore
	binder  ProviderStore
	secrets SecretSealer
	now     func() time.Time
	static  []string
}

// NewProviderAdmin returns a ProviderAdmin. Store, Binder and Secrets are
// required.
func NewProviderAdmin(cfg ProviderAdminConfig) (*ProviderAdmin, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: sign-in provider store", ErrMissingDependency)
	case cfg.Binder == nil:
		return nil, fmt.Errorf("%w: sign-in provider binding", ErrMissingDependency)
	case cfg.Secrets == nil:
		return nil, fmt.Errorf("%w: installation secrets", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &ProviderAdmin{
		store: cfg.Store, binder: cfg.Binder, secrets: cfg.Secrets, now: now, static: slices.Clone(cfg.Static),
	}, nil
}

// Static reports whether the configuration of the server sets the
// provider id.
func (a *ProviderAdmin) Static(id string) bool {
	return slices.Contains(a.static, id)
}

// List returns the stored providers, ordered by ID.
func (a *ProviderAdmin) List(ctx context.Context) ([]ProviderConfig, error) {
	list, err := a.store.ListProviderConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sign-in providers: %w", err)
	}

	return list, nil
}

// Get returns the stored provider id, or ErrProviderNotFound.
func (a *ProviderAdmin) Get(ctx context.Context, id string) (ProviderConfig, error) {
	p, err := a.store.GetProviderConfig(ctx, id)
	if err != nil {
		return ProviderConfig{}, fmt.Errorf("get sign-in provider: %w", err)
	}

	return p, nil
}

// ClientSecret opens the client secret of p.
func (a *ProviderAdmin) ClientSecret(ctx context.Context, p ProviderConfig) (string, error) {
	plain, err := a.secrets.Open(ctx, SecretScope(p.ID), p.SealedSecret)
	if err != nil {
		return "", fmt.Errorf("open the client secret of sign-in provider %s: %w", p.ID, err)
	}

	return string(plain), nil
}

// Create stores the provider of in. It returns ErrProviderStatic for an ID
// of the configuration, ErrIssuerChanged for an ID that another issuer
// used, ErrProviderExists, and ErrInvalidProvider. e is the entry of the
// caller; Create completes it and keeps its changes.
func (a *ProviderAdmin) Create(ctx context.Context, e audit.Entry, in ProviderInput) (ProviderConfig, error) {
	if a.Static(in.ID) {
		return ProviderConfig{}, staticError(in.ID)
	}

	if in.ClientSecret == "" {
		return ProviderConfig{}, fmt.Errorf("%w: the client secret is required", ErrInvalidProvider)
	}

	at := a.now().UTC()
	p := ProviderConfig{
		CreatedAt: at, UpdatedAt: at, ID: in.ID, Issuer: in.Issuer, DisplayName: in.DisplayName,
		ClientID: in.ClientID, SealedSecret: []byte{0}, Scopes: cleanScopes(in.Scopes), Version: 1,
		AutoCreate: in.AutoCreate, Enabled: in.Enabled,
	}

	// Validate before the binding and the seal, so a refused provider
	// records nothing. The placeholder secret passes the length check.
	if err := p.Validate(); err != nil {
		return ProviderConfig{}, err
	}

	if err := a.bind(ctx, p.ID, p.Issuer, at); err != nil {
		return ProviderConfig{}, err
	}

	sealed, err := a.secrets.Seal(ctx, SecretScope(p.ID), []byte(in.ClientSecret))
	if err != nil {
		return ProviderConfig{}, fmt.Errorf("seal the client secret of sign-in provider %s: %w", p.ID, err)
	}

	p.SealedSecret = sealed

	if err := a.store.CreateProviderConfig(ctx, p, providerEntry(e, at, p.ID, createdChanges(p))); err != nil {
		return ProviderConfig{}, fmt.Errorf("create sign-in provider: %w", err)
	}

	return p, nil
}

// Update applies c to the provider id. It returns ErrProviderStatic,
// ErrProviderNotFound, ErrProviderChanged when the provider has another
// version than c.Version, ErrIssuerChanged for an issuer that the ID did
// not have, and ErrInvalidProvider.
func (a *ProviderAdmin) Update(
	ctx context.Context,
	e audit.Entry,
	id string,
	c ProviderChange,
) (ProviderConfig, error) {
	if a.Static(id) {
		return ProviderConfig{}, staticError(id)
	}

	old, err := a.store.GetProviderConfig(ctx, id)
	if err != nil {
		return ProviderConfig{}, fmt.Errorf("get sign-in provider: %w", err)
	}

	if old.Version != c.Version {
		return ProviderConfig{}, fmt.Errorf("%w: %s has version %d, the change read version %d",
			ErrProviderChanged, id, old.Version, c.Version)
	}

	at := a.now().UTC()
	p := applyChange(old, c)
	p.UpdatedAt, p.Version = maxTime(at, old.UpdatedAt), old.Version+1

	if err := p.Validate(); err != nil {
		return ProviderConfig{}, err
	}

	if NormalizeIssuer(p.Issuer) != NormalizeIssuer(old.Issuer) {
		if err := a.bind(ctx, id, p.Issuer, at); err != nil {
			return ProviderConfig{}, err
		}
	}

	if c.ClientSecret != nil {
		if *c.ClientSecret == "" {
			return ProviderConfig{}, fmt.Errorf("%w: the client secret cannot be empty", ErrInvalidProvider)
		}

		sealed, sealErr := a.secrets.Seal(ctx, SecretScope(id), []byte(*c.ClientSecret))
		if sealErr != nil {
			return ProviderConfig{}, fmt.Errorf("seal the client secret of sign-in provider %s: %w", id, sealErr)
		}

		p.SealedSecret = sealed
	}

	changes := changedChanges(old, p)
	if c.ClientSecret != nil {
		changes = append(changes, secretChanged())
	}

	entry := providerEntry(e, at, id, changes)
	if err := a.store.UpdateProviderConfig(ctx, p, entry); err != nil {
		return ProviderConfig{}, fmt.Errorf("update sign-in provider: %w", err)
	}

	return p, nil
}

// Delete deletes the provider id. It returns ErrProviderStatic,
// ErrProviderNotFound, and ErrProviderInUse while a principal has a
// subject of the provider.
func (a *ProviderAdmin) Delete(ctx context.Context, e audit.Entry, id string) error {
	if a.Static(id) {
		return staticError(id)
	}

	at := a.now().UTC()
	if err := a.store.DeleteProviderConfig(ctx, id, providerEntry(e, at, id, nil)); err != nil {
		if errors.Is(err, ErrProviderInUse) {
			return inUseError{id: id, err: err}
		}

		return fmt.Errorf("delete sign-in provider: %w", err)
	}

	return nil
}

// bind records issuer for the provider ID id, and returns an error that
// matches ErrIssuerChanged when the ID has another issuer.
func (a *ProviderAdmin) bind(ctx context.Context, id, issuer string, at time.Time) error {
	return BindProviders(ctx, a.binder, []Provider{{ID: id, Issuer: issuer}}, at)
}

// staticError is the refusal of a change of a provider of the
// configuration. Its text is one sentence that says where to change it.
func staticError(id string) error {
	return fmt.Errorf("%w: sign-in provider %q comes from the flags, the environment or the configuration file of "+
		"the server, so change it there", ErrProviderStatic, id)
}

// inUseError is the refusal of a delete of a provider with principals. Its
// text is one sentence that says to disable the provider instead.
type inUseError struct {
	err error
	id  string
}

func (e inUseError) Error() string {
	return fmt.Sprintf("sign-in provider %q has principals, so disable it with PATCH and \"enabled\": false "+
		"instead of a delete", e.id)
}

// Unwrap returns the store error, which matches ErrProviderInUse.
func (e inUseError) Unwrap() error { return e.err }

// applyChange returns old with the fields of c that are set.
func applyChange(old ProviderConfig, c ProviderChange) ProviderConfig {
	p := old
	p.Scopes = slices.Clone(old.Scopes)

	if c.Issuer != nil {
		p.Issuer = *c.Issuer
	}

	if c.DisplayName != nil {
		p.DisplayName = *c.DisplayName
	}

	if c.ClientID != nil {
		p.ClientID = *c.ClientID
	}

	if c.Scopes != nil {
		p.Scopes = cleanScopes(*c.Scopes)
	}

	if c.AutoCreate != nil {
		p.AutoCreate = *c.AutoCreate
	}

	if c.Enabled != nil {
		p.Enabled = *c.Enabled
	}

	return p
}

// cleanScopes returns scopes with no duplicate, in their order, and never
// nil.
func cleanScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))

	for _, s := range scopes {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}

	return out
}

func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}

	return a
}

// providerEntry completes the entry e of the caller for a change of the
// provider id at the time at. It keeps the changes of the caller and adds
// changes.
func providerEntry(e audit.Entry, at time.Time, id string, changes []audit.Change) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = ProviderRef(id)
	e.Changes = append(slices.Clone(e.Changes), changes...)

	return e
}

// secretSet is the recorded value of a client secret: never the secret.
const secretSet = "set"

// createdChanges records the values of a new provider, with no old value.
// The client secret is only "set".
func createdChanges(p ProviderConfig) []audit.Change {
	fields := providerFields()
	out := make([]audit.Change, 0, len(fields)+1)

	for _, f := range fields {
		out = append(out, audit.Change{Key: f.key, Old: nil, New: f.value(p), Layer: "", TakesEffect: ""})
	}

	return append(out, audit.Change{Key: "clientSecret", Old: nil, New: secretSet, Layer: "", TakesEffect: ""})
}

// changedChanges records the fields that differ between old and p, with
// both values.
func changedChanges(old, p ProviderConfig) []audit.Change {
	var out []audit.Change

	for _, f := range providerFields() {
		before, after := f.value(old), f.value(p)
		if before != after {
			out = append(out, audit.Change{Key: f.key, Old: &before, New: after, Layer: "", TakesEffect: ""})
		}
	}

	return out
}

// secretChanged records a new client secret: "set" before and after,
// never the secret.
func secretChanged() audit.Change {
	before := secretSet

	return audit.Change{Key: "clientSecret", Old: &before, New: secretSet, Layer: "", TakesEffect: ""}
}

// providerField is one recorded field of a provider, by its name in the
// API.
type providerField struct {
	value func(ProviderConfig) string
	key   string
}

// providerFields returns the recorded fields of a provider.
func providerFields() []providerField {
	return []providerField{
		{key: "issuer", value: func(p ProviderConfig) string { return p.Issuer }},
		{key: "displayName", value: func(p ProviderConfig) string { return p.DisplayName }},
		{key: "clientID", value: func(p ProviderConfig) string { return p.ClientID }},
		{key: "scopes", value: func(p ProviderConfig) string { return strings.Join(p.Scopes, " ") }},
		{key: "autoCreate", value: func(p ProviderConfig) string { return strconv.FormatBool(p.AutoCreate) }},
		{key: "enabled", value: func(p ProviderConfig) string { return strconv.FormatBool(p.Enabled) }},
	}
}
