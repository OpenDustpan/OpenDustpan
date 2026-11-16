package errcode_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

func TestPlacementNoMatchNamesTheCommand(t *testing.T) {
	t.Parallel()

	spec, ok := errcode.Lookup(errcode.PlacementNoMatch)
	if !ok || !strings.Contains(spec.Meaning, "opendustpan placement add") {
		t.Fatal("placement.no-match does not name opendustpan placement add")
	}
}
