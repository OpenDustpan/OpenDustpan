package backup

import (
	"errors"
	"slices"
)

// Access table names. A restore replaces these tables only when the caller
// holds server:restore-access. The constants give every list one source:
// AccessTables, Tables and the portable spec share them.
const (
	TablePrincipals        = "principals"
	TablePrincipalGroups   = "principal_groups"
	TableGroupMembers      = "group_members"
	TableTempAdmins        = "temp_admins"
	TableAccessTokens      = "access_tokens"
	TableRoles             = "roles"
	TableGrants            = "grants"
	TableSessions          = "sessions"
	TableSignInRequests    = "sign_in_requests"
	TableAccessTokenPolicy = "access_token_policy"
	TableSessionPolicy     = "session_policy"
	// TableOIDCProviders keeps the issuer of each sign-in provider ID. A
	// restore that changed it could let another issuer sign in as the
	// people of the provider.
	TableOIDCProviders = "oidc_providers"
	// TableSignInProviders and TableInstallationSecretKeys move together:
	// the client secret of a provider is sealed under an installation key.
	TableSignInProviders        = "sign_in_providers"
	TableInstallationSecretKeys = "installation_secret_keys"
	// TableGitHostAppRegistrations holds the GitHub App of the installation.
	// Its private key and its event secret are sealed under an installation
	// key, so the table moves with TableInstallationSecretKeys: a restore that
	// kept the keys and took the registration of the backup would hold a
	// sealed key that no key opens.
	TableGitHostAppRegistrations = "git_host_app_registrations"
	// TableGitHostInstallations maps a GitHub App installation to the
	// namespace that may use it. Only the operator changes it. A restore
	// without server:restore-access keeps the current rows: a backup that
	// replaced them could give an installation to another namespace.
	TableGitHostInstallations = "git_host_installations"
	// TablePersonPreferences holds the preferences of each person. A row
	// names a principal by a foreign key, so the table moves with the
	// principals table: a restore that kept the principals and took the
	// preferences of the backup would leave rows with no person.
	TablePersonPreferences = "person_preferences"
)

// AccessTables lists the tables that a restore replaces only when the
// caller holds server:restore-access. A restore by any other caller keeps
// the current rows of these tables and replaces the other tables.
var AccessTables = []string{ //nolint:gochecknoglobals // Shared frozen list, like Tables.
	TablePrincipals,
	TablePrincipalGroups,
	TableGroupMembers,
	TableAccessTokens,
	TableRoles,
	TableGrants,
	TableSessions,
	TableSignInRequests,
	TableAccessTokenPolicy,
	TableSessionPolicy,
	TableOIDCProviders,
	TableSignInProviders,
	TableInstallationSecretKeys,
	TableGitHostAppRegistrations,
	TableGitHostInstallations,
	TablePersonPreferences,
}

// ErrRestoreAccessMismatch reports that the backup names access data that
// is not in the current access tables. A site administrator can restore
// the access tables.
//
//nolint:staticcheck // The restore route returns this exact sentence-case text for HTTP 409.
var ErrRestoreAccessMismatch = errors.New(
	"The backup names access data that is not in the current access tables. " +
		"A site administrator can restore the access tables.",
)

// IsAccessTable reports whether table is one of AccessTables.
func IsAccessTable(table string) bool {
	return slices.Contains(AccessTables, table)
}
