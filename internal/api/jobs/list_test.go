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
	Damaged       int        `json:"damaged"`
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
	jobs.listed = func(f job.Filter) job.Page {
		var out job.Page

		for i := afterIndex(rows, f.After); i < len(rows); i++ {
			if !selected(&rows[i], f) {
				continue
			}

			if f.Limit > 0 && len(out.Jobs) == f.Limit {
				out.After = out.Jobs[len(out.Jobs)-1].ID

				break
			}

			out.Jobs = append(out.Jobs, rows[i])
		}

		return out
	}

	return newHandler(t, newAuthorizer(holds...), jobs)
}

// afterIndex returns the index of the first row after the row with the ID
// after, and 0 when after is empty or names no row.
func afterIndex(rows []job.Job, after job.ID) int {
	if after == "" {
		return 0
	}

	for i := range rows {
		if rows[i].ID == after {
			return i + 1
		}
	}

	return 0
}

// selected reports whether the row matches the unit and the states of the
// filter.
func selected(row *job.Job, f job.Filter) bool {
	if f.UnitID != "" && row.Spec.Unit.UnitID() != f.UnitID {
		return false
	}

	return len(f.States) == 0 || slices.Contains(f.States, row.State)
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
	jobs.listed = func(f job.Filter) job.Page {
		seen = f

		return job.Page{Jobs: nil, Damaged: nil, After: ""}
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

// TestListJobsCountsDamagedRows proves that the list answers the jobs that
// read and the number of rows that did not decode, without the reason of a
// row.
func TestListJobsCountsDamagedRows(t *testing.T) {
	t.Parallel()

	good := listedJob(t, "acme", "job-aaaaaaaaaaaaaaaaaaaa", "unt-a")

	jobs := newFakeJobs()
	jobs.listed = func(job.Filter) job.Page {
		return job.Page{
			Jobs: []job.Job{good},
			Damaged: []job.DamagedRow{
				{ID: "job-bbbbbbbbbbbbbbbbbbbb", Reason: "the body is not JSON"},
				{ID: "job-cccccccccccccccccccc", Reason: "the body names an unknown source"},
			},
			After: "",
		}
	}

	h := newHandler(t, newAuthorizer(readsUnits("unt-a")...), jobs)

	code, got := getList(t, h, "")
	if code != http.StatusOK || !slices.Equal(ids(got), []string{string(good.ID)}) || got.Damaged != 2 {
		t.Fatalf("page = %d %v with %d damaged rows, want the good job and 2", code, ids(got), got.Damaged)
	}
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
