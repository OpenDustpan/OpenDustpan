package jobs_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

type listedID struct {
	ID string `json:"id"`
}

type listAnswer struct {
	Jobs          []listedID `json:"jobs"`
	NextPageToken string     `json:"nextPageToken"`
	ErrorCode     string     `json:"errorCode"`
}

// listedJob returns a job of the unit unitID in the namespace ns.
func listedJob(t *testing.T, ns namespace.Name, id, unitID string) job.Job {
	t.Helper()

	j := runningJob(t, job.ID(id))
	o, err := identity.UnitObject("opentofu", ns, "net-"+unitID)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	j.Spec.Unit = o.WithUnitID(unitID)

	return j
}

func listHandler(t *testing.T, holds []string, rows ...job.Job) testHandler {
	t.Helper()

	jobs := newFakeJobs()
	jobs.listed = func(f job.Filter) []job.Job {
		out := []job.Job{}
		skip := f.After != ""

		for i := range rows {
			if skip {
				skip = rows[i].ID != f.After

				continue
			}

			if (f.UnitID == "" || rows[i].Spec.Unit.UnitID() == f.UnitID) &&
				(len(f.States) == 0 || slices.Contains(f.States, rows[i].State)) {
				out = append(out, rows[i])
			}
		}

		return out
	}

	return newHandler(t, newAuthorizer(holds...), jobs)
}

func getList(t *testing.T, h http.Handler, query string) (int, listAnswer) {
	t.Helper()

	rec := serve(t, h, request{
		method: http.MethodGet, path: "/api/dustpan/v1/namespaces/acme/jobs" + query, token: testToken,
	})

	var out listAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}

	return rec.Code, out
}

func ids(a listAnswer) []string {
	out := make([]string, 0, len(a.Jobs))
	for _, j := range a.Jobs {
		out = append(out, j.ID)
	}

	return out
}

func readsUnits(units ...string) []string {
	out := readsNamespace()
	for _, u := range units {
		out = append(out, string(identity.ActionJobRead)+" opentofu:acme/net-"+u)
	}

	return out
}

func TestListJobsFilters(t *testing.T) {
	t.Parallel()

	a := listedJob(t, "acme", "job-aaaaaaaaaaaaaaaaaaaa", "unt-a")
	b := listedJob(t, "acme", "job-bbbbbbbbbbbbbbbbbbbb", "unt-b")
	b.State = job.StateFailed
	b.Spec.Purpose = job.PurposeDriftCheck
	c := listedJob(t, "acme", "job-cccccccccccccccccccc", "unt-a")
	c.Trigger = &job.TriggerRef{ID: "trg-1", Saver: "ada"}
	// The caller may not read the unit of d: the list leaves it out.
	d := listedJob(t, "acme", "job-dddddddddddddddddddd", "unt-d")
	// A row of another namespace, as a faulty store could give it.
	e := listedJob(t, "other", "job-eeeeeeeeeeeeeeeeeeee", "unt-a")

	h := listHandler(t, readsUnits("unt-a", "unt-b"), a, b, c, d, e)

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"all readable, in store order", "", []string{string(a.ID), string(b.ID), string(c.ID)}},
		{"unit", "?unit=unt-a", []string{string(a.ID), string(c.ID)}},
		{"status", "?status=failed", []string{string(b.ID)}},
		{"two statuses", "?status=failed&status=running", []string{string(a.ID), string(b.ID), string(c.ID)}},
		{"purpose", "?purpose=drift-check", []string{string(b.ID)}},
		{"trigger", "?trigger=trg-1", []string{string(c.ID)}},
		{"filters together", "?unit=unt-a&trigger=trg-1&status=running", []string{string(c.ID)}},
		{"no match", "?trigger=trg-9", []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code, got := getList(t, h, tc.query)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}

			if !slices.Equal(ids(got), tc.want) {
				t.Errorf("jobs = %v, want %v", ids(got), tc.want)
			}
		})
	}
}

func TestListJobsStatusFilterReachesTheStore(t *testing.T) {
	t.Parallel()

	var seen job.Filter

	jobs := newFakeJobs()
	jobs.listed = func(f job.Filter) []job.Job {
		seen = f

		return nil
	}

	h := newHandler(t, newAuthorizer(readsNamespace()...), jobs)
	if code, _ := getList(t, h, "?status=failed&unit=unt-a"); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if seen.Namespace != "acme" || seen.UnitID != "unt-a" || !slices.Equal(seen.States, []job.State{job.StateFailed}) {
		t.Errorf("filter = %+v", seen)
	}
}

func TestListJobsPages(t *testing.T) {
	t.Parallel()

	rows := []job.Job{
		listedJob(t, "acme", "job-aaaaaaaaaaaaaaaaaaaa", "unt-a"),
		listedJob(t, "acme", "job-bbbbbbbbbbbbbbbbbbbb", "unt-a"),
		listedJob(t, "acme", "job-cccccccccccccccccccc", "unt-a"),
	}
	h := listHandler(t, readsUnits("unt-a"), rows...)

	_, first := getList(t, h, "?pageSize=2")
	if !slices.Equal(ids(first), []string{"job-aaaaaaaaaaaaaaaaaaaa", "job-bbbbbbbbbbbbbbbbbbbb"}) ||
		first.NextPageToken == "" {
		t.Fatalf("first page = %v token %q", ids(first), first.NextPageToken)
	}

	_, second := getList(t, h, "?pageSize=2&pageToken="+url.QueryEscape(first.NextPageToken))
	if !slices.Equal(ids(second), []string{"job-cccccccccccccccccccc"}) || second.NextPageToken != "" {
		t.Fatalf("second page = %v token %q", ids(second), second.NextPageToken)
	}

	t.Run("tampered token", func(t *testing.T) {
		t.Parallel()

		tampered := first.NextPageToken[:len(first.NextPageToken)-3] + "AAA"
		code, got := getList(t, h, "?pageToken="+url.QueryEscape(tampered))

		if code != http.StatusBadRequest || got.ErrorCode != errcode.RequestPageTokenInvalid {
			t.Errorf("tampered token = %d %q", code, got.ErrorCode)
		}
	})

	t.Run("token of another query", func(t *testing.T) {
		t.Parallel()

		code, got := getList(t, h, "?status=failed&pageToken="+url.QueryEscape(first.NextPageToken))
		if code != http.StatusBadRequest || got.ErrorCode != errcode.RequestPageTokenInvalid {
			t.Errorf("token of another query = %d %q", code, got.ErrorCode)
		}
	})
}

func TestListJobsRefusesBadQuery(t *testing.T) {
	t.Parallel()

	h := listHandler(t, readsUnits("unt-a"))

	for _, query := range []string{
		"?status=bogus", "?purpose=other", "?pageSize=0", "?pageSize=201", "?pageSize=x", "?unit=../x",
		"?trigger=a%20b", "?pageToken=!",
	} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			code, got := getList(t, h, query)
			if code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", code)
			}

			if got.ErrorCode != errcode.RequestInvalidQuery && got.ErrorCode != errcode.RequestPageTokenInvalid {
				t.Errorf("errorCode = %q", got.ErrorCode)
			}
		})
	}
}

func TestListJobsNeedsTheNamespace(t *testing.T) {
	t.Parallel()

	h := listHandler(t, nil)
	if code, got := getList(t, h, ""); code != http.StatusNotFound || got.ErrorCode != errcode.JobNotFound {
		t.Errorf("list without namespace:read = %d %q, want 404 job.not-found", code, got.ErrorCode)
	}
}
