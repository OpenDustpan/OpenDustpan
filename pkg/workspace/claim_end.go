package workspace

import (
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Causes that a claim end records in its audit entry.
const (
	// ClaimEndedByJob is the cause of an end that follows the end of the job
	// of the claim.
	ClaimEndedByJob = "the job ended"
	// ClaimEndedByExpiry is the cause of an end by lease expiry.
	ClaimEndedByExpiry = "the lease expired"
)

// ClaimEnd names one automatic end of a claim: where it was, which claim and
// job, why it ended and when.
type ClaimEnd struct {
	Namespace namespace.Name
	Workspace Name
	ClaimID   string
	JobID     string
	Cause     string
	At        time.Time
}

// ClaimEndEntry returns the audit entry of the automatic end of a claim: the
// server subject, the action that registered the claim, the unit, the claim,
// the job and the cause. The adapters of ClaimStore, and the store of jobs,
// append it in the transaction that ends the claim, so no claim ends without
// its record. The entry holds IDs and names, never a secret.
func ClaimEndEntry(c *ClaimEnd) (audit.Entry, error) {
	unit, err := identity.UnitObject("opentofu", c.Namespace, c.Workspace.String())
	if err != nil {
		return audit.Entry{}, fmt.Errorf("unit of the claim %s: %w", c.ClaimID, err)
	}

	end := "accepted"
	to := string(ClaimEnded)

	return audit.Entry{
		At: c.At.UTC(), Namespace: c.Namespace, Subject: history.ServerSubject, IDPSubject: "", TokenID: "",
		Action: string(identity.ActionUnitClaimRegister), Object: unit.String(), Version: "", ValuePath: "",
		ValueCount: nil,
		Changes: []audit.Change{
			{Key: "state", Old: &end, New: to, Layer: "", TakesEffect: ""},
		},
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: c.ClaimID, PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: audit.PrincipalKindService, JobID: c.JobID,
		Reason: "claim ended: " + c.Cause, Origin: nil,
	}, nil
}
