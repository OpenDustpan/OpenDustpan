package servertest_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

const jobsRoute = "/api/dustpan/v1/namespaces/acme/jobs"

// jobPoolDoc holds the members of a job document that name its pool.
type jobPoolDoc struct {
	ID        string   `json:"id"`
	Pool      string   `json:"pool"`
	PoolID    string   `json:"poolID"`
	Defaulted []string `json:"defaulted"`
}

// TestJobPoolReadBackStartsAgain proves that the pool in a job document is a
// value that a start request takes: a start with no pool names the pool by its
// name and its ID, and a second start that sends that name makes a job on the
// same pool. Only the first start took the pool by default.
func TestJobPoolReadBackStartsAgain(t *testing.T) {
	t.Parallel()

	c := clusterWithPool(t)

	first := startJob(t, c, "")
	if first.Pool != "main" || first.PoolID == "" || !slices.Equal(first.Defaulted, []string{"pool"}) {
		t.Fatalf("first job = %+v, want pool main, a pool ID and defaulted [pool]", first)
	}

	second := startJob(t, c, `"pool":"`+first.Pool+`",`)
	if second.Pool != "main" || second.PoolID != first.PoolID || len(second.Defaulted) != 0 {
		t.Errorf("second job = %+v, want pool main, pool ID %s and no defaulted", second, first.PoolID)
	}

	status, answer := c.Do(t, 0, http.MethodGet, jobsRoute+"/"+first.ID, "")
	if status != http.StatusOK {
		t.Fatalf("read = %d %s, want %d", status, answer, http.StatusOK)
	}

	var read jobPoolDoc
	if err := json.Unmarshal([]byte(answer), &read); err != nil || read.Pool != "main" || read.PoolID != first.PoolID {
		t.Errorf("read job = %+v (%v), want pool main and pool ID %s", read, err, first.PoolID)
	}
}

// startJob starts a plan job on the workspace web and returns its document.
// pool is a JSON member with a trailing comma, or empty for no pool.
func startJob(t *testing.T, c *servertest.Cluster, pool string) jobPoolDoc {
	t.Helper()

	body := `{"unit":{"tool":"opentofu","name":"web"},"operation":"plan",` + pool +
		`"location":{"repository":"acme/infra","ref":"main"}}`

	status, answer := c.Do(t, 0, http.MethodPost, jobsRoute, body)
	if status != http.StatusCreated {
		t.Fatalf("start = %d %s, want %d", status, answer, http.StatusCreated)
	}

	var doc jobPoolDoc
	if err := json.Unmarshal([]byte(answer), &doc); err != nil {
		t.Fatalf("decode %s: %v", answer, err)
	}

	return doc
}

// clusterWithPool starts a server with the namespace acme, a pool for jobs
// named main, and the workspace web.
func clusterWithPool(t *testing.T) *servertest.Cluster {
	t.Helper()

	c := servertest.Start(t, 1, dustpan.WithPublicURL("https://dustpan.example.com"))
	for _, step := range []struct{ path, body string }{
		{"/api/server/namespaces", `{"name":"acme"}`},
		{"/api/runners/acme/pools", `{"name":"main","purpose":"jobs"}`},
	} {
		if status, body := c.Do(t, 0, http.MethodPost, step.path, step.body); status != http.StatusCreated {
			t.Fatalf("POST %s = %d %s, want %d", step.path, status, body, http.StatusCreated)
		}
	}

	seedPlacement(t, c, 0, `{"name":"acme"}`)
	workspaceID(t, c)

	return c
}
