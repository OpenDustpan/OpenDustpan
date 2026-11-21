package codec_test

import (
	"errors"
	"strconv"
	"strings"
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
		{"empty set", func(r *codec.ClaimRow) { r.Addresses = "[]" }},
		{"empty address", func(r *codec.ClaimRow) { r.Addresses = `[""]` }},
		{"unsorted set", func(r *codec.ClaimRow) { r.Addresses = `["b","a"]` }},
		{"duplicate address", func(r *codec.ClaimRow) { r.Addresses = `["a","a"]` }},
		{"untrimmed address", func(r *codec.ClaimRow) { r.Addresses = `[" a"]` }},
		{"address too long", func(r *codec.ClaimRow) { r.Addresses = `["` + strings.Repeat("a", 1025) + `"]` }},
		{"oversize set", func(r *codec.ClaimRow) { r.Addresses = oversizeSet() }},
		{"accepted claim with an end time", func(r *codec.ClaimRow) { r.EndedAtMs = 1 }},
		{"ended claim with no end time", func(r *codec.ClaimRow) { r.State = "ended"; r.EndedAtMs = 0 }},
		{"empty principal", func(r *codec.ClaimRow) { r.Principal = "" }},
		{"empty base", func(r *codec.ClaimRow) { r.Base = "" }},
	} {
		row := good
		c.mutate(&row)

		if _, err = codec.ClaimFromRow(row); !errors.Is(err, codec.ErrClaimRow) {
			t.Errorf("%s: ClaimFromRow = %v, want ErrClaimRow", c.name, err)
		}
	}
}

// oversizeSet returns the stored text of a set one address over the bound.
func oversizeSet() string {
	parts := make([]string, workspace.MaxClaimAddresses+1)
	for i := range parts {
		parts[i] = `"` + strconv.Itoa(1_000_000+i) + `"`
	}

	return "[" + strings.Join(parts, ",") + "]"
}
