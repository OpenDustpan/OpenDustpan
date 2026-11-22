package cli_test

import (
	"strconv"
	"strings"
	"testing"
)

const (
	testPoolIDJobs   = "rp-0123456789abcdefghij"
	testPoolIDSweeps = "rp-9876543210abcdefghij"
)

// runnerPoolsPath is the route that the command reads.
const runnerPoolsPath = "/api/dustpan/v1/namespaces/acme/runner-pools"

// poolAnswer returns one pool of the server answer.
func poolAnswer(id, name, purpose string, concurrency int, isolation string) string {
	return `{"id":"` + id + `","namespace":"acme","name":"` + name + `","purpose":"` + purpose +
		`","lease":90000,"concurrency":` + strconv.Itoa(concurrency) + `,"isolation":` + isolation +
		`,"gitCredentials":{"mode":"none"},"readOnly":false,` +
		`"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"}`
}

// runnerPoolsCase is one row of the TestRunnerPoolsList table.
type runnerPoolsCase struct {
	name        string
	status      int
	answer      string
	output      string
	errorText   string
	extraOutput string
}

// TestRunnerPoolsList is the table-driven test of `opendustpan runner pools
// list --namespace <ns>` with a fake server: two pools, an empty list, a 403,
// a 404 and a server answer of an unknown shape.
func TestRunnerPoolsList(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	sweeps := poolAnswer(testPoolIDSweeps, "sweeps", "sweeps", 2, `["kubernetes"]`)
	jobs := poolAnswer(testPoolIDJobs, "jobs", "jobs", 1, `["kubernetes","firecracker"]`)

	for _, tc := range []runnerPoolsCase{
		{
			name:   "two pools sorted by name",
			status: 200,
			answer: `{"pools":[` + sweeps + `,` + jobs + `]}`,
			output: "jobs\t" + testPoolIDJobs + "\tjobs\t1\tkubernetes,firecracker\n" +
				"sweeps\t" + testPoolIDSweeps + "\tsweeps\t2\tkubernetes\n",
			errorText:   "",
			extraOutput: "",
		},
		{
			name:        "empty list",
			status:      200,
			answer:      `{"pools":[]}`,
			output:      "",
			errorText:   "",
			extraOutput: "",
		},
		{
			name:        "server refuses",
			status:      403,
			answer:      `{"message":"access denied","errorCode":"access.denied"}`,
			output:      "",
			errorText:   "access.denied",
			extraOutput: "",
		},
		{
			name:        "unknown namespace",
			status:      404,
			answer:      `{"message":"namespace not found","errorCode":"namespace.not-found"}`,
			output:      "",
			errorText:   "namespace.not-found",
			extraOutput: "",
		},
		{
			name:        "unknown JSON shape",
			status:      200,
			answer:      `{"items":[]}`,
			output:      "",
			errorText:   "opendustpan runner pools list",
			extraOutput: "",
		},
		{
			name:        "json output prints the server answer unchanged",
			status:      200,
			answer:      `{"pools":[` + jobs + `]}`,
			output:      `{"pools":[` + jobs + `]}` + "\n",
			errorText:   "",
			extraOutput: "--output json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

			srv := serveCollectionCommands(t, []commandAnswer{
				{"GET", runnerPoolsPath, "", tc.status, tc.answer},
			})

			args := []string{"--server", srv.URL, "--namespace", "acme"}
			if tc.extraOutput != "" {
				args = append(args, strings.Fields(tc.extraOutput)...)
			}

			checkCollectionResult(t, runCommand(t, "runner pools list", args, "variable"),
				tc.output, tc.errorText)
		})
	}
}

// TestRunnerPoolsListNeedsANamespace proves a run without --namespace is a
// usage error that names the flag, and that no request goes out.
func TestRunnerPoolsListNeedsANamespace(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for _, args := range [][]string{
		{},
		{"--namespace", ""},
		{"--namespace", ".."},
		{"--namespace", "acme/other"},
		{"--namespace", "acme", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

			srv := serveCollectionCommands(t, nil)
			got := runCommand(t, "runner pools list", append([]string{"--server", srv.URL}, args...), "variable")

			checkCollectionResult(t, got, "", "--namespace")
		})
	}
}

// TestRunnerPoolsListRejectsHostileAnswers proves the command checks each pool
// of the answer before it prints one line, so an unknown value is an error and
// not partial output.
func TestRunnerPoolsListRejectsHostileAnswers(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for name, answer := range map[string]string{
		"hostile pool ID":    `{"pools":[` + poolAnswer("../other", "jobs", "jobs", 1, `["kubernetes"]`) + `]}`,
		"missing pool ID":    `{"pools":[{"name":"jobs","purpose":"jobs","concurrency":1}]}`,
		"unknown purpose":    `{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "other", 1, `["kubernetes"]`) + `]}`,
		"hostile name":       `{"pools":[` + poolAnswer(testPoolIDJobs, "Bad name", "jobs", 1, `["kubernetes"]`) + `]}`,
		"bad concurrency":    `{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "jobs", 0, `["kubernetes"]`) + `]}`,
		"unknown isolation":  `{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "jobs", 1, `["unknown"]`) + `]}`,
		"isolation not text": `{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "jobs", 1, `[1]`) + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

			srv := serveCollectionCommands(t, []commandAnswer{
				{"GET", runnerPoolsPath, "", 200, answer},
			})

			got := runCommand(t, "runner pools list",
				[]string{"--server", srv.URL, "--namespace", "acme"}, "variable")

			checkCollectionResult(t, got, "", "opendustpan runner pools list")
		})
	}
}

// TestRunnerPoolsListBoundAnswers proves a body that is not JSON and a body
// above the cap of the read are errors that name the command.
func TestRunnerPoolsListBoundAnswers(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for _, answer := range []string{`{`, strings.Repeat("x", 1<<20+1)} {
		t.Run(answer[:1], func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

			srv := serveCollectionCommands(t, []commandAnswer{
				{"GET", runnerPoolsPath, "", 200, answer},
			})

			got := runCommand(t, "runner pools list",
				[]string{"--server", srv.URL, "--namespace", "acme"}, "variable")

			checkCollectionResult(t, got, "", "runner pools list")
		})
	}
}
