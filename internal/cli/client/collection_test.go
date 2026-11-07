package client_test

import (
	"bytes"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

const collectionsPath = "/api/dustpan/v1/namespaces/acme/collections"

// TestCollectionCreate runs collection create against a stub server.
func TestCollectionCreate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    func(url string) []string
		answer  *stubAnswer
		wantOut string
		wantErr string
	}{
		{
			name: "prints the created collection",
			args: func(u string) []string { return withServer(u, "acme/everything") },
			answer: &stubAnswer{
				method: "POST", path: collectionsPath, body: `{"name":"everything"}`, status: 201,
				answer: `{"namespace":"acme","name":"everything","createdAt":1}`,
			},
			wantOut: "created collection acme/everything\n",
			wantErr: "",
		},
		{
			name:    "missing operand names the argument",
			args:    func(u string) []string { return withServer(u) },
			answer:  nil,
			wantOut: "",
			wantErr: "needs one argument <namespace>/<name>",
		},
		{
			name:    "operand without a slash names the form",
			args:    func(u string) []string { return withServer(u, "acme") },
			answer:  nil,
			wantOut: "",
			wantErr: "needs <namespace>/<name>",
		},
		{
			name:    "operand with an empty name names the form",
			args:    func(u string) []string { return withServer(u, "acme/") },
			answer:  nil,
			wantOut: "",
			wantErr: "needs <namespace>/<name>",
		},
		{
			name:    "missing server names the flag",
			args:    func(string) []string { return []string{"acme/everything"} },
			answer:  nil,
			wantOut: "",
			wantErr: "needs --server",
		},
		{
			name: "server refusal passes through",
			args: func(u string) []string { return withServer(u, "acme/everything") },
			answer: &stubAnswer{
				method: "POST", path: collectionsPath, body: `{"name":"everything"}`, status: 409,
				answer: `{"code":409,"message":"collection exists"}`,
			},
			wantOut: "",
			wantErr: "409 Conflict: collection exists",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := serveStub(t, tc.answer)

			var stdout, stderr bytes.Buffer

			err := client.CollectionCreateWithLookup(
				t.Context(), tc.args(srv.URL), tokenLookup, &stdout, &stderr,
			)
			checkResult(t, stdout.String(), err, tc.wantOut, tc.wantErr)
		})
	}
}

// TestCollectionList runs collection list against a stub server.
func TestCollectionList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    func(url string) []string
		answer  *stubAnswer
		wantOut string
		wantErr string
	}{
		{
			name: "prints one line for each collection",
			args: func(u string) []string { return withServer(u, "acme") },
			answer: &stubAnswer{
				method: "GET", path: collectionsPath, body: "", status: 200,
				answer: `{"collections":[{"namespace":"acme","name":"everything","createdAt":1},` +
					`{"namespace":"acme","name":"web","createdAt":2}]}`,
			},
			wantOut: "acme/everything\nacme/web\n",
			wantErr: "",
		},
		{
			name: "no collections prints nothing",
			args: func(u string) []string { return withServer(u, "acme") },
			answer: &stubAnswer{
				method: "GET", path: collectionsPath, body: "", status: 200, answer: `{"collections":[]}`,
			},
			wantOut: "",
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
			name:    "missing server names the flag",
			args:    func(string) []string { return []string{"acme"} },
			answer:  nil,
			wantOut: "",
			wantErr: "needs --server",
		},
		{
			name: "unknown namespace passes through",
			args: func(u string) []string { return withServer(u, "acme") },
			answer: &stubAnswer{
				method: "GET", path: collectionsPath, body: "", status: 404,
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

			err := client.CollectionListWithLookup(t.Context(), tc.args(srv.URL), tokenLookup, &stdout, &stderr)
			checkResult(t, stdout.String(), err, tc.wantOut, tc.wantErr)
		})
	}
}
