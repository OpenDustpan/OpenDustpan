package pulumijournal_test

import (
	"os"
	"strings"
	"testing"
)

// portedFiles returns the files of this package that port upstream code (ADR 43, decision 4.2).
func portedFiles() []string {
	return []string{"features.go", "links.go", "replay.go", "resource.go"}
}

// upstreamCommit is the pinned commit of pulumi/pulumi that the ported code comes from.
const upstreamCommit = "128d3f2ec94e147915a6a928fdf7449d561d560e"

// TestPortedFilesNameTheirUpstream checks that each ported file has the
// Apache-2.0 header, says that it is derived, and names the pinned commit.
func TestPortedFilesNameTheirUpstream(t *testing.T) {
	t.Parallel()
	for _, name := range portedFiles() {
		//nolint:gosec // The test reads the ported source files at fixed paths in this package.
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, want := range []string{"Licensed under the Apache License", "This file is derived from", upstreamCommit} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s does not contain %q", name, want)
			}
		}
	}
}
