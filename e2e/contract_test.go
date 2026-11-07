//go:build e2e

package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// contractReportFile is the file in the report directory that lists, for a CLI
// version with no contract file, what it uses that the newest contract of its
// tool does not have.
const contractReportFile = "contract.md"

// checkContracts compares what each CLI version used in this run with the
// contract file of that version. A version with a file must keep the whole
// contract: a route, a status or a field that is gone fails the test. A
// version with no file (for example a new "latest") never fails here. The
// run writes what it uses that the newest contract of the tool lacks to
// contract.md in the report directory, so a person sees the new reads of a
// new CLI release. With E2E_CONTRACT_UPDATE=1 the run writes the files
// instead of checking them.
func checkContracts(t *testing.T, dir string) {
	t.Helper()

	runs := readCoverageRuns(t, dir)
	if len(runs) == 0 {
		t.Errorf("no e2e run with requests in %s: the CLI contracts need CLI traffic", dir)

		return
	}

	observed := e2e.ObserveContracts(e2e.ServedRoutes(t), e2e.ScenarioRuns(runs, e2e.Pulumi(), e2e.OpenTofu()))

	if os.Getenv(e2e.ContractUpdateEnv) == "1" {
		for _, contract := range observed {
			if err := e2e.WriteContract(e2e.ContractDir, contract); err != nil {
				t.Errorf("%v", err)
			}
		}

		return
	}

	var report strings.Builder

	for _, got := range observed {
		report.WriteString(compareContract(t, got))
	}

	if report.Len() == 0 {
		return
	}

	text := "# New reads of a CLI version with no contract file\n\n" +
		"Check each line. If the server answers it right, run the suite with " +
		e2e.ContractUpdateEnv + "=1 and commit the file.\n\n" + report.String()

	t.Logf("%s", text)

	if err := os.WriteFile(filepath.Join(dir, contractReportFile), []byte(text), 0o600); err != nil {
		t.Errorf("write %s: %v", contractReportFile, err)
	}
}

// binaryModeGaps returns, by route, the statuses that only an in-process run can
// produce. The state.write gate steps of OpenTofu make the 403 of the lock
// route. They need the decision records, which have no route, so they do not
// run against the server binary. A binary run must not fail for a status that
// its steps cannot make. The in-process run still checks every one of them.
func binaryModeGaps() map[string][]int {
	return map[string][]int{
		"POST /api/v2/workspaces/{id}/actions/lock": {403},
	}
}

// inBinaryGap reports whether line, a result of Contract.Missing, names a
// status that binaryModeGaps returns.
func inBinaryGap(line string) bool {
	for route, statuses := range binaryModeGaps() {
		for _, status := range statuses {
			if line == route+": "+fmt.Sprintf("the status %d is gone", status) {
				return true
			}
		}
	}

	return false
}

// compareContract checks got against the contract file of its version. It
// fails the test for each thing the file wants that is gone. For a version with
// no file it returns the lines of the new reads, compared with the newest file
// of the tool.
func compareContract(t *testing.T, got e2e.Contract) string {
	t.Helper()

	files, err := e2e.ReadContracts(e2e.ContractDir, got.Tool)
	if err != nil {
		t.Errorf("%v", err)

		return ""
	}

	if len(files) == 0 {
		return ""
	}

	index := slices.IndexFunc(files, func(c e2e.Contract) bool { return c.Version == got.Version })
	if index >= 0 {
		for _, line := range files[index].Missing(got) {
			if e2e.UseBinaryServer() && inBinaryGap(line) {
				continue
			}

			t.Errorf("%s %s: the contract file wants %s", got.Tool, got.Version, line)
		}

		return ""
	}

	var lines strings.Builder

	for _, line := range files[0].New(got) {
		lines.WriteString("- " + got.Tool + " " + got.Version + " vs " + files[0].Version + ": " + line + "\n")
	}

	return lines.String()
}

// TestContractRoutesAreServed needs no CLI. Every route in a contract file
// must be a route the server serves. It fails when a route is removed or
// renamed, also when the suite runs with other CLI versions.
func TestContractRoutesAreServed(t *testing.T) {
	t.Parallel()

	served := map[string]bool{}
	for _, route := range e2e.ServedRoutes(t) {
		served[route.String()] = true
	}

	for _, tool := range []string{e2e.ToolPulumi, e2e.ToolTofu} {
		files, err := e2e.ReadContracts(e2e.ContractDir, tool)
		if err != nil {
			t.Fatalf("%v", err)
		}

		for _, contract := range files {
			for _, route := range contract.Routes {
				if !served[route.Route] {
					t.Errorf("%s %s: the server does not serve %q", tool, contract.Version, route.Route)
				}
			}
		}
	}
}
