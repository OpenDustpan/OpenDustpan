//go:build e2e

package e2e_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

func contractRun(store e2e.Store, requests ...e2e.Entry) e2e.Run {
	return e2e.Run{
		Tool: e2e.ToolPulumi, Label: "3.1.0", Store: store, Blobs: "fs", Version: "3.1.0",
		ChecksumSource: "", Steps: nil, Requests: requests,
	}
}

func contractEntry(path string, status int, body string) e2e.Entry {
	return e2e.Entry{
		Step: "", Method: "GET", Host: "", Path: path, Query: "", UserAgent: "pulumi-cli/1", Backend: "", Accept: "", ContentType: "", RequestBody: "", ResponseBody: body, ResponseHeaders: []string{"Content-Type: application/json", "X-Request-Id"}, ResponseCut: false, Seq: 0, Status: status, RequestBytes: 0, ResponseBytes: 0, At: time.Time{},
	}
}

func contractServed() []e2e.RouteRef {
	return []e2e.RouteRef{
		{Method: "GET", Path: "/api/user"},
		{Method: "GET", Path: "/api/stacks/{owner}"},
	}
}

// observeOne returns the answer of one route with one status, or fails.
func observeOne(t *testing.T, status int, runs ...e2e.Run) e2e.ContractAnswer {
	t.Helper()

	got := e2e.ObserveContracts(contractServed(), runs)
	if len(got) != 1 {
		t.Fatalf("contracts = %+v, want one", got)
	}

	for _, item := range got[0].Routes {
		if item.Route != "GET /api/user" {
			continue
		}

		for _, answer := range item.Answers {
			if answer.Status == status {
				return answer
			}
		}
	}

	t.Fatalf("no answer %d for GET /api/user in %+v", status, got[0])

	return e2e.ContractAnswer{Status: 0, ResponseKeys: nil, Headers: nil, Cut: false}
}

func TestObserveContractsKeepsOnlyFieldsOfEveryAnswer(t *testing.T) {
	t.Parallel()

	run := contractRun(e2e.StoreSQLite,
		contractEntry("/api/user", 200, `{"githubLogin":"a","organizations":[{"name":"x"}]}`),
		contractEntry("/api/user", 200, `{"githubLogin":"b","email":"e"}`),
	)

	answer := observeOne(t, 200, run)
	if want := []string{"githubLogin"}; !slices.Equal(answer.ResponseKeys, want) {
		t.Errorf("keys = %v, want %v", answer.ResponseKeys, want)
	}
}

func TestObserveContractsRecordsErrorBodiesAndHeaders(t *testing.T) {
	t.Parallel()

	run := contractRun(e2e.StoreSQLite,
		contractEntry("/api/user", 401, `{"code":401,"message":"no","errorCode":"auth.unauthenticated"}`))

	answer := observeOne(t, 401, run)
	if want := []string{"code", "errorCode", "message"}; !slices.Equal(answer.ResponseKeys, want) {
		t.Errorf("keys = %v, want %v", answer.ResponseKeys, want)
	}

	if want := []string{"Content-Type: application/json", "X-Request-Id"}; !slices.Equal(answer.Headers, want) {
		t.Errorf("headers = %v, want %v", answer.Headers, want)
	}
}

func TestObserveContractsMarksACutAnswerAndDoesNotReadItsFields(t *testing.T) {
	t.Parallel()

	cut := contractEntry("/api/user", 200, `{"githubLogin":"c","orga`)
	cut.ResponseCut = true

	run := contractRun(e2e.StoreSQLite,
		contractEntry("/api/user", 200, `{"githubLogin":"a","email":"e"}`), cut)

	answer := observeOne(t, 200, run)
	if !answer.Cut {
		t.Error("a cut answer must set Cut")
	}

	if want := []string{"email", "githubLogin"}; !slices.Equal(answer.ResponseKeys, want) {
		t.Errorf("keys = %v, want the fields of the whole answer %v", answer.ResponseKeys, want)
	}
}

func TestObserveContractsReadsArraysAndDeepFields(t *testing.T) {
	t.Parallel()

	run := contractRun(
		e2e.StoreSQLite,
		contractEntry(
			"/api/user",
			200,
			`[{"a":1,"b":{"c":{"d":{"e":{"f":1}}}}},{"a":2,"b":{"c":{"d":{"e":{"f":2}}}},"x":1}]`,
		),
	)

	answer := observeOne(t, 200, run)

	for _, want := range []string{"[]", "[].a", "[].b.c.d.e.f"} {
		if !slices.Contains(answer.ResponseKeys, want) {
			t.Errorf("keys = %v, lack %q", answer.ResponseKeys, want)
		}
	}

	if slices.Contains(answer.ResponseKeys, "[].x") {
		t.Errorf("keys = %v: [].x is in one element only", answer.ResponseKeys)
	}
}

func TestObserveContractsKeepsAStatusOnlyIfEveryStoreSawIt(t *testing.T) {
	t.Parallel()

	sqlite := contractRun(e2e.StoreSQLite,
		contractEntry("/api/user", 200, `{"a":1}`), contractEntry("/api/user", 409, `{"message":"race"}`))
	postgres := contractRun(e2e.StorePostgres, contractEntry("/api/user", 200, `{"a":1}`))

	got := e2e.ObserveContracts(contractServed(), []e2e.Run{sqlite, postgres})
	if len(got) != 1 || len(got[0].Routes) != 1 || len(got[0].Routes[0].Answers) != 1 ||
		got[0].Routes[0].Answers[0].Status != 200 {
		t.Errorf("contracts = %+v, want only the status 200", got)
	}
}

func TestObserveContractsIgnoresHarnessUnroutedAndServerErrors(t *testing.T) {
	t.Parallel()

	harness := contractEntry("/api/user", 200, `{"a":1}`)
	harness.UserAgent = "opendustpan-e2e-profiles"

	run := contractRun(e2e.StoreSQLite, harness, contractEntry("/api/nothing", 404, ""),
		contractEntry("/api/user", 502, ""))

	if got := e2e.ObserveContracts(contractServed(), []e2e.Run{run}); len(got) != 1 || len(got[0].Routes) != 0 {
		t.Errorf("contracts = %+v, want one contract with no route", got)
	}
}

func TestContractMissingNamesWhatIsGone(t *testing.T) {
	t.Parallel()

	want := e2e.Contract{Tool: e2e.ToolPulumi, Version: "3.1.0", Routes: []e2e.ContractRoute{
		{Route: "GET /api/user", Answers: []e2e.ContractAnswer{
			{Status: 200, ResponseKeys: []string{"githubLogin", "name"}, Headers: []string{"ETag"}, Cut: false},
			{Status: 401, ResponseKeys: []string{"message"}, Headers: nil, Cut: false},
		}},
		{
			Route:   "GET /api/stacks/{owner}",
			Answers: []e2e.ContractAnswer{{Status: 200, ResponseKeys: nil, Headers: nil, Cut: false}},
		},
	}}
	got := e2e.Contract{Tool: e2e.ToolPulumi, Version: "3.1.0", Routes: []e2e.ContractRoute{
		{Route: "GET /api/user", Answers: []e2e.ContractAnswer{
			{Status: 200, ResponseKeys: []string{"githubLogin", "extra"}, Headers: nil, Cut: false},
		}},
	}}

	lines := strings.Join(want.Missing(got), "\n")
	for _, part := range []string{`no field "name"`, `no header "ETag"`, "status 401 is gone", "GET /api/stacks/{owner}: the CLI did not use"} {
		if !strings.Contains(lines, part) {
			t.Errorf("missing lines lack %q:\n%s", part, lines)
		}
	}

	if strings.Contains(lines, "extra") {
		t.Errorf("an extra field must not be a problem:\n%s", lines)
	}

	if news := want.New(got); len(news) != 1 || !strings.Contains(news[0], "new field \"extra\"") {
		t.Errorf("New = %v, want the one extra field", news)
	}
}

func TestScenarioRunsLeavesOutTheRunsOfOtherTests(t *testing.T) {
	t.Setenv("E2E_PULUMI_VERSIONS", "3.1.0,latest")

	labeled := func(label string) e2e.Run {
		run := contractRun(e2e.StoreSQLite)
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
