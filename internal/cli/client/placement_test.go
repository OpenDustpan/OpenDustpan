package client_test

import (
	"bytes"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

const placementRulesPath = "/api/dustpan/v1/namespaces/acme/placement-rules"

// placementAddArgs returns the flags of a placement add for the rule of the
// quickstart: every Pulumi stack goes to the everything collection. The extra
// flags come before the namespace operand.
func placementAddArgs(u string, extra ...string) []string {
	args := []string{
		"--tool", "pulumi", "--project", "*", "--stack", "*", "--collection", "everything",
	}

	args = append(args, extra...)

	return withServer(u, append(args, "acme")...)
}

// TestPlacementAdd runs placement add against a stub server.
func TestPlacementAdd(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    func(url string) []string
		answer  *stubAnswer
		wantOut string
		wantErr string
	}{
		{
			name: "prints the created rule with its position",
			args: func(u string) []string { return placementAddArgs(u, "--position", "1") },
			answer: &stubAnswer{
				method: "POST", path: placementRulesPath,
				body:   `{"tool":"pulumi","project":"*","stack":"*","collection":"everything","position":1}`,
				status: 201,
				answer: `{"id":"rule-1","namespace":"acme","position":1,"tool":"pulumi",` +
					`"project":"*","stack":"*","collection":"everything","createdBy":"admin","createdAt":1}`,
			},
			wantOut: "created placement rule rule-1 in namespace acme: pulumi project * stack * " +
				"to collection everything, position 1\n",
			wantErr: "",
		},
		{
			name: "leaves position out when the flag is not set",
			args: func(u string) []string { return placementAddArgs(u) },
			answer: &stubAnswer{
				method: "POST", path: placementRulesPath,
				body:   `{"tool":"pulumi","project":"*","stack":"*","collection":"everything"}`,
				status: 201,
				answer: `{"id":"rule-2","namespace":"acme","position":2,"tool":"pulumi",` +
					`"project":"*","stack":"*","collection":"everything","createdBy":"admin","createdAt":1}`,
			},
			wantOut: "created placement rule rule-2 in namespace acme: pulumi project * stack * " +
				"to collection everything, position 2\n",
			wantErr: "",
		},
		{
			name: "missing tool names the flag",
			args: func(u string) []string {
				return withServer(u, "--project", "*", "--stack", "*", "--collection", "everything", "acme")
			},
			answer:  nil,
			wantOut: "",
			wantErr: "needs --tool",
		},
		{
			name: "missing collection names the flag",
			args: func(u string) []string {
				return withServer(u, "--tool", "pulumi", "--project", "*", "--stack", "*", "acme")
			},
			answer:  nil,
			wantOut: "",
			wantErr: "needs --collection",
		},
		{
			name: "missing namespace names the argument",
			args: func(u string) []string {
				return withServer(u, "--tool", "pulumi", "--project", "*", "--stack", "*", "--collection", "everything")
			},
			answer:  nil,
			wantOut: "",
			wantErr: "needs one argument <namespace>",
		},
		{
			name: "missing server names the flag",
			args: func(string) []string {
				return []string{
					"--tool",
					"pulumi",
					"--project",
					"*",
					"--stack",
					"*",
					"--collection",
					"everything",
					"acme",
				}
			},
			answer:  nil,
			wantOut: "",
			wantErr: "needs --server",
		},
		{
			name: "server refusal passes through",
			args: func(u string) []string { return placementAddArgs(u) },
			answer: &stubAnswer{
				method: "POST", path: placementRulesPath,
				body:   `{"tool":"pulumi","project":"*","stack":"*","collection":"everything"}`,
				status: 422,
				answer: `{"code":422,"message":"collection does not exist"}`,
			},
			wantOut: "",
			wantErr: "collection does not exist",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := serveStub(t, tc.answer)

			var stdout, stderr bytes.Buffer

			err := client.PlacementAddWithLookup(t.Context(), tc.args(srv.URL), tokenLookup, &stdout, &stderr)
			checkResult(t, stdout.String(), err, tc.wantOut, tc.wantErr)
		})
	}
}

// TestPlacementList runs placement list against a stub server.
func TestPlacementList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    func(url string) []string
		answer  *stubAnswer
		wantOut string
		wantErr string
	}{
		{
			name: "prints one line for each rule",
			args: func(u string) []string { return withServer(u, "acme") },
			answer: &stubAnswer{
				method: "GET", path: placementRulesPath, body: "", status: 200,
				answer: `{"rules":[{"id":"rule-1","namespace":"acme","position":1,"tool":"pulumi",` +
					`"project":"*","stack":"*","collection":"everything"}]}`,
			},
			wantOut: "1\trule-1\tpulumi\t*\t*\teverything\n",
			wantErr: "",
		},
		{
			name:    "missing namespace names the argument",
			args:    func(u string) []string { return withServer(u) },
			answer:  nil,
			wantOut: "",
			wantErr: "needs one argument <namespace>",
		},
		{
			name: "server refusal passes through",
			args: func(u string) []string { return withServer(u, "acme") },
			answer: &stubAnswer{
				method: "GET", path: placementRulesPath, body: "", status: 404,
				answer: `{"code":404,"message":"namespace not found"}`,
			},
			wantOut: "",
			wantErr: "404 Not Found: namespace not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := serveStub(t, tc.answer)

			var stdout, stderr bytes.Buffer

			err := client.PlacementListWithLookup(t.Context(), tc.args(srv.URL), tokenLookup, &stdout, &stderr)
			checkResult(t, stdout.String(), err, tc.wantOut, tc.wantErr)
		})
	}
}

// TestPlacementDelete runs placement delete against a stub server.
func TestPlacementDelete(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    func(url string) []string
		answer  *stubAnswer
		wantOut string
		wantErr string
	}{
		{
			name: "prints the deleted rule",
			args: func(u string) []string { return withServer(u, "acme", "rule-1") },
			answer: &stubAnswer{
				method: "DELETE", path: placementRulesPath + "/rule-1", body: "", status: 204, answer: "",
			},
			wantOut: "deleted placement rule rule-1 in namespace acme\n",
			wantErr: "",
		},
		{
			name:    "missing rule ID names the arguments",
			args:    func(u string) []string { return withServer(u, "acme") },
			answer:  nil,
			wantOut: "",
			wantErr: "needs two arguments <namespace> <rule-id>",
		},
		{
			name: "unknown rule passes through",
			args: func(u string) []string { return withServer(u, "acme", "rule-9") },
			answer: &stubAnswer{
				method: "DELETE", path: placementRulesPath + "/rule-9", body: "", status: 404,
				answer: `{"code":404,"message":"placement rule not found"}`,
			},
			wantOut: "",
			wantErr: "404 Not Found: placement rule not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := serveStub(t, tc.answer)

			var stdout, stderr bytes.Buffer

			err := client.PlacementDeleteWithLookup(t.Context(), tc.args(srv.URL), tokenLookup, &stdout, &stderr)
			checkResult(t, stdout.String(), err, tc.wantOut, tc.wantErr)
		})
	}
}
