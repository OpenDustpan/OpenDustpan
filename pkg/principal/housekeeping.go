package principal

import (
	"opendustpan.org/opendustpan/pkg/identity"
)

// HousekeepingPurpose names one housekeeping purpose of the server. Its
// fields are unexported, so a value comes only from HousekeepingPurposes.
type HousekeepingPurpose struct {
	purpose string
	subject string
	role    string
	actions []identity.Action
}

// Purpose returns the name of the purpose.
func (p HousekeepingPurpose) Purpose() string { return p.purpose }

// Name returns the name of the purpose.
func (p HousekeepingPurpose) Name() string { return p.purpose }

// String returns the name of the purpose.
func (p HousekeepingPurpose) String() string { return p.purpose }

// Subject returns the subject of the principal of the purpose.
func (p HousekeepingPurpose) Subject() string { return p.subject }

// Role returns the name of the role that the principal of the purpose holds.
func (p HousekeepingPurpose) Role() string { return p.role }

// RoleName returns the name of the role that the principal of the purpose holds.
func (p HousekeepingPurpose) RoleName() string { return p.role }

// Actions returns the actions of the role of the purpose.
func (p HousekeepingPurpose) Actions() []identity.Action {
	return append([]identity.Action(nil), p.actions...)
}

// AllowedRoles returns the roles that a grant to the principal of the purpose
// may name.
func (p HousekeepingPurpose) AllowedRoles() []string { return []string{p.role} }

// HousekeepingPurposes returns every housekeeping purpose, in a fixed order.
func HousekeepingPurposes() []HousekeepingPurpose {
	out := make([]HousekeepingPurpose, 0, len(housekeepingPurposeNames))
	for _, name := range housekeepingPurposeNames {
		out = append(out, HousekeepingPurpose{
			purpose: name,
			subject: "housekeeping:" + name,
			role:    "housekeeping:" + name,
			actions: []identity.Action{identity.ActionUnitRead},
		})
	}
	return out
}

// Housekeeping returns the principal of the purpose. It accepts a
// HousekeepingPurpose or the name of a purpose.
func Housekeeping(purpose any) identity.Principal {
	var name string
	switch v := purpose.(type) {
	case HousekeepingPurpose:
		name = v.purpose
	case string:
		name = v
	default:
		name = ""
	}

	for _, p := range HousekeepingPurposes() {
		if p.purpose == name {
			return identity.Principal{
				Subject:     p.subject,
				DisplayName: "Housekeeping principal of " + p.purpose,
				Kind:        identity.KindService,
				Credential:  identity.CredentialServer,
			}
		}
	}

	// An unknown purpose has no principal.
	return identity.Principal{}
}

var housekeepingPurposeNames = []string{
	"artifact-sweep",
	"job-reconcile",
	"saved-view-cleanup",
	"history-retention",
	"name-lock-prune",
	"agent-reaper",
	"checkpoint-sweep",
	"blob-sweep",
	"token-sweep",
	"temp-admin-expiry",
	"issuer-rotation",
}
