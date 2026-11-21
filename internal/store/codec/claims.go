package codec

import (
	"encoding/json"
	"errors"
	"fmt"
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
}

// ClaimFromRow reads a claim from its row. An unknown state or a damaged
// address text is ErrClaimRow.
func ClaimFromRow(r ClaimRow) (workspace.Claim, error) {
	addresses, err := ParseClaimAddresses(r.Addresses)
	if err != nil {
		return workspace.Claim{}, err
	}

	state := workspace.ClaimState(r.State)
	if state != workspace.ClaimAccepted && state != workspace.ClaimEnded {
		return workspace.Claim{}, fmt.Errorf("%w: unknown state %q", ErrClaimRow, r.State)
	}

	var ended time.Time
	if r.EndedAtMs != 0 {
		ended = FromMillis(r.EndedAtMs)
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
	}, nil
}
