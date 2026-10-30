//go:build e2e

package e2e_test

import (
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

func contractRun(version string, requests ...e2e.Entry) e2e.Run {
	return e2e.Run{
		Tool: e2e.ToolPulumi, Label: version, Store: e2e.StoreSQLite, Blobs: "fs", Version: version,
		ChecksumSource: "", Steps: nil, Requests: requests,
	}
}

func contractEntry(path string, status int, body string) e2e.Entry {
	return e2e.Entry{
		Step: "", Method: "GET", Host: "", Path: path, Query: "", UserAgent: "pulumi-cli/1",
		Backend: "", Accept: "", ContentType: "", RequestBody: "", ResponseBody: body,
		Seq: 0, Status: status, RequestBytes: 0, ResponseBytes: 0,
	}
}

func contractServed() []e2e.RouteRef {
	return []e2e.RouteRef{
		{Method: "GET", Path: "/api/user"},
		{Method: "GET", Path: "/api/stacks/{owner}"},
	}
}

func TestObserveContractsKeepsOnlyFieldsOfEveryAnswer(t *testing.T) {
	t.Parallel()

	run := contractRun("3.1.0",
		contractEntry("/api/user", 200, `{"githubLogin":"a","organizations":[{"name":"x"}]}`),
		contractEntry("/api/user", 200, `{"githubLogin":"b","email":"e"}`),
		// A cut answer is not valid JSON: it must not remove a field.
		contractEntry("/api/user", 200, `{"githubLogin":"c","orga`),
	)

	got := e2e.ObserveContracts(contractServed(), []e2e.Run{run})
	if len(got) != 1 || len(got[0].Routes) != 1 {
		t.Fatalf("contracts = %+v", got)
	}

	if want := []string{"githubLogin"}; !slices.Equal(got[0].Routes[0].ResponseKeys, want) {
		t.Errorf("keys = %v, want %v", got[0].Routes[0].ResponseKeys, want)
	}
}

func TestObserveContractsIgnoresHarnessAndUnroutedRequests(t *testing.T) {
	t.Parallel()

	harness := contractEntry("/api/user", 200, `{"a":1}`)
	harness.UserAgent = "opendustpan-e2e-profiles"

	run := contractRun("3.1.0", harness, contractEntry("/api/nothing", 404, ""),
		contractEntry("/api/user", 502, ""))

	if got := e2e.ObserveContracts(contractServed(), []e2e.Run{run}); len(got) != 1 || len(got[0].Routes) != 0 {
		t.Errorf("contracts = %+v, want one contract with no route", got)
	}
}

func TestContractMissingNamesWhatIsGone(t *testing.T) {
	t.Parallel()

	want := e2e.Contract{Tool: e2e.ToolPulumi, Version: "3.1.0", Routes: []e2e.ContractRoute{
		{Route: "GET /api/user", Statuses: []int{200}, ResponseKeys: []string{"githubLogin", "name"}},
		{Route: "GET /api/stacks/{owner}", Statuses: []int{200, 404}, ResponseKeys: nil},
	}}
	got := e2e.Contract{Tool: e2e.ToolPulumi, Version: "3.1.0", Routes: []e2e.ContractRoute{
		{Route: "GET /api/user", Statuses: []int{200}, ResponseKeys: []string{"githubLogin", "extra"}},
		{Route: "GET /api/stacks/{owner}", Statuses: []int{200}, ResponseKeys: nil},
	}}

	lines := strings.Join(want.Missing(got), "\n")
	for _, part := range []string{`no field "name"`, "status 404 is gone"} {
		if !strings.Contains(lines, part) {
			t.Errorf("missing lines lack %q:\n%s", part, lines)
		}
	}

	if strings.Contains(lines, "extra") {
		t.Errorf("an extra field must not be a problem:\n%s", lines)
	}

	if lost := (e2e.Contract{Tool: want.Tool, Version: want.Version, Routes: nil}); len(want.Missing(lost)) != 2 {
		t.Errorf("a contract with no route must lose both routes: %v", want.Missing(lost))
	}

	if news := want.New(got); len(news) != 1 || !strings.Contains(news[0], "new field \"extra\"") {
		t.Errorf("New = %v, want the one extra field", news)
	}
}

func TestScenarioRunsLeavesOutTheRunsOfOtherTests(t *testing.T) {
	t.Setenv("E2E_PULUMI_VERSIONS", "3.1.0,latest")

	labeled := func(label string) e2e.Run {
		run := contractRun("3.1.0")
		run.Label = label

		return run
	}

	runs := []e2e.Run{
		labeled("3.1.0"), labeled("latest-delta-on"), labeled("3.1.0-delta-off"),
		labeled("3.1.0-import"), labeled("3.1.0-pair"), labeled("9.9.9"), labeled(""),
	}

	got := make([]string, 0, 3)

	for _, run := range e2e.ScenarioRuns(runs, e2e.Pulumi()) {
		got = append(got, run.Label)
	}

	if want := []string{"3.1.0", "latest-delta-on", "3.1.0-delta-off"}; !slices.Equal(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
}
