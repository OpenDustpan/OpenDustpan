package errcode_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// apiDir is the directory of the HTTP adapters, from this package.
const apiDir = "../api"

// knownDir holds one list of the known uncoded error paths for each adapter.
const knownDir = "testdata/uncoded"

// gated lists every HTTP adapter whose error answers must carry a code of the
// catalog.
func gated() []errcodetest.Adapter {
	return []errcodetest.Adapter{
		{Name: "audit", Bodies: []string{"errorResponse", "codedErrorResponse"}, Skip: nil},
		{Name: "drift", Bodies: []string{"errorResponse"}, Skip: nil},
		{Name: "inventory", Bodies: []string{"errorResponse"}, Skip: nil},
		{Name: "jobs", Bodies: []string{"errorResponse"}, Skip: nil},
		{Name: "notifications", Bodies: []string{"errorResponse"}, Skip: nil},
		{Name: "objectcontext", Bodies: []string{"errorResponse", "codedErrorResponse"}, Skip: nil},
		{Name: "profile", Bodies: []string{"errorResponse"}, Skip: nil},
		{Name: "pulumi", Bodies: []string{"errorResponse", "codedErrorResponse", "freshRefusalResponse"}, Skip: nil},
		{Name: "registry", Bodies: []string{"errorResponse"}, Skip: nil},
		{
			Name:   "runnerapi",
			Bodies: []string{"managementError", "codedManagementError"},
			// The runner protocol has its own codes and its own versions.
			Skip: []string{"classifyProtocol", "classifyProtocolOther"},
		},
		{
			Name:   "server",
			Bodies: []string{"errorResponse", "codedResponse", "reauthenticateResponse", "enterRefusedResponse"},
			Skip:   nil,
		},
		{Name: "tfe", Bodies: []string{"jsonAPIError"}, Skip: nil},
	}
}

// ungated lists each directory of apiDir that is not a gated adapter, and why.
func ungated() map[string]string {
	return map[string]string{
		"apidesc":       "the API description; it writes no error answer",
		"auditrequired": "a helper of the adapters; it writes no error answer",
		"idempotent":    "a helper of the adapters; its errors reach the error writer of each adapter",
		"refusal":       "a helper of the adapters; its errors reach the classify functions of each adapter",
		"tofuhttp":      "the OpenTofu HTTP backend; OpenTofu fixes its error shapes, which have no code field",
	}
}

// TestEveryAdapterIsGated fails for a directory of apiDir that is neither
// gated nor on the ungated list, and for a name on either list that has no
// directory.
func TestEveryAdapterIsGated(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(apiDir)
	if err != nil {
		t.Fatalf("read %s: %v", apiDir, err)
	}

	listed := map[string]bool{}
	for _, a := range gated() {
		listed[a.Name] = true
	}

	for name := range ungated() {
		if listed[name] {
			t.Errorf("adapter %q is gated and ungated", name)
		}

		listed[name] = true
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		if !listed[e.Name()] {
			t.Errorf("directory %s/%s is not gated: add it to gated or, with a reason, to ungated", apiDir, e.Name())
		}

		delete(listed, e.Name())
	}

	for name := range listed {
		t.Errorf("adapter %q has no directory in %s: remove it", name, apiDir)
	}
}

// TestErrorPathsHaveCodes fails for an error path that answers without a code
// of the catalog and is not on the known list of its adapter, and for a path
// on the known list that has a code now or is gone. The known lists only
// shrink: a new error path gets a code.
func TestErrorPathsHaveCodes(t *testing.T) {
	t.Parallel()

	for _, a := range gated() {
		t.Run(a.Name, func(t *testing.T) {
			t.Parallel()

			got, err := errcodetest.UncodedPaths(filepath.Join(apiDir, a.Name), a)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}

			checkKnown(t, a.Name, got, readKnown(t, a.Name))
		})
	}
}

// checkKnown fails for each path of got that known does not have, and for
// each path of known that got does not have.
func checkKnown(t *testing.T, adapter string, got, known []string) {
	t.Helper()

	for _, p := range got {
		if !slices.Contains(known, p) {
			t.Errorf("new uncoded error path: %s: %s: give it a code of the catalog", adapter, p)
		}
	}

	for _, p := range known {
		if !slices.Contains(got, p) {
			t.Errorf("known path has a code now or is gone: %s: %s: remove it from %s",
				adapter, p, filepath.Join(knownDir, adapter+".txt"))
		}
	}
}

// TestKnownListsAreSorted keeps each known list sorted and without repeats, so
// that two changes to one list merge in a predictable way.
func TestKnownListsAreSorted(t *testing.T) {
	t.Parallel()

	for _, a := range gated() {
		known := readKnown(t, a.Name)

		if !slices.IsSorted(known) || len(slices.Compact(slices.Clone(known))) != len(known) {
			t.Errorf("the known list of %s is not sorted or has a repeat", a.Name)
		}
	}
}

// readKnown returns the paths of the known list of the adapter. A missing
// file is an empty list. A line that starts with "#" is a comment.
func readKnown(t *testing.T, adapter string) []string {
	t.Helper()

	root, err := os.OpenRoot(knownDir)
	if err != nil {
		t.Fatalf("open %s: %v", knownDir, err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close %s: %v", knownDir, closeErr)
		}
	}()

	data, err := root.ReadFile(adapter + ".txt")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		t.Fatalf("read the known list of %s: %v", adapter, err)
	}

	var out []string

	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}

	return out
}
