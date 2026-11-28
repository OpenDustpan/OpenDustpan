package server

import (
	"errors"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// TestReadEgressAllowlistInput_RefusesNilLists calls the conversion of the
// input with null lists, past the check of the handler that runs before it.
// The conversion refuses them: without its own check, the call dereferences a
// nil pointer.
func TestReadEgressAllowlistInput_RefusesNilLists(t *testing.T) {
	t.Parallel()

	a, err := readEgressAllowlistInput(egressAllowlistRequest{Hosts: nil, CIDRs: nil})
	if err == nil {
		t.Fatal("readEgressAllowlistInput() = nil, want an error for the null lists")
	}

	if a.Hosts != nil || a.CIDRs != nil {
		t.Fatalf("readEgressAllowlistInput() = %+v, want no list", a)
	}

	coded, ok := errors.AsType[*codedError](err)
	if !ok || coded.status != http.StatusBadRequest || coded.code != errcode.AgentConnectionAllowlistInvalid {
		t.Fatalf("readEgressAllowlistInput() err = %v, want the coded bad request of the allowlist", err)
	}
}
