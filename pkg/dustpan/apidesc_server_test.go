package dustpan_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

// newEveryModuleServer builds a server with every optional module turned on:
// jobs, runners, profiles, the registry, saved views, durable operations and the stores of
// idempotency. The description of the API holds all of them.
func newEveryModuleServer(t *testing.T) *dustpan.Server {
	t.Helper()

	return newModuleServer(t, dustpan.WithLDAPProvider(describedLDAPProvider()))
}

// describedLDAPProvider is an LDAP provider that mounts the route of the
// password sign-in of a provider. The server checks its settings and opens no
// connection until a sign-in.
func describedLDAPProvider() dustpan.LDAPProvider {
	return dustpan.LDAPProvider{
		RootCAs: nil, AllowedRanges: nil, ID: "corp-ldap", DisplayName: "Corp", URL: "ldaps://ldap.example.org",
		BindDN: "cn=dustpan,ou=services,dc=example,dc=org", BindPassword: session.NewPassword("service-password"),
		BaseDN: "ou=people,dc=example,dc=org", UserFilter: "(&(objectClass=person)(uid={user}))",
		UserNameAttribute: "uid", EmailAttribute: "mail", NameAttribute: "cn", Subject: "entryUUID",
		DisabledAttribute: "", Groups: "off", GroupBaseDN: "", GroupFilter: "", GroupMemberAttribute: "",
		GroupMemberOfAttribute: "", GroupNameAttribute: "", DisabledValues: nil, DisabledMask: 0, StartTLS: false,
		AutoCreate: true,
	}
}

// newDevModuleServer builds the server of newEveryModuleServer with the
// installation kind dev and the dev sign-in, which adds the route of the
// one-time code.
func newDevModuleServer(t *testing.T) *dustpan.Server {
	t.Helper()

	code, _, err := devcode.Make(nil)
	if err != nil {
		t.Fatalf("make code: %v", err)
	}

	return newModuleServer(t, dustpan.WithInstallationKind(installation.KindDev), devSignInOption(t, code))
}

// newModuleServer builds the server of newEveryModuleServer with more
// options.
func newModuleServer(t *testing.T, extra ...dustpan.Option) *dustpan.Server {
	t.Helper()

	p := newPorts(t)

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn),
		dustpan.WithDurableStore(p.st.DurableOperations()),
		dustpan.WithJobStore(p.st.Jobs()),
		dustpan.WithDriftStore(p.st.DriftResults()),
		dustpan.WithTriggerStore(p.st.Triggers()),
		dustpan.WithUnitSettingsStore(p.st.UnitSettings()),
		dustpan.WithRunners(p.st.Runners()),
		dustpan.WithSourceFilters(p.st.SourceFilters()),
		dustpan.WithTrustRules(p.st.TrustRules()),
		dustpan.WithSelfServiceBlueprints(p.st.SelfServiceBlueprints()),
		dustpan.WithSavedViewStore(p.st.SavedViews()),
		dustpan.WithSettings(p.st.SettingStore(), settingtest.Declarations(), nil),
		dustpan.WithProfileStore(p.st.Profiles()),
		dustpan.WithIssuerStore(p.st.Issuer()),
		dustpan.WithGitHostStore(p.st.GitHost()),
		dustpan.WithRegistry(p.st.Registry(), p.st.RegistryKeys()),
		dustpan.WithSinkStore(p.st.NotificationSinks()),
		dustpan.WithIdempotencyStore(p.st.Idempotency(testWrapper(t))),
		dustpan.WithPublicURL("https://dustpan.example"),
	)
	opts = append(opts, extra...)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}
