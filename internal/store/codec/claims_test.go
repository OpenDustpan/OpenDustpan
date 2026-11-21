package codec_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestClaimFromRowRefusesDamagedRows sends damaged rows straight to the
// codec, as a database of another version of the schema would give them: a
// row that the store cannot read is an error, never an empty claim.
func TestClaimFromRowRefusesDamagedRows(t *testing.T) {
	t.Parallel()

	good := codec.ClaimRow{
		ID: "clm-1", WorkspaceID: "ws-1", Base: "sv-1", Addresses: `["a","b"]`, Principal: "ada", JobID: "job-1",
		TriggerName: "", State: "accepted", StartedAtMs: 1_700_000_000_000, EndedAtMs: 0,
	}

	claim, err := codec.ClaimFromRow(good)
	if err != nil {
		t.Fatalf("good row = %v", err)
	}

	if claim.State != workspace.ClaimAccepted || len(claim.Addresses) != 2 || !claim.EndedAt.IsZero() {
		t.Fatalf("claim = %+v, want an accepted claim with two addresses and no end time", claim)
	}

	for _, c := range []struct {
		name   string
		mutate func(r *codec.ClaimRow)
	}{
		{"unknown state", func(r *codec.ClaimRow) { r.State = "pending" }},
		{"empty state", func(r *codec.ClaimRow) { r.State = "" }},
		{"null addresses", func(r *codec.ClaimRow) { r.Addresses = "null" }},
		{"addresses that are no array", func(r *codec.ClaimRow) { r.Addresses = `{"a":1}` }},
		{"addresses of numbers", func(r *codec.ClaimRow) { r.Addresses = `[1,2]` }},
		{"text that is no JSON", func(r *codec.ClaimRow) { r.Addresses = "a,b" }},
	} {
		row := good
		c.mutate(&row)

		if _, err = codec.ClaimFromRow(row); !errors.Is(err, codec.ErrClaimRow) {
			t.Errorf("%s: ClaimFromRow = %v, want ErrClaimRow", c.name, err)
		}
	}
}
