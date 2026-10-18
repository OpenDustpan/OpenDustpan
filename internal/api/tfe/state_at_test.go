package tfe_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// stateAtTestData decodes the state version of the state-at document in tests.
type stateAtTestData struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// stateAtTestMeta decodes the meta of the state-at document in tests.
type stateAtTestMeta struct {
	RequestedBy string `json:"requested-by"` //nolint:tagliatelle // The wire name of this API.
	NextCursor  string `json:"next-cursor"`  //nolint:tagliatelle // The wire name of this API.
	FinalizedAt int64  `json:"finalized-at"` //nolint:tagliatelle // The wire name of this API.
	Opaque      bool   `json:"opaque"`
	Incomplete  bool   `json:"incomplete"`
}

// stateAtTestDoc decodes the state-at document in tests.
type stateAtTestDoc struct {
	Data     stateAtTestData    `json:"data"`
	Included []resourceTestItem `json:"included"`
	Meta     stateAtTestMeta    `json:"meta"`
}

// stateAtWant is what a state-at request must answer.
type stateAtWant struct {
	id       string
	first    string
	finalize int64
	count    int
	opaque   bool
}

// checkStateAtDoc proves one state-at document against want.
func checkStateAtDoc(t *testing.T, doc stateAtTestDoc, want stateAtWant) {
	t.Helper()

	if doc.Data.ID != want.id || doc.Data.Type != "state-versions" {
		t.Errorf("data = %+v, want %s of type state-versions", doc.Data, want.id)
	}

	wantMeta := stateAtTestMeta{
		RequestedBy: "alice",
		NextCursor:  "",
		FinalizedAt: want.finalize,
		Opaque:      want.opaque,
		Incomplete:  false,
	}
	if doc.Meta != wantMeta {
		t.Errorf("meta = %+v, want %+v", doc.Meta, wantMeta)
	}

	if len(doc.Included) != want.count {
		t.Fatalf("included = %+v, want %d resources", doc.Included, want.count)
	}

	if want.count > 0 && doc.Included[0].ID != want.first {
		t.Errorf("first resource = %q, want %q", doc.Included[0].ID, want.first)
	}
}

// TestStateAtAnswersTheStateVersionOfATime proves the state-at route picks the
// state version of the time, with its finalize time and its actor, and masks
// each secret value.
func TestStateAtAnswersTheStateVersionOfATime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target string
		want   stateAtWant
	}{
		{
			name: "at the first finalize", target: "/state-at/1000",
			want: stateAtWant{id: "sv-test123", first: "terraform_data.smoke", finalize: 1000, count: 2, opaque: false},
		},
		{
			name: "between two versions", target: "/state-at/1999",
			want: stateAtWant{id: "sv-test123", first: "terraform_data.smoke", finalize: 1000, count: 2, opaque: false},
		},
		{
			name:   "at the second finalize",
			target: "/state-at/2000",
			want: stateAtWant{
				id:       "sv-second123",
				first:    "terraform_data.smoke",
				finalize: 2000,
				count:    3,
				opaque:   false,
			},
		},
		{
			name: "a state the client encrypted", target: "/state-at/3000",
			want: stateAtWant{id: "sv-encrypted123", first: "", finalize: 3000, count: 0, opaque: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := resourceBody(t, "/api/v2/workspaces/ws-test123"+tc.target, http.StatusOK)
			checkNoSecret(t, body)

			var doc stateAtTestDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatalf("decode state-at document: %v", err)
			}

			checkStateAtDoc(t, doc, tc.want)
		})
	}
}

// TestStateAtFilterAndPaging proves the route takes the filter and the page
// size of the masked resource list.
func TestStateAtFilterAndPaging(t *testing.T) {
	t.Parallel()

	var doc stateAtTestDoc

	body := resourceBody(t, "/api/v2/workspaces/ws-test123/state-at/2000?filter[q]=random&page[size]=1", http.StatusOK)
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode state-at document: %v", err)
	}

	if len(doc.Included) != 1 || doc.Included[0].Attributes.Type != "random_password" {
		t.Fatalf("included = %+v, want the one random_password resource", doc.Included)
	}

	resourceBody(t, "/api/v2/workspaces/ws-test123/state-at/2000?page[size]=0", http.StatusUnprocessableEntity)
}

// TestStateAtErrors proves the exact error documents of the route: 404 for a
// time before the first state version, with the text that names the first
// one, and 422 for a time that is not Unix milliseconds.
func TestStateAtErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target string
		status int
		title  string
		detail string
	}{
		{
			name:   "before the first state version",
			target: "/state-at/999",
			status: http.StatusNotFound,
			title:  "not found",
			detail: "workspace has no state at that time: the first state version of acme/web is at 1970-01-01T00:00:01Z (1000 ms)",
		},
		{
			name: "a time that is not a number", target: "/state-at/yesterday", status: http.StatusUnprocessableEntity,
			title:  "unprocessable",
			detail: `invalid state version: time "yesterday" is not Unix milliseconds`,
		},
		{
			name: "a negative time", target: "/state-at/-1", status: http.StatusUnprocessableEntity,
			title:  "unprocessable",
			detail: `invalid state version: time "-1" is not Unix milliseconds`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := resourceResponse(t, "/api/v2/workspaces/ws-test123"+tc.target, tc.status)

			want := `{"errors":[{"status":"` + strconv.Itoa(tc.status) +
				`","title":"` + tc.title + `","detail":` + quote(tc.detail) + `}]}`
			if got := strings.TrimSpace(rec.Body.String()); got != want {
				t.Fatalf("error body = %s, want %s", got, want)
			}
		})
	}
}

func quote(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return ""
	}

	return string(raw)
}
