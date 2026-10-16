package pulumijournal

import (
	"errors"
	"strings"
	"testing"
)

// TestCheckIndexRefusesMemoryExceeded checks that a build with more estimated
// memory than the bound allows fails with ErrBuildMemoryExceeded, while small
// builds pass.
func TestCheckIndexRefusesMemoryExceeded(t *testing.T) {
	t.Parallel()
	// One indexed resource with 70 MiB of link bytes exceeds the 64 MiB bound
	// with a single entry, so no other bound triggers first.
	big := linkFields{ //nolint:exhaustruct_v5 // The test sets only the links that count toward the bound.
		URN:          "urn:pulumi:dev::web::t:mod:T::big",
		Dependencies: []string{strings.Repeat("d", 70<<20)},
	}
	scan := &baseScan{index: []linkFields{big}} //nolint:exhaustruct_v5 // The test indexes one resource only.
	state := newReplayState(nil, nil)
	if err := state.checkIndex(scan); !errors.Is(err, ErrBuildMemoryExceeded) {
		t.Fatalf("checkIndex with oversized links: err = %v, want ErrBuildMemoryExceeded", err)
	}

	small := &baseScan{ //nolint:exhaustruct_v5 // The test indexes one resource only.
		index: []linkFields{
			{URN: "urn:pulumi:dev::web::t:mod:T::a"}, //nolint:exhaustruct_v5 // The test sets only the URN.
		},
	}
	if err := state.checkIndex(small); err != nil {
		t.Fatalf("checkIndex with small links: err = %v, want nil", err)
	}
}

// TestLinkBytesCountsMembers checks that linkBytes adds every link member.
func TestLinkBytesCountsMembers(t *testing.T) {
	t.Parallel()
	links := linkFields{
		URN:                  "urn:a",
		Parent:               "urn:p",
		Dependencies:         []string{"urn:d"},
		PropertyDependencies: map[string][]string{"p": {"urn:q"}},
		ReplaceWith:          []string{"urn:r"},
		DeletedWith:          "urn:w",
		Aliases:              []string{"urn:l"},
		Provider:             "urn:v",
		ViewOf:               "urn:o",
	}
	var want int64
	want += int64(len("urn:a") + len("urn:p") + len("urn:v") + len("urn:o") + len("urn:w"))
	want += int64(len("urn:d") + len("urn:l") + len("urn:r") + len("urn:q"))
	if got := linkBytes([]linkFields{links}); got != want {
		t.Fatalf("linkBytes = %d, want %d", got, want)
	}
	if got := linkBytes(nil); got != 0 {
		t.Fatalf("linkBytes of no index = %d, want 0", got)
	}
}
