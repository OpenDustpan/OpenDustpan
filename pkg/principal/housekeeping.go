package principal

import (
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// HousekeepingPurpose names one housekeeping purpose. A value comes only from
// HousekeepingPurposes.
type HousekeepingPurpose struct {
	name    string
	subject string
	role    string
	actions []identity.Action
}

// Name returns the stable name of the purpose.
func (p HousekeepingPurpose) Name() string { return p.name }

// Subject returns the stable subject of the principal of the purpose.
func (p HousekeepingPurpose) Subject() string { return p.subject }

// Role returns the name of the role of the purpose.
func (p HousekeepingPurpose) Role() string { return p.role }

// Actions returns the actions of the role of the purpose.
func (p HousekeepingPurpose) Actions() []identity.Action { return slices.Clone(p.actions) }

// housekeepingPurposes is the closed list of housekeeping purposes, in name
// order.
//
//nolint:gochecknoglobals // Closed constant table of purposes.
var housekeepingPurposes = []HousekeepingPurpose{
	{name: "agent-reaper", subject: "housekeeping:agent-reaper", role: "housekeeping-agent-reaper", actions: nil},
	{name: "artifact-sweep", subject: "housekeeping:artifact-sweep", role: "housekeeping-artifact-sweep", actions: nil},
	{name: "blob-sweep", subject: "housekeeping:blob-sweep", role: "housekeeping-blob-sweep", actions: nil},
	{name: "checkpoint-sweep", subject: "housekeeping:checkpoint-sweep", role: "housekeeping-checkpoint-sweep", actions: nil},
	{name: "history-retention", subject: "housekeeping:history-retention", role: "housekeeping-history-retention", actions: nil},
	{name: "issuer-rotation", subject: "housekeeping:issuer-rotation", role: "housekeeping-issuer-rotation", actions: nil},
	{name: "job-reconcile", subject: "housekeeping:job-reconcile", role: "housekeeping-job-reconcile", actions: nil},
	{name: "name-lock-prune", subject: "housekeeping:name-lock-prune", role: "housekeeping-name-lock-prune", actions: nil},
	{name: "saved-view-cleanup", subject: "housekeeping:saved-view-cleanup", role: "housekeeping-saved-view-cleanup", actions: nil},
	{name: "temp-admin-expiry", subject: "housekeeping:temp-admin-expiry", role: "housekeeping-temp-admin-expiry", actions: nil},
	{name: "token-sweep", subject: "housekeeping:token-sweep", role: "housekeeping-token-sweep", actions: nil},
}

// HousekeepingPurposes returns the closed list of housekeeping purposes.
func HousekeepingPurposes() []HousekeepingPurpose { return slices.Clone(housekeepingPurposes) }

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
