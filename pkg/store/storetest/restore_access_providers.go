package storetest

import (
	"bytes"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
)

// keepProviderPorts holds the ports of the sign-in providers of the API: the
// provider records, the issuer bindings and the installation keys.
type keepProviderPorts struct {
	configs session.ProviderConfigStore
	binder  session.ProviderStore
	keys    secret.InstallationKeyStore
}

// keepProviderStores returns the provider ports of s, failing the test when
// the store misses one: a store that keeps the access tables keeps these
// tables too.
func keepProviderStores(t *testing.T, s RestoreStore) keepProviderPorts {
	t.Helper()

	configs, ok := s.(session.ProviderConfigStore)
	if !ok {
		t.Fatalf("store %T does not implement session.ProviderConfigStore", s)
	}

	binder, ok := s.(session.ProviderStore)
	if !ok {
		t.Fatalf("store %T does not implement session.ProviderStore", s)
	}

	keys, ok := s.(secret.InstallationKeyStore)
	if !ok {
		t.Fatalf("store %T does not implement secret.InstallationKeyStore", s)
	}

	return keepProviderPorts{configs: configs, binder: binder, keys: keys}
}

const (
	// keepLiveProvider is the provider that exists only after the backup.
	keepLiveProvider = "gamma"
	// keepLiveIssuer is the issuer of keepLiveProvider.
	keepLiveIssuer = "https://gamma.example"
	// keepOtherIssuer is an issuer that no provider has.
	keepOtherIssuer = "https://other.example"
)

// mutateKeepProviders changes the sign-in provider tables after the backup:
// it seals a new client secret for the provider corp of the seed, and adds
// the disabled provider gamma with its issuer binding and its installation
// key. The keep restore must keep each change, and the replacing restore
// must undo each one.
func mutateKeepProviders(t *testing.T, ports keepProviderPorts) {
	t.Helper()

	ctx := t.Context()

	corp, err := ports.configs.GetProviderConfig(ctx, "corp")
	if err != nil {
		t.Fatalf("get provider corp: %v", err)
	}

	corp.SealedSecret = []byte("rotated sealed secret")
	corp.UpdatedAt = now()
	corp.Version++

	updateErr := ports.configs.UpdateProviderConfig(ctx, corp, ServerChange(now(), "sign-in-provider:corp"))
	if updateErr != nil {
		t.Fatalf("update provider corp: %v", updateErr)
	}

	gamma := session.ProviderConfig{
		CreatedAt: now(), UpdatedAt: now(), ID: keepLiveProvider, Mode: session.ModeOIDC, Issuer: keepLiveIssuer,
		DisplayName: "Gamma", ClientID: "dustpan", SealedSecret: []byte("gamma sealed secret"), Scopes: nil,
		OAuth: session.OAuthSettings{
			AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "", NameClaim: "", UsernameClaim: "",
			EmailClaim: "",
		}, Version: 1, AutoCreate: false, Enabled: false,
	}
	createErr := ports.configs.CreateProviderConfig(ctx, gamma, ServerChange(now(), "sign-in-provider:gamma"))
	if createErr != nil {
		t.Fatalf("create provider gamma: %v", createErr)
	}

	if _, bindErr := ports.binder.BindProvider(ctx, keepLiveProvider, keepLiveIssuer, now()); bindErr != nil {
		t.Fatalf("bind provider gamma: %v", bindErr)
	}

	scope, err := secret.InstallationScope("sign-in-" + keepLiveProvider)
	if err != nil {
		t.Fatalf("installation scope: %v", err)
	}

	key := secret.WrappedKey{CreatedAt: now(), Scope: scope, WrapperID: "kek-test", Ciphertext: []byte("gamma key")}
	if _, keyErr := ports.keys.CreateInstallationKey(ctx, key); keyErr != nil {
		t.Fatalf("create installation key of gamma: %v", keyErr)
	}
}

// checkKeptProviders proves that the keep restore kept the provider tables
// as they are live: the new secret of corp, and the disabled provider gamma
// with its sealed secret, its issuer binding and its installation key.
func checkKeptProviders(t *testing.T, ports keepProviderPorts) {
	t.Helper()

	ctx := t.Context()

	corp, err := ports.configs.GetProviderConfig(ctx, "corp")
	if err != nil {
		t.Fatalf("get kept provider corp: %v", err)
	}

	if corp.Version != 2 || !bytes.Equal(corp.SealedSecret, []byte("rotated sealed secret")) {
		t.Fatalf("kept provider corp = version %d, secret %q; want version 2 with the rotated secret",
			corp.Version, corp.SealedSecret)
	}

	gamma, err := ports.configs.GetProviderConfig(ctx, keepLiveProvider)
	if err != nil {
		t.Fatalf("get kept provider gamma: %v", err)
	}

	if gamma.Enabled || !bytes.Equal(gamma.SealedSecret, []byte("gamma sealed secret")) {
		t.Fatalf("kept provider gamma = enabled %t, secret %q; want disabled with its secret",
			gamma.Enabled, gamma.SealedSecret)
	}

	issuer, err := ports.binder.BindProvider(ctx, keepLiveProvider, keepOtherIssuer, now())
	if err != nil || issuer != keepLiveIssuer {
		t.Fatalf("kept issuer of gamma = %q, %v; want %q", issuer, err, keepLiveIssuer)
	}

	if _, keyErr := ports.keys.GetInstallationKey(ctx, "sign-in-"+keepLiveProvider); keyErr != nil {
		t.Fatalf("get kept installation key of gamma: %v", keyErr)
	}
}

// checkReplacedProviders proves that the replacing restore brought the
// provider tables of the backup back: corp with its first secret, and no
// provider gamma, issuer binding of gamma or installation key of gamma.
func checkReplacedProviders(t *testing.T, ports keepProviderPorts) {
	t.Helper()

	ctx := t.Context()

	corp, err := ports.configs.GetProviderConfig(ctx, "corp")
	if err != nil {
		t.Fatalf("get replaced provider corp: %v", err)
	}

	if corp.Version != 1 || !bytes.Equal(corp.SealedSecret, []byte("sealed secret")) {
		t.Fatalf("replaced provider corp = version %d, secret %q; want version 1 with the backup secret",
			corp.Version, corp.SealedSecret)
	}

	_, gammaErr := ports.configs.GetProviderConfig(ctx, keepLiveProvider)
	if !errors.Is(gammaErr, session.ErrProviderNotFound) {
		t.Fatalf("get replaced provider gamma: err = %v, want ErrProviderNotFound", gammaErr)
	}

	_, keyErr := ports.keys.GetInstallationKey(ctx, "sign-in-"+keepLiveProvider)
	if !errors.Is(keyErr, secret.ErrKeyNotFound) {
		t.Fatalf("get replaced installation key of gamma: err = %v, want ErrKeyNotFound", keyErr)
	}

	issuer, err := ports.binder.BindProvider(ctx, keepLiveProvider, keepOtherIssuer, now())
	if err != nil || issuer != keepOtherIssuer {
		t.Fatalf("issuer of gamma after the replacing restore = %q, %v; want no binding", issuer, err)
	}
}
