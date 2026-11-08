package errcode_test

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// apiDir is the directory of the HTTP adapters, from this package.
const apiDir = "../api"

// dustpanDir is the SDK entry point, which answers the errors of the router,
// of a missing component and of maintenance mode, from this package.
const dustpanDir = "../../pkg/dustpan"

// knownDir holds one list of the known uncoded error paths for each adapter.
const knownDir = "testdata/uncoded"

// gated lists every HTTP adapter whose error answers must carry a code of the
// catalog.
func gated() []errcodetest.Adapter {
	return []errcodetest.Adapter{
		api("audit", nil, nil),
		api("drift", nil, nil),
		{
			Name: "dustpan",
			Dir:  dustpanDir,
			Bodies: []string{
				"maintenancePulumiError", "maintenanceInventoryError", "maintenanceTFEError", "routeError",
			},
			// OpenTofu fixes the plain text errors of the HTTP backend. The
			// runner protocol has its own codes and its own versions.
			Skip: []string{"writeMaintenanceHTTPBackend", "writeMaintenanceRunner"},
		},
		api("inventory", nil, nil),
		api("issuer", nil, nil),
		api("jobs", nil, nil),
		api("notifications", nil, nil),
		api("objectcontext", nil, nil),
		// The shared error module of the tool-neutral APIs. Its answers have no
		// known list, so a new answer in it needs a code.
		api("problem", []string{"Body"}, nil),
		api("profile", nil, nil),
		api("pulumi", []string{"errorResponse", "codedErrorResponse", "freshRefusalResponse"}, nil),
		api("registry", nil, nil),
		// The runner protocol has its own codes and its own versions.
		api("runnerapi", nil,
			[]string{"classifyProtocol", "classifyProtocolOther"}),
		api("selfservice", nil, nil),
		api("settings", []string{"bulkError"}, nil),
		api("server", []string{
			"errorResponse", "codedResponse", "codedError", "enterRefusedResponse",
		}, nil),
		api("sourcefilter", nil, nil),
		// The package writes no error answer: it gives each adapter the
		// outcome of a write of state, and the adapter writes the answer. It is
		// gated with no body type and no known list, so a new answer in it
		// needs a code.
		api("stategate", nil, nil),
		api("tfe", []string{"jsonAPIError", "errorClass", "registryErrors"}, nil),
		// The package writes no error answer. It is gated with no body type
		// and no known list, so a new answer in it needs a code.
		api("tofuunit", nil, nil),
		// The package writes no error answer: DecodeBody returns an error of
		// the shared error module to the gated writer of each adapter.
		api("route", nil, nil),
		api("triggers", nil, nil),
		api("trustrules", nil, nil),
	}
}

// notBodies lists, for each gated adapter, the struct types with a JSON field
// code, errorCode or errors that are no error body of the adapter, and why.
// Every other such type must be in the Bodies of its adapter.
func notBodies() map[string]map[string]string {
	return map[string]map[string]string{
		"dustpan": {
			"maintenanceRunnerError": "the 503 of the runner protocol, which has its own codes and its own versions",
			"maintenanceTFEDocument": "the JSON:API document around maintenanceTFEError, which is gated",
		},
		"server": {
			"devCodeRequest":     "the request body of the dev sign-in; code is the sign-in code",
			"restoreWarningJSON": "a warning in the report of a restore that succeeded; it is no error answer",
			"getRestoreResponse": "the 200 row of a restore; errorCode repeats the catalog code that the restore " +
				"stored when it ended, and a test checks that each such code is in the catalog",
			"restoreSummaryJSON": "a 200 row of the list of restores; errorCode repeats the stored catalog code " +
				"of the restore, as getRestoreResponse does",
		},
		"tfe": {
			"jsonAPIDocument": "the JSON:API document around jsonAPIError, which is gated",
		},
	}
}

// TestEveryBodyTypeIsGated fails for a struct type of a gated adapter with a
// JSON field code, errorCode or errors that is neither in the Bodies of the
// adapter nor in notBodies, so that a new error body type cannot hide its
// literals from the gate. It also fails for a name of notBodies that is no
// such type, or that is in Bodies too.
func TestEveryBodyTypeIsGated(t *testing.T) {
	t.Parallel()

	names := map[string]bool{}

	for _, a := range gated() {
		names[a.Name] = true

		ignored := slices.Sorted(maps.Keys(notBodies()[a.Name]))

		missing, err := errcodetest.MissingBodies(a, ignored)
		if err != nil {
			t.Errorf("body types of %s: %v", a.Name, err)

			continue
		}

		for _, name := range missing {
			t.Errorf("type %s of the gated adapter %s has a JSON field code, errorCode or errors: "+
				"add it to the Bodies of the adapter, or to notBodies with a reason", name, a.Name)
		}
	}

	for name := range notBodies() {
		if !names[name] {
			t.Errorf("notBodies names %q, which is no gated adapter: remove it", name)
		}
	}
}

// api returns the gated adapter of the directory name of apiDir.
func api(name string, bodies, skip []string) errcodetest.Adapter {
	return errcodetest.Adapter{Name: name, Dir: filepath.Join(apiDir, name), Bodies: bodies, Skip: skip}
}

// ungated lists each directory of apiDir that is not a gated adapter, and
// each subdirectory of a gated adapter, and why.
func ungated() map[string]string {
	return map[string]string{
		"admit":         "a helper of the adapters; its error reaches the error writer of each adapter",
		"apidesc":       "the API description; it writes no error answer",
		"auditrequired": "a helper of the adapters; it writes no error answer",
		"faultlog":      "a helper of the adapters; it writes the log line of a failure and no error answer",
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
		if filepath.Dir(a.Dir) == apiDir {
			listed[a.Name] = true
		}
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

	coded, err := errcodetest.CodedNames(".")
	if err != nil {
		t.Fatalf("coded names: %v", err)
	}

	for _, a := range gated() {
		t.Run(a.Name, func(t *testing.T) {
			t.Parallel()

			got, err := errcodetest.UncodedPaths(a, coded)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}

			limit, known := readKnown(t, a.Name)
			checkKnown(t, a.Name, got, known)
			checkLimit(t, a.Name, limit, known)
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

// checkLimit fails when the known list of the adapter has more or fewer
// paths than its limit. A path that gets a code lowers the limit in the same
// change, so the limit only goes down, and a higher limit is a line of the
// diff that a review sees.
func checkLimit(t *testing.T, adapter string, limit int, known []string) {
	t.Helper()

	file := filepath.Join(knownDir, adapter+".txt")

	switch {
	case limit < 0:
		t.Errorf("%s has no line %q: add it with the number of its paths", file, limitPrefix+"<n>")
	case len(known) > limit:
		t.Errorf("%s has %d paths and the limit %d: a new error path gets a code, the limit never grows",
			file, len(known), limit)
	case len(known) < limit:
		t.Errorf("%s has %d paths and the limit %d: lower the limit to %d", file, len(known), limit, len(known))
	default:
	}
}

// TestKnownListsAreSorted keeps each known list sorted and without repeats, so
// that two changes to one list merge in a predictable way.
func TestKnownListsAreSorted(t *testing.T) {
	t.Parallel()

	for _, a := range gated() {
		_, known := readKnown(t, a.Name)

		if !slices.IsSorted(known) || len(slices.Compact(slices.Clone(known))) != len(known) {
			t.Errorf("the known list of %s is not sorted or has a repeat", a.Name)
		}
	}
}

// limitPrefix starts the line of a known list that holds its limit.
const limitPrefix = "limit "

// readKnown returns the limit and the paths of the known list of the adapter.
// A missing file is an empty list with the limit 0. A line that starts with
// "#" is a comment. The limit is -1 when the file has no limit line.
func readKnown(t *testing.T, adapter string) (int, []string) {
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
		return 0, nil
	}

	if err != nil {
		t.Fatalf("read the known list of %s: %v", adapter, err)
	}

	limit := -1

	var out []string

	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)

		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, limitPrefix):
			limit, err = strconv.Atoi(strings.TrimPrefix(line, limitPrefix))
			if err != nil || limit < 0 {
				t.Fatalf("the known list of %s has the limit line %q: want a number of 0 or more", adapter, line)
			}
		default:
			out = append(out, line)
		}
	}

	return limit, out
}

// TestGatedAdaptersHaveNoUngatedPackage fails for a subdirectory of a gated
// adapter that holds Go source other than tests, unless ungated names it with
// a reason. The scan reads one directory, so a package under it is not
// gated.
func TestGatedAdaptersHaveNoUngatedPackage(t *testing.T) {
	t.Parallel()

	for _, a := range gated() {
		dirs, err := errcodetest.SourceDirs(a.Dir)
		if err != nil {
			t.Fatalf("source directories of %s: %v", a.Name, err)
		}

		for _, dir := range dirs {
			if _, ok := ungated()[filepath.ToSlash(filepath.Join(a.Name, dir))]; !ok {
				t.Errorf("package %s of the gated adapter %s is not gated: gate it, or add %q with a reason to ungated",
					dir, a.Name, filepath.ToSlash(filepath.Join(a.Name, dir)))
			}
		}
	}
}
