package jobs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// defaultPageSize and maxPageSize are the default and the largest page
	// size of the job list.
	defaultPageSize = 50
	maxPageSize     = 200
	// scanBatch is the number of jobs that one read of the store gives.
	scanBatch = 100
	// maxScanned is the largest number of jobs that one request reads from the
	// store. A request that reaches it answers the page it has, with a page
	// token that continues after the last job that it read, so one request
	// never reads the whole table.
	maxScanned = 2000
	// maxUnitIDLength bounds the unit and trigger values of the query.
	maxUnitIDLength = 128
)

var (
	errInvalidQuery = errors.New("invalid query parameter")
	errPageToken    = fmt.Errorf(
		"%w: pageToken does not belong to this query: start again without it", errInvalidQuery,
	)
)

// listJobsResponse is the page of jobs that the list answers. The jobs are
// newest first.
type listJobsResponse struct {
	Jobs          []jobResponse `json:"jobs"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
}

// pageTokenWire is the opaque page token of the job list: the ID of the last
// job that the page before read, and a fingerprint of the query.
type pageTokenWire struct {
	After       string `json:"j"`
	Fingerprint string `json:"f"`
}

// listQuery is the filter and the page of a job list.
type listQuery struct {
	unit    string
	states  []job.State
	purpose job.Purpose
	trigger string
	size    int
	after   job.ID
}

// parseListQuery reads and checks the query of a job list in the namespace ns.
// It refuses a value that is not valid, and never changes it to make it
// valid.
func parseListQuery(r *http.Request, ns namespace.Name) (listQuery, error) {
	query := queryOf(r)
	if err := query.Err(); err != nil {
		return listQuery{}, fmt.Errorf("%w: the query cannot be read", errInvalidQuery)
	}

	q := listQuery{
		unit: query.Get("unit"), states: nil, purpose: job.PurposeNone, trigger: query.Get("trigger"),
		size: defaultPageSize, after: "",
	}

	if !validOptionalID(q.unit) || !validOptionalID(q.trigger) {
		return listQuery{}, fmt.Errorf("%w: unit and trigger must be IDs", errInvalidQuery)
	}

	var err error
	if q.states, err = parseStates(query.All("status")); err != nil {
		return listQuery{}, err
	}

	if q.purpose, err = parsePurpose(query.Get("purpose")); err != nil {
		return listQuery{}, err
	}

	if q.size, err = parsePageSize(query.Get("pageSize")); err != nil {
		return listQuery{}, err
	}

	if raw := query.Get("pageToken"); raw != "" {
		if q.after, err = parsePageToken(raw, q.fingerprint(ns)); err != nil {
			return listQuery{}, err
		}
	}

	return q, nil
}

// validOptionalID reports whether s is empty or an ID.
func validOptionalID(s string) bool {
	return s == "" || ident.Valid(s, maxUnitIDLength)
}

// parseStates reads the status values. A value that is no state is refused.
func parseStates(values []string) ([]job.State, error) {
	var states []job.State

	for _, raw := range values {
		state := job.State(raw)
		if !slices.Contains(job.States(), state) {
			return nil, fmt.Errorf("%w: status %q is not a state of a job", errInvalidQuery, raw)
		}

		if !slices.Contains(states, state) {
			states = append(states, state)
		}
	}

	return states, nil
}

// parsePurpose reads the purpose value: empty, or a purpose that a job can
// have besides none.
func parsePurpose(raw string) (job.Purpose, error) {
	purpose := job.Purpose(raw)
	if raw != "" && (purpose == job.PurposeNone || !purpose.Valid()) {
		return job.PurposeNone, fmt.Errorf("%w: purpose must be %q", errInvalidQuery, job.PurposeDriftCheck)
	}

	return purpose, nil
}

// parsePageSize reads the pageSize value.
func parsePageSize(raw string) (int, error) {
	if raw == "" {
		return defaultPageSize, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxPageSize {
		return 0, fmt.Errorf("%w: pageSize must be a number from 1 to %d", errInvalidQuery, maxPageSize)
	}

	return n, nil
}

// parsePageToken returns the job ID after which the page of the token starts.
// A token that does not decode, that holds no job ID, or that another query
// made, gives errPageToken.
func parsePageToken(raw, fingerprint string) (job.ID, error) {
	var wire pageTokenWire
	if err := pagetoken.Decode(raw, &wire); err != nil {
		return "", errPageToken
	}

	if !strings.HasPrefix(wire.After, job.IDPrefix) || !ident.Valid(wire.After, maxIDLength) ||
		!pagetoken.SameFingerprint(wire.Fingerprint, fingerprint) {
		return "", errPageToken
	}

	return job.ID(wire.After), nil
}

// fingerprint returns a short digest of the namespace and the filters, so a
// page token works only for the query that made it. The page size is not part
// of it: a client may change the size between pages.
func (q listQuery) fingerprint(ns namespace.Name) string {
	states := make([]string, 0, len(q.states))
	for _, s := range q.states {
		states = append(states, string(s))
	}

	slices.Sort(states)

	return pagetoken.Fingerprint(strings.Join([]string{
		"ns=" + ns.String(), "unit=" + q.unit, "status=" + strings.Join(states, ","),
		"purpose=" + string(q.purpose), "trigger=" + q.trigger,
	}, "\n"))
}

// matches reports whether j has the purpose and the trigger of the query. The
// store filters the namespace, the unit and the states.
func (q listQuery) matches(j *job.Job, ns namespace.Name) bool {
	switch {
	case j.Spec.Unit.Namespace() != ns, !j.Spec.Unit.IsUnit():
		// Defense in depth: the store filters the namespace too. A job of
		// another namespace never joins a page.
		return false
	case q.unit != "" && j.Spec.Unit.UnitID() != q.unit:
		return false
	case q.purpose != job.PurposeNone && j.Spec.Purpose != q.purpose:
		return false
	case q.trigger != "" && (j.Trigger == nil || j.Trigger.ID != q.trigger):
		return false
	default:
		return true
	}
}

// listJobs answers GET /api/dustpan/v1/namespaces/{namespace}/jobs with a page
// of the jobs that the caller may read, newest first. The route wrapper
// allowed namespace:read. The handler checks job:read on the unit of each job
// before the job joins the page, and a job that the caller may not read is
// not in the page: the list tells nothing about it.
func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	q, err := parseListQuery(r, t.ns.Name)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	page, next, err := h.scanJobs(ctx, p, t.ns.Name, &q)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.answerJobList(w, r, &page, next, q.fingerprint(t.ns.Name))
}

// scanJobs reads the store until the page holds q.size jobs and one more, or
// the list ends, or maxScanned jobs are read. It returns the page and the job
// ID after which the next page starts, or "" at the end of the list.
func (h *Handler) scanJobs(
	ctx context.Context, p identity.Principal, ns namespace.Name, q *listQuery,
) ([]job.Job, job.ID, error) {
	page := make([]job.Job, 0, q.size+1)
	filter := job.Filter{
		Namespace: ns, UnitID: q.unit, States: q.states, Limit: scanBatch, After: q.after,
		StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
	}

	for scanned := 0; scanned < maxScanned; {
		rows, err := h.jobs.List(ctx, filter)
		if err != nil {
			return nil, "", fmt.Errorf("list jobs: %w", err)
		}

		for i := range rows {
			scanned++
			filter.After = rows[i].ID

			if !q.matches(&rows[i], ns) || h.authz.Authorize(ctx, p, identity.ActionJobRead, rows[i].Spec.Unit) != nil {
				continue
			}

			page = append(page, rows[i])
			if len(page) > q.size {
				// More matches follow: the next page starts after the last job
				// of this page.
				return page[:q.size], page[q.size-1].ID, nil
			}
		}

		if len(rows) < scanBatch {
			return page, "", nil
		}
	}

	// The scan limit ended the request before the end of the list.
	return page, filter.After, nil
}

// answerJobList writes the page. It names the pool of each job once for each
// pool ID.
func (h *Handler) answerJobList(w http.ResponseWriter, r *http.Request, page *[]job.Job, next job.ID, fp string) {
	ctx := r.Context()
	out := listJobsResponse{Jobs: make([]jobResponse, 0, len(*page)), NextPageToken: ""}
	names := map[string]string{}

	for i := range *page {
		j := &(*page)[i]
		doc := toJobResponse(j)

		if j.Spec.Pool != "" {
			name, seen := names[j.Spec.Pool]
			if !seen {
				var err error

				if name, err = h.poolName(ctx, j); err != nil {
					h.writeError(ctx, w, err)

					return
				}

				names[j.Spec.Pool] = name
			}

			doc.Pool = name
		}

		out.Jobs = append(out.Jobs, doc)
	}

	if next != "" {
		token, err := pagetoken.Encode(pageTokenWire{After: string(next), Fingerprint: fp})
		if err != nil {
			h.writeError(ctx, w, fmt.Errorf("encode page token: %w", err))

			return
		}

		out.NextPageToken = token
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// queryOf returns the query of a request, as the label of its route allows
// the handler to read it.
func queryOf(r *http.Request) apidesc.Query {
	return apidesc.QueryOf(r, operationLabels)
}
