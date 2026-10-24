package inventory_test

import (
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/outputshare"
)

// TestOutputsRouteAnswersAGoneUnitAsUnknown proves that a unit that a delete
// removes between the check of the read and the read of its outputs answers
// the 404 of an unknown unit, with the same body, and not 500. The source of
// each tool answers a wrapped ErrUnknownProducer for such a unit. A state
// that moved answers 409.
func TestOutputsRouteAnswersAGoneUnitAsUnknown(t *testing.T) {
	t.Parallel()

	unknown := newOutputsFixture(t, selectiveAuthorizer{deny: nil}).get(t, outputsOther)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown unit: status = %d, want 404: %s", unknown.Code, unknown.Body.String())
	}

	for _, tt := range []struct {
		name   string
		target string
		fail   error
		want   int
	}{
		{
			"stack deleted during the read", outputsPulumi,
			fmt.Errorf("read the outputs: %w: stack %q", outputshare.ErrUnknownProducer, "acme/web/prod"),
			http.StatusNotFound,
		},
		{
			"workspace deleted during the read", outputsTofu,
			fmt.Errorf("read the outputs: %w: workspace %q", outputshare.ErrUnknownProducer, "acme/prod"),
			http.StatusNotFound,
		},
		{
			"state moved during the read", outputsTofu,
			fmt.Errorf("read the outputs: %w", outputshare.ErrStateMoved),
			http.StatusConflict,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newOutputsFixture(t, selectiveAuthorizer{deny: nil})
			f.source.fail = tt.fail

			rec := f.get(t, tt.target)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}

			if tt.want == http.StatusNotFound && rec.Body.String() != unknown.Body.String() {
				t.Errorf("body = %s, want the body of an unknown unit %s", rec.Body.String(), unknown.Body.String())
			}

			if f.source.reads != 1 {
				t.Errorf("the source was called %d times, want 1", f.source.reads)
			}
		})
	}
}
