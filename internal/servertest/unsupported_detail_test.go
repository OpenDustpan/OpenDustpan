package servertest_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/servertest"
)

// TestDriftCheckWithoutCodeLocationAnswersTheDetail pins the answer of a drift
// check on a unit with no code location. The message is the text that the job
// service wraps around its sentinel, so a change of that wrap format fails here.
func TestDriftCheckWithoutCodeLocationAnswersTheDetail(t *testing.T) {
	t.Parallel()

	c := servertest.Start(t, 1)
	send(t, c, request{
		server: 0,
		method: http.MethodPost,
		path:   "/api/server/namespaces",
		body:   `{"name":"acme"}`,
		want:   http.StatusCreated,
	})

	unit := "/api/dustpan/v1/namespaces/acme/units/" + workspaceID(t, c)

	status, body := c.Do(t, 0, http.MethodPost, unit+"/drift-checks", `{}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %s, want %d", status, body, http.StatusUnprocessableEntity)
	}

	var got struct {
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("answer %s is not JSON: %v", body, err)
	}

	const wantMessage = "the unit has no code location; name one in the request"
	if got.ErrorCode != "job.unsupported" || got.Message != wantMessage {
		t.Errorf("answer = %q %q, want %q %q", got.ErrorCode, got.Message, "job.unsupported", wantMessage)
	}
}

// workspaceID creates an OpenTofu workspace named web in the organization acme
// on server 0 and returns its ID.
func workspaceID(t *testing.T, c *servertest.Cluster) string {
	t.Helper()

	status, body := c.Do(t, 0, http.MethodPost, "/api/v2/organizations/acme/workspaces",
		`{"data":{"type":"workspaces","attributes":{"name":"web"}}}`)
	if status != http.StatusCreated {
		t.Fatalf("create workspace = %d %s, want %d", status, body, http.StatusCreated)
	}

	var created workspaceAnswer
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.Data.ID == "" {
		t.Fatalf("workspace answer %s has no id: %v", body, err)
	}

	return created.Data.ID
}

// workspaceAnswer is the JSON:API document that answers a created workspace.
type workspaceAnswer struct {
	Data workspaceData `json:"data"`
}

// workspaceData is the resource of a workspace in workspaceAnswer.
type workspaceData struct {
	ID string `json:"id"`
}
