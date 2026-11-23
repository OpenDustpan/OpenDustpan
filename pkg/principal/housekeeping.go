package principal

import (
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// HousekeepingPurpose names one housekeeping purpose. A value comes only from
// HousekeepingPurposes. A purpose is one principal that holds one or two
// roles: a role granted on the installation (@server) and a role granted on
// each namespace. No role granted on the installation holds an action inside a
// namespace, so that a revoke of a grant on a namespace denies the action on
// that namespace.
type HousekeepingPurpose struct {
	name             string
	subject          string
	installationRole string
	namespaceRole    string
}

// Name returns the stable name of the purpose.
func (p HousekeepingPurpose) Name() string { return p.name }

// Subject returns the stable subject of the principal of the purpose.
func (p HousekeepingPurpose) Subject() string { return p.subject }

// ID returns the stable ID of the principal of the purpose, as the baseline
// of each store dialect writes it.
func (p HousekeepingPurpose) ID() string { return "prn-housekeeping-" + p.name }

// InstallationRole returns the name of the role that the principal holds on
// the installation (@server), or "" when the purpose does not act on the
// installation. The baseline of each store dialect writes this grant.
func (p HousekeepingPurpose) InstallationRole() string { return p.installationRole }

// NamespaceRole returns the name of the role that the principal holds on each
// namespace, or "" when the purpose does not act inside a namespace. The
// creation of a namespace writes this grant.
func (p HousekeepingPurpose) NamespaceRole() string { return p.namespaceRole }

// ActsOnInstallation reports whether the principal of the purpose holds a
// grant on the installation (@server).
func (p HousekeepingPurpose) ActsOnInstallation() bool { return p.installationRole != "" }

// ActsInsideNamespace reports whether the principal of the purpose holds a
// grant on each namespace.
func (p HousekeepingPurpose) ActsInsideNamespace() bool { return p.namespaceRole != "" }

// housekeepingPurposeList returns the closed list of housekeeping purposes, in
// name order.
func housekeepingPurposeList() []HousekeepingPurpose {
	return []HousekeepingPurpose{
		{
			name: "agent-reaper", subject: "housekeeping:agent-reaper",
			installationRole: "housekeeping-agent-reveal-sweep", namespaceRole: "housekeeping-agent-run-reaper",
		},
		{
			name: "artifact-sweep", subject: "housekeeping:artifact-sweep",
			installationRole: "housekeeping-artifact-sweep", namespaceRole: "",
		},
		{
			name: "blob-sweep", subject: "housekeeping:blob-sweep",
			installationRole: "housekeeping-blob-sweep", namespaceRole: "",
		},
		{
			name: "checkpoint-sweep", subject: "housekeeping:checkpoint-sweep",
			installationRole: "housekeeping-checkpoint-sweep", namespaceRole: "",
		},
		{
			name: "history-retention", subject: "housekeeping:history-retention",
			installationRole: "", namespaceRole: "housekeeping-history-retention",
		},
		{
			name: "issuer-rotation", subject: "housekeeping:issuer-rotation",
			installationRole: "", namespaceRole: "housekeeping-issuer-rotation",
		},
		{
			name: "job-reconcile", subject: "housekeeping:job-reconcile",
			installationRole: "", namespaceRole: "housekeeping-job-reconcile",
		},
		{
			name: "name-lock-prune", subject: "housekeeping:name-lock-prune",
			installationRole: "housekeeping-name-lock-prune", namespaceRole: "",
		},
		{
			name: "saved-view-cleanup", subject: "housekeeping:saved-view-cleanup",
			installationRole: "", namespaceRole: "housekeeping-saved-view-cleanup",
		},
		{
			name: "temp-admin-expiry", subject: "housekeeping:temp-admin-expiry",
			installationRole: "housekeeping-temp-admin-expiry", namespaceRole: "",
		},
		{
			name: "token-sweep", subject: "housekeeping:token-sweep",
			installationRole: "housekeeping-token-sweep", namespaceRole: "",
		},
	}
}

// HousekeepingPurposes returns the closed list of housekeeping purposes.
func HousekeepingPurposes() []HousekeepingPurpose { return housekeepingPurposeList() }

// Housekeeping returns the identity of the principal of purpose.
func Housekeeping(purpose HousekeepingPurpose) identity.Principal {
	return identity.Principal{
		Subject:                purpose.subject,
		DisplayName:            purpose.subject,
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Kind:                   identity.KindService,
		Credential:             "",
		JobID:                  "",
	}
}
