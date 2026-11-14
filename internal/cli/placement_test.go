package cli_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

const (
	placementRulesPath = "/api/dustpan/v1/namespaces/acme/placement-rules"
	placementAnswer    = `{"id":"` + testRuleID + `","position":1,"tool":"opentofu",` +
		`"project":"*","stack":"web","collection":"` + testCollectionID + `"}`
	placementBody = `{"tool":"opentofu","project":"*","stack":"web","collection":"` + testCollectionID + `"`
)

func placementArgs(extra ...string) []string {
	args := make([]string, 0, 9+len(extra))
	args = append(args, "--tool", "opentofu", "--project", "*", "--stack", "web", "--collection", "prod")
	return append(append(args, extra...), "acme")
}

func TestPlacementCommands(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		command string
		args    []string
		answers []commandAnswer
		output  string
	}{
		{
			command: "placement add", args: placementArgs(),
			answers: []commandAnswer{
				{"GET", collectionsPath, "", 200, collectionList},
				{"POST", placementRulesPath, placementBody + `}`, 201, placementAnswer},
			},
			output: "created placement rule " + testRuleID + " in namespace acme: opentofu project * stack web " +
				"to collection " + testCollectionID + ", position 1\n",
		},
		{
			command: "placement list", args: []string{"acme"},
			answers: []commandAnswer{{"GET", placementRulesPath, "", 200, `{"rules":[` + placementAnswer + `]}`}},
			output:  "1\t" + testRuleID + "\topentofu\t*\tweb\t" + testCollectionID + "\n",
		},
		{
			command: "placement delete", args: []string{"acme", testRuleID},
			answers: []commandAnswer{{"DELETE", placementRulesPath + "/" + testRuleID, "", 204, ""}},
			output:  "deleted placement rule " + testRuleID + " in namespace acme\n",
		},
	} {
		t.Run(tc.command, func(t *testing.T) {
			t.Parallel()
			for _, source := range []string{"variable", "file", ""} {
				t.Run("token "+source, func(t *testing.T) {
					t.Parallel()
					answers, output, errorText := tc.answers, tc.output, ""
					if source == "" {
						answers, output, errorText = nil, "", "no admin token is set"
					}
					srv := serveCollectionCommands(t, answers)
					args := append([]string{"--server", srv.URL}, tc.args...)
					checkCollectionResult(t, runCollectionCommand(t, tc.command, args, source), output, errorText)
				})
			}
		})
	}
}

func TestPlacementAddResolvesCollection(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		collection string
		answer     string
		position   []string
		body       string
		wantErr    string
	}{
		{"name", "prod", collectionList, nil, placementBody + `}`, ""},
		{"case insensitive name", "PROD", collectionList, nil, placementBody + `}`, ""},
		{"stable ID", testCollectionID, collectionList, nil, placementBody + `}`, ""},
		{
			"ID takes priority over a matching name", testCollectionID,
			`{"collections":[{"id":"col-other","name":"` + testCollectionID + `"},` + collectionAnswer + `]}`,
			nil, placementBody + `}`, "",
		},
		{
			"explicit position", "prod", collectionList,
			[]string{"--position", "1"},
			placementBody + `,"position":1}`, "",
		},
		{"unknown name", "missing", collectionList, nil, "", "collection does not exist"},
		{"missing ID", "prod", `{"collections":[{"name":"prod"}]}`, nil, "", "no stable collection ID"},
		{"hostile ID", "prod", `{"collections":[{"id":"../other","name":"prod"}]}`, nil, "", "stable collection ID"},
		{"empty ID suffix", "prod", `{"collections":[{"id":"col-","name":"prod"}]}`, nil, "", "stable collection ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			answers := []commandAnswer{{"GET", collectionsPath, "", 200, tc.answer}}
			if tc.body != "" {
				answers = append(answers, commandAnswer{"POST", placementRulesPath, tc.body, 201, placementAnswer})
			}
			srv := serveCollectionCommands(t, answers)
			args := []string{
				"--server", srv.URL, "--tool", "opentofu", "--project", "*", "--stack", "web",
				"--collection", tc.collection,
			}
			args = append(append(args, tc.position...), "acme")
			got := runCollectionCommand(t, "placement add", args, "variable")
			if tc.wantErr != "" {
				checkCollectionResult(t, got, "", tc.wantErr)
			} else if got.Error != "" {
				t.Fatal(got.Error)
			}
			if tc.name == "unknown name" && !strings.Contains(got.Error, "opendustpan collection list") {
				t.Error("unknown collection error names no collection list command")
			}
		})
	}
}

func TestPlacementCommandsPrintServerCodes(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"placement add", "placement list", "placement delete"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			args := []string{"acme"}
			answers := []commandAnswer{}
			method, path, body := "GET", placementRulesPath, ""
			switch command {
			case "placement add":
				args = placementArgs()
				answers = append(answers, commandAnswer{"GET", collectionsPath, "", 200, collectionList})
				method, body = "POST", placementBody+"}"
			case "placement delete":
				args = append(args, testRuleID)
				method, path = "DELETE", placementRulesPath+"/"+testRuleID
			case "placement list":
			default:
				t.Fatal("unknown placement command")
			}
			answers = append(answers, commandAnswer{
				method, path, body, 403,
				`{"message":"access denied","errorCode":"access.denied"}`,
			})
			srv := serveCollectionCommands(t, answers)
			got := runCollectionCommand(t, command, append([]string{"--server", srv.URL}, args...), "variable")
			checkCollectionResult(t, got, "", "access.denied")
			if !strings.Contains(got.Error, "403 Forbidden") {
				t.Error("server error names no HTTP status")
			}
		})
	}
}

func TestPlacementCommandsRejectHostileArguments(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"placement list", []string{".."}},
		{"placement list", []string{"acme/other"}},
		{"placement delete", []string{"acme", "../other"}},
		{"placement delete", []string{"acme", "bad\nid"}},
		{"placement add", placementArgs("--position", "-1")},
		{"placement add", placementArgs("--position", "0")},
		{"placement add", placementArgs("--tool", "unknown")},
		{"placement add", placementArgs("--project", "**")},
		{"placement add", placementArgs("--stack", "bad?")},
		{"placement add", placementArgs("--collection", "../other")},
		{"placement add", []string{"--tool", "pulumi", "acme"}},
	} {
		t.Run(tc.command+strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			srv := serveCollectionCommands(t, nil)
			got := runCollectionCommand(t, tc.command, append([]string{"--server", srv.URL}, tc.args...), "variable")
			checkCollectionResult(t, got, "", "opendustpan "+tc.command)
		})
	}
}

func TestPlacementCommandsBoundAnswers(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{`{`, strings.Repeat("x", client.MaxClientBodyBytes+1)} {
		t.Run(answer[:1], func(t *testing.T) {
			t.Parallel()
			srv := serveCollectionCommands(t, []commandAnswer{{"GET", collectionsPath, "", 200, answer}})
			got := runCollectionCommand(
				t,
				"placement add",
				append([]string{"--server", srv.URL}, placementArgs()...),
				"variable",
			)
			checkCollectionResult(t, got, "", "server answer")
		})
	}
}

func TestPlacementListRejectsHostileAnswers(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{
		strings.Replace(placementAnswer, testRuleID, "../other", 1),
		strings.Replace(placementAnswer, `"opentofu"`, `"unknown"`, 1),
		strings.Replace(placementAnswer, `"stack":"web"`, `"stack":"bad\nname"`, 1),
		strings.Replace(placementAnswer, testCollectionID, "prod", 1),
	} {
		t.Run(answer, func(t *testing.T) {
			t.Parallel()
			srv := serveCollectionCommands(t, []commandAnswer{
				{"GET", placementRulesPath, "", 200, `{"rules":[` + answer + `]}`},
			})
			got := runCollectionCommand(t, "placement list", []string{"--server", srv.URL, "acme"}, "variable")
			checkCollectionResult(t, got, "", "opendustpan placement list")
		})
	}
}
