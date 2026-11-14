package cli_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/setting"
)

// TestOnlyKnownSettingsTakeDatabaseValues pins the settings that the database
// layer holds to an explicit list. Each row is one key. A key joins the list
// only after a check that the setting is not a host, a URL or an endpoint that
// receives a static credential: a holder of the manage-settings action could
// otherwise point the host at another server, and the server would send the
// static credential there.
func TestOnlyKnownSettingsTakeDatabaseValues(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{}
	for _, d := range cli.SettingDeclarations() {
		declared[d.Key] = d.Database
	}

	databaseKeys := []string{
		"branding.preset",
		"egress.allowPlainConnections",
		"password.minLength",
		"password.requireLettersAndDigits",
		"password.resetLifetime",
		"principal.removalAlertCount",
		"principal.removalAlertPercent",
		"principal.removalAlertWindow",
		"session.breakGlassMaxLength",
		"signIn.localPassword.enabled",
	}

	known := map[string]bool{}
	for _, key := range databaseKeys {
		known[key] = true

		t.Run(key, func(t *testing.T) {
			t.Parallel()

			database, ok := declared[key]
			if !ok {
				t.Fatalf("no declaration for %s", key)
			}

			if !database {
				t.Fatalf("%s: database = false, want true: the setting holds a database value today", key)
			}
		})
	}

	for key, database := range declared {
		if !database || known[key] {
			continue
		}

		t.Errorf("setting %s holds a database value and is not in the list: a setting that the database "+
			"layer holds must not be a host, a URL or an endpoint that receives a static credential; "+
			"add it to the list only after that check", key)
	}
}

// TestStaticCredentialHostsAreStaticOnly pins the settings that name a host, a
// URL or an endpoint that receives a static credential: each one stays static,
// because a change through the settings API could point it at another server
// while the credential stays. Each declaration also carries the
// static-credential-host mark, so the settings registry refuses a database
// layer for it, also for a declaration that an SDK user gives.
//
// public-url and issuer-base-url are URL settings that receive no static
// credential: the server builds external URLs and its own issuer URL from
// them, so no static credential goes to the host that they name, and neither
// carries the mark. A runner gets public-url as the server URL and sends a
// credential there that the server makes at run time, not a static one.
func TestStaticCredentialHostsAreStaticOnly(t *testing.T) {
	t.Parallel()

	declared := map[string]setting.Declaration{}
	for _, d := range cli.SettingDeclarations() {
		declared[d.Key] = d
	}

	rows := []struct {
		key      string
		receives string
	}{
		{key: "audit-sink", receives: "the audit webhook secret and the OTLP headers"},
		{key: "backup-target", receives: "the S3 credentials"},
		{key: "blob-store", receives: "the S3 credentials"},
		{key: "database", receives: "the password inside the URL"},
		{key: "ldap.url", receives: "the LDAP bind password and the password of each person"},
		{key: "log-sink", receives: "the log webhook secret and the OTLP headers"},
		{key: "oidc-issuer", receives: "the OpenID Connect client secret"},
		{key: "s3-endpoint", receives: "the S3 credentials"},
	}

	for _, row := range rows {
		t.Run(row.key, func(t *testing.T) {
			t.Parallel()

			d, ok := declared[row.key]
			if !ok {
				t.Fatalf("no declaration for %s", row.key)
			}

			if d.Database {
				t.Fatalf("%s takes a database value: a holder of the manage-settings action could point it "+
					"at another server and the server would send %s there", row.key, row.receives)
			}

			if !d.StaticCredentialHost {
				t.Fatalf("%s: StaticCredentialHost = false, want true: the server sends %s to the host that "+
					"this setting names, so a database value must never choose that host", row.key, row.receives)
			}
		})
	}
}
