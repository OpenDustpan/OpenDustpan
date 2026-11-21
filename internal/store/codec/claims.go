package codec

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// ErrClaimRow reports a claim row that the store cannot read: a row of
// another version of the schema, or a damaged row. A caller fails closed.
var ErrClaimRow = errors.New("claim row is not readable")

// ClaimAddressesText returns the stored text of an address set: a JSON
// array of strings.
func ClaimAddressesText(addresses []string) (string, error) {
	raw, err := json.Marshal(addresses)
	if err != nil {
		return "", fmt.Errorf("encode claim addresses: %w", err)
	}

	return string(raw), nil
}

// ParseClaimAddresses reads the stored text of an address set. A value that
// is not a JSON array of strings is an error, so a damaged row never reads
// as an empty set.
func ParseClaimAddresses(text string) ([]string, error) {
	var out []string

	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("%w: addresses: %w", ErrClaimRow, err)
	}

	// Defense in depth: checkClaimSet also refuses an empty set. This check
	// stays so that the text "null" is refused where it is read, for each
	// caller of this function, and it does not rely on the set check.
	if out == nil {
		return nil, fmt.Errorf("%w: addresses are null", ErrClaimRow)
	}

	return out, nil
}

// ClaimRow holds the columns of one claim row.
type ClaimRow struct {
	ID          string
	WorkspaceID string
	Base        string
	Addresses   string
	Principal   string
	JobID       string
	TriggerName string
	State       string
	StartedAtMs int64
	EndedAtMs   int64
	// LeaseEndsAtMs is the end of the lease, or 0 for a claim of a job.
	LeaseEndsAtMs int64
	// RenewedAtMs is the time of the last renewal, or 0 for a claim that no
	// one renewed.
	RenewedAtMs int64
}

// LeaseMillis returns the stored form of the end of a lease: Unix
// milliseconds, or 0 for no lease.
func LeaseMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return Millis(t)
}

// ClaimFromRow reads a claim from its row. An unknown state or a damaged
// address text is ErrClaimRow.
func ClaimFromRow(r ClaimRow) (workspace.Claim, error) {
	addresses, err := ParseClaimAddresses(r.Addresses)
	if err != nil {
		return workspace.Claim{}, err
	}

	if setErr := checkClaimSet(addresses); setErr != nil {
		return workspace.Claim{}, setErr
	}

	state, err := checkClaimState(&r)
	if err != nil {
		return workspace.Claim{}, err
	}

	if fieldsErr := checkClaimFields(&r); fieldsErr != nil {
		return workspace.Claim{}, fieldsErr
	}

	var ended, lease, renewed time.Time
	if r.EndedAtMs != 0 {
		ended = FromMillis(r.EndedAtMs)
	}

	if r.LeaseEndsAtMs != 0 {
		lease = FromMillis(r.LeaseEndsAtMs)
	}

	if r.RenewedAtMs != 0 {
		renewed = FromMillis(r.RenewedAtMs)
	}

	return workspace.Claim{
		ID:          r.ID,
		WorkspaceID: workspace.ID(r.WorkspaceID),
		Base:        workspace.StateVersionID(r.Base),
		Addresses:   addresses,
		Principal:   r.Principal,
		JobID:       r.JobID,
		Trigger:     r.TriggerName,
		State:       state,
		StartedAt:   FromMillis(r.StartedAtMs),
		EndedAt:     ended,
		LeaseEndsAt: lease,
		RenewedAt:   renewed,
	}, nil
}

// checkClaimSet checks that the stored address set of a claim is what the
// registry stores: not empty, within the bounds, with no empty address, and
// trimmed, sorted and without duplicates. A row that is not is ErrClaimRow.
func checkClaimSet(addresses []string) error {
	switch {
	case len(addresses) == 0:
		return fmt.Errorf("%w: the address set is empty", ErrClaimRow)
	case len(addresses) > workspace.MaxClaimAddresses:
		return fmt.Errorf("%w: the address set holds %d addresses", ErrClaimRow, len(addresses))
	}

	// Defense in depth: the normalized-set check below also refuses an empty
	// address. This check stays so that an empty address is refused by its own
	// rule, and it does not rely on that check.
	for _, a := range addresses {
		if a == "" || len(a) > workspace.MaxClaimAddressLength {
			return fmt.Errorf("%w: an address is empty or too long", ErrClaimRow)
		}
	}

	if !slices.Equal(addresses, workspace.NormalizeClaimAddresses(addresses)) {
		return fmt.Errorf("%w: the address set is not normalized", ErrClaimRow)
	}

	return nil
}

// leaseDamaged reports a row whose lease does not fit its job: a claim of a job
// has no lease, a claim with no job has one, and only a claim with a lease has
// a renewal time.
func leaseDamaged(r *ClaimRow) bool {
	return (r.JobID == "") == (r.LeaseEndsAtMs == 0) || (r.RenewedAtMs != 0 && r.LeaseEndsAtMs == 0)
}

// checkClaimState checks that the state of a row is known and agrees with its
// end time. It returns the state.
func checkClaimState(r *ClaimRow) (workspace.ClaimState, error) {
	state := workspace.ClaimState(r.State)

	switch {
	case state != workspace.ClaimAccepted && state != workspace.ClaimEnded:
		return "", fmt.Errorf("%w: unknown state %q", ErrClaimRow, r.State)
	case state == workspace.ClaimAccepted && r.EndedAtMs != 0:
		return "", fmt.Errorf("%w: an accepted claim has an end time", ErrClaimRow)
	case state == workspace.ClaimEnded && r.EndedAtMs == 0:
		return "", fmt.Errorf("%w: an ended claim has no end time", ErrClaimRow)
	default:
		return state, nil
	}
}

// checkClaimFields checks that the lease of a row agrees with its job and that
// the fields that a claim always has are not empty.
func checkClaimFields(r *ClaimRow) error {
	switch {
	case leaseDamaged(r):
		return fmt.Errorf("%w: the lease of the claim does not agree with its job", ErrClaimRow)
	case r.ID == "" || r.WorkspaceID == "" || r.Base == "" || r.Principal == "":
		return fmt.Errorf("%w: a claim has an empty ID, workspace, base or principal", ErrClaimRow)
	default:
		return nil
	}
}
