package inventory_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestOutputsRouteAnswers413OnStateBound proves that a state that a masked read
// refuses answers 413 with the catalog code of the bound, and not 500. The
// source answers a wrapped stack.ErrStateBound.
func TestOutputsRouteAnswers413OnStateBound(t *testing.T) {
	t.Parallel()

	f := newOutputsFixture(t, selectiveAuthorizer{deny: nil})
	f.source.fail = fmt.Errorf("read the outputs: %w", stack.ErrStateBound)

	rec := f.get(t, outputsPulumi)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Code string `json:"errorCode"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}

	if body.Code != errcode.StackStateTooComplex {
		t.Errorf("code = %q, want %q: %s", body.Code, errcode.StackStateTooComplex, rec.Body.String())
	}
}
