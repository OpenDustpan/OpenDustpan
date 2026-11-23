package cli_test

import (
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/runner"
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
			errorText:   "access denied",
			extraOutput: "",
		},
		{
			name:        "unknown namespace",
			status:      404,
			answer:      `{"message":"namespace not found","errorCode":"namespace.not-found"}`,
			output:      "",
			errorText:   "namespace not found",
			extraOutput: "",
		},
		{
			name:        "unknown JSON shape",
			status:      200,
			answer:      `{"items":[]}`,
			output:      "",
			errorText:   "no pools list",
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

	const (
		invalidID          = "invalid runner pool in the server answer: run opendustpan runner pools list"
		invalidName        = "invalid runner pool name in the server answer: run opendustpan runner pools list"
		unknownPurpose     = "unknown runner pool purpose in the server answer: run opendustpan runner pools list"
		invalidConcurrency = "invalid runner pool concurrency in the server answer: run opendustpan runner pools list"
		unknownIsolation   = "unknown runner pool isolation in the server answer: run opendustpan runner pools list"
		noPoolsList        = "runner pools list: the server answer has no pools list: " +
			"run opendustpan runner pools list again"
		isolationNotText = "runner pools list: decode server answer: json: cannot unmarshal number into " +
			"poolListAnswer.pools.0.isolation.0 of type string: run opendustpan runner pools list again"
		hiddenText = "runner pools list: the server answer has invalid UTF-8 or a hidden character: " +
			"run opendustpan runner pools list again"
		otherNamespace = "runner pool of another namespace in the server answer: run opendustpan runner pools list"
		duplicateKey   = "runner pools list: the server answer has a duplicate key: run opendustpan runner pools list again"
	)

	jobs := poolAnswer(testPoolIDJobs, "jobs", "jobs", 1, `["kubernetes"]`)

	// withNote puts text in a field that the command does not decode, so only
	// the check of the raw answer sees it.
	withNote := func(text string) string {
		return `{"pools":[` + jobs + `],"note":"` + text + `"}`
	}

	for name, tc := range map[string]struct {
		answer    string
		errorText string
	}{
		"hostile pool ID": {
			`{"pools":[` + poolAnswer("../other", "jobs", "jobs", 1, `["kubernetes"]`) + `]}`, invalidID,
		},
		"missing pool ID": {`{"pools":[{"name":"jobs","purpose":"jobs","concurrency":1}]}`, invalidID},
		"no pool prefix": {
			`{"pools":[` + poolAnswer("xx-0123456789abcdefghij", "jobs", "jobs", 1, `["kubernetes"]`) + `]}`, invalidID,
		},
		"bare pool prefix": {
			`{"pools":[` + poolAnswer("rp-", "jobs", "jobs", 1, `["kubernetes"]`) + `]}`, invalidID,
		},
		"pool ID with space": {
			`{"pools":[` + poolAnswer("rp-a b", "jobs", "jobs", 1, `["kubernetes"]`) + `]}`, invalidID,
		},
		"pool ID too long": {
			`{"pools":[` + poolAnswer("rp-"+strings.Repeat("a", 62), "jobs", "jobs", 1, `["kubernetes"]`) + `]}`,
			invalidID,
		},
		"concurrency too big": {
			`{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "jobs", runner.MaxConcurrency+1, `["kubernetes"]`) + `]}`,
			invalidConcurrency,
		},
		"null answer": {`null`, noPoolsList},
		"unknown purpose": {
			`{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "other", 1, `["kubernetes"]`) + `]}`, unknownPurpose,
		},
		"hostile name": {
			`{"pools":[` + poolAnswer(testPoolIDJobs, "Bad name", "jobs", 1, `["kubernetes"]`) + `]}`, invalidName,
		},
		"bad concurrency": {
			`{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "jobs", 0, `["kubernetes"]`) + `]}`, invalidConcurrency,
		},
		"unknown isolation": {
			`{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "jobs", 1, `["unknown"]`) + `]}`, unknownIsolation,
		},
		"isolation not text": {
			`{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "jobs", 1, `[1]`) + `]}`, isolationNotText,
		},
		"C1 OSC sequence":        {withNote("\u009d0;title\u009c"), hiddenText},
		"C1 CSI":                 {withNote("\u009b2J"), hiddenText},
		"right-to-left override": {withNote("abc" + string(rune(0x202e)) + "def"), hiddenText},
		"raw 0x9B byte":          {withNote("\x9b2J"), hiddenText},
		"CR between tokens":      {`{"pools":[` + jobs + `]` + "\r" + `}`, hiddenText},
		"pool of another namespace": {
			`{"pools":[` + strings.Replace(jobs, `"namespace":"acme"`, `"namespace":"other"`, 1) + `]}`,
			otherNamespace,
		},
		"duplicate pools key": {
			`{"pools":[` + poolAnswer(testPoolIDJobs, "jobs", "jobs", 1, `["unknown"]`) + `],"pools":[]}`,
			duplicateKey,
		},
	} {
		for _, extra := range [][]string{{}, {"--output", "json"}} {
			t.Run(name+" "+strings.Join(extra, " "), func(t *testing.T) {
				t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
				t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

				srv := serveCollectionCommands(t, []commandAnswer{
					{"GET", runnerPoolsPath, "", 200, tc.answer},
				})

				args := append([]string{"--server", srv.URL, "--namespace", "acme"}, extra...)
				got := runCommand(t, "runner pools list", args, "variable")

				checkCollectionResult(t, got, "", tc.errorText)
			})
		}
	}
}

// TestRunnerPoolsListRefusesBadFlags proves an unknown flag, a missing
// --server and an --output value other than json are errors, and that no
// request goes out.
func TestRunnerPoolsListRefusesBadFlags(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for name, tc := range map[string]struct {
		args      []string
		noServer  bool
		errorText string
	}{
		"unknown flag":    {[]string{"--namespace", "acme", "--bogus"}, false, "bogus"},
		"no server":       {[]string{"--namespace", "acme"}, true, "--server"},
		"output not json": {[]string{"--namespace", "acme", "--output", "yaml"}, false, "--output"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

			srv := serveCollectionCommands(t, nil)

			args := tc.args
			if !tc.noServer {
				args = append([]string{"--server", srv.URL}, args...)
			}

			checkCollectionResult(t, runCommand(t, "runner pools list", args, "variable"), "", tc.errorText)
		})
	}
}

// TestRunnerPoolsListRefusesBadServerOrToken proves a --server value that is
// not a URL, a --server value with user information and a run without an admin
// token are errors, and that no request goes out.
func TestRunnerPoolsListRefusesBadServerOrToken(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for name, tc := range map[string]struct {
		server      func(base string) string
		tokenSource string
		errorText   string
	}{
		"server not a URL": {
			func(string) string { return "http://%zz" }, "variable", "the value is not a URL",
		},
		"server with user information": {
			func(base string) string { return strings.Replace(base, "http://", "http://admin@", 1) },
			"variable", "the URL has user information",
		},
		"no token": {
			func(base string) string { return base }, "", "no admin token is set",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

			srv := serveCollectionCommands(t, nil)

			got := runCommand(t, "runner pools list",
				[]string{"--server", tc.server(srv.URL), "--namespace", "acme"}, tc.tokenSource)

			checkCollectionResult(t, got, "", tc.errorText)
		})
	}
}

// TestRunnerPoolsListBoundAnswers proves a body that is not JSON, a JSON value
// of the wrong shape and a valid JSON body above the cap of the read are errors
// that name the cause.
func TestRunnerPoolsListBoundAnswers(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	empty := `{"pools":[]}`

	for name, tc := range map[string]struct {
		answer    string
		errorText string
	}{
		"not JSON":       {`{`, "decode server answer"},
		"pools not list": {`{"pools":"text"}`, "decode server answer"},
		"above the cap":  {empty + strings.Repeat(" ", 1<<20+1-len(empty)), "over 1048576 bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

			srv := serveCollectionCommands(t, []commandAnswer{
				{"GET", runnerPoolsPath, "", 200, tc.answer},
			})

			got := runCommand(t, "runner pools list",
				[]string{"--server", srv.URL, "--namespace", "acme"}, "variable")

			checkCollectionResult(t, got, "", tc.errorText)
		})
	}
}
