package principal

import (
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// HousekeepingPurpose names one housekeeping purpose. A value comes only from
// HousekeepingPurposes.
type HousekeepingPurpose struct {
	name                string
	subject             string
	role                string
	actsOnInstallation  bool
	actsInsideNamespace bool
}

// Name returns the stable name of the purpose.
func (p HousekeepingPurpose) Name() string { return p.name }

// Subject returns the stable subject of the principal of the purpose.
func (p HousekeepingPurpose) Subject() string { return p.subject }

// ID returns the stable ID of the principal of the purpose, as the baseline
// of each store dialect writes it.
func (p HousekeepingPurpose) ID() string { return "prn-housekeeping-" + p.name }

// Role returns the name of the role of the purpose.
func (p HousekeepingPurpose) Role() string { return p.role }

// ActsOnInstallation reports whether the principal of the purpose holds a
// grant of its role on the installation (@server). The baseline of each store
// dialect writes this grant.
func (p HousekeepingPurpose) ActsOnInstallation() bool { return p.actsOnInstallation }

// ActsInsideNamespace reports whether the role holds actions that act inside a
// namespace.
func (p HousekeepingPurpose) ActsInsideNamespace() bool { return p.actsInsideNamespace }

// housekeepingPurposeList returns the closed list of housekeeping purposes, in
// name order.
func housekeepingPurposeList() []HousekeepingPurpose {
	return []HousekeepingPurpose{
		{
			name: "agent-reaper", subject: "housekeeping:agent-reaper",
			role: "housekeeping-agent-reaper", actsOnInstallation: true, actsInsideNamespace: true,
		},
		{
			name: "artifact-sweep", subject: "housekeeping:artifact-sweep",
			role: "housekeeping-artifact-sweep", actsOnInstallation: true, actsInsideNamespace: false,
		},
		{
			name: "blob-sweep", subject: "housekeeping:blob-sweep",
			role: "housekeeping-blob-sweep", actsOnInstallation: true, actsInsideNamespace: false,
		},
		{
			name: "checkpoint-sweep", subject: "housekeeping:checkpoint-sweep",
			role: "housekeeping-checkpoint-sweep", actsOnInstallation: true, actsInsideNamespace: false,
		},
		{
			name: "history-retention", subject: "housekeeping:history-retention",
			role: "housekeeping-history-retention", actsOnInstallation: false, actsInsideNamespace: true,
		},
		{
			name: "issuer-rotation", subject: "housekeeping:issuer-rotation",
			role: "housekeeping-issuer-rotation", actsOnInstallation: false, actsInsideNamespace: true,
		},
		{
			name: "job-reconcile", subject: "housekeeping:job-reconcile",
			role: "housekeeping-job-reconcile", actsOnInstallation: false, actsInsideNamespace: true,
		},
		{
			name: "name-lock-prune", subject: "housekeeping:name-lock-prune",
			role: "housekeeping-name-lock-prune", actsOnInstallation: true, actsInsideNamespace: false,
		},
		{
			name: "saved-view-cleanup", subject: "housekeeping:saved-view-cleanup",
			role: "housekeeping-saved-view-cleanup", actsOnInstallation: false, actsInsideNamespace: true,
		},
		{
			name: "temp-admin-expiry", subject: "housekeeping:temp-admin-expiry",
			role: "housekeeping-temp-admin-expiry", actsOnInstallation: true, actsInsideNamespace: false,
		},
		{
			name: "token-sweep", subject: "housekeeping:token-sweep",
			role: "housekeeping-token-sweep", actsOnInstallation: true, actsInsideNamespace: false,
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
