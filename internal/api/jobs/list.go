package jobs

import (
	"context"
	"encoding/base64"
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
	// errUnitUnresolved reports a damaged row whose unit the handler cannot
	// resolve. The list does not count the row.
	errUnitUnresolved = errors.New("the unit of a damaged row cannot be resolved")
)

// listJobsResponse is the page of jobs that the list answers. The jobs are
// newest first.
type listJobsResponse struct {
	Jobs          []jobResponse `json:"jobs"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
	// Damaged is the number of stored rows that this page read and could not
	// decode, of units that the caller may list. The server leaves them out
	// of Jobs and never dispatches, expires or closes them. A row that sits
	// between the jobs of two pages can be in the count of both.
	Damaged int `json:"damaged"`
}

// pageTokenWire is the opaque page token of the job list: the ID of the last
// job that the page before read, and a fingerprint of the query.
type pageTokenWire struct {
	After       string `json:"j"`
	Fingerprint string `json:"f"`
}

// listQuery is the filter and the page of a job list.
type listQuery struct {
	// cursor is the cursor of the page token, as the token holds it. The
	// handler opens it with openCursor.
	cursor  string
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
		size: defaultPageSize, after: "", cursor: "",
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
		if q.cursor, err = parsePageToken(raw, q.fingerprint(ns)); err != nil {
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

// parsePageToken returns the cursor of a token. A token that does not decode,
// that holds no cursor, or that another query made, gives errPageToken.
func parsePageToken(raw, fingerprint string) (string, error) {
	var wire pageTokenWire
	if err := pagetoken.Decode(raw, &wire); err != nil || wire.After == "" ||
		!pagetoken.SameFingerprint(wire.Fingerprint, fingerprint) {
		return "", errPageToken
	}

	return wire.After, nil
}

// cursorAAD binds a sealed cursor to the namespace and the query, so a cursor
// works only in the token of the query that made it.
func cursorAAD(ns namespace.Name, fingerprint string) []byte {
	return []byte("job-list-cursor\n" + ns.String() + "\n" + fingerprint)
}

// sealCursor returns the cursor of a token for the job ID after. With a
// sealer, the cursor is the sealed ID, which names no job to the caller.
func (h *Handler) sealCursor(ctx context.Context, ns namespace.Name, fingerprint string, after job.ID) (string, error) {
	if h.cursors == nil {
		return string(after), nil
	}

	sealed, err := h.cursors.Wrap(ctx, []byte(after), cursorAAD(ns, fingerprint))
	if err != nil {
		return "", fmt.Errorf("seal page cursor: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openCursor returns the job ID that a cursor holds. A cursor that does not
// open, or that does not hold a job ID, gives errPageToken.
func (h *Handler) openCursor(ctx context.Context, ns namespace.Name, fingerprint, cursor string) (job.ID, error) {
	id := cursor

	if h.cursors != nil {
		// Defense in depth: Unwrap refuses a sealed cursor whose bytes
		// changed; the strict decode also refuses a text that is not the
		// canonical encoding, so one cursor has one spelling only.
		sealed, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if err != nil {
			return "", errPageToken
		}

		plain, err := h.cursors.Unwrap(ctx, sealed, cursorAAD(ns, fingerprint))
		if err != nil {
			return "", errPageToken
		}

		id = string(plain)
	}

	if !strings.HasPrefix(id, job.IDPrefix) || !ident.Valid(id, maxIDLength) {
		return "", errPageToken
	}

	return job.ID(id), nil
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

	fingerprint := q.fingerprint(t.ns.Name)

	if q.cursor != "" {
		if q.after, err = h.openCursor(ctx, t.ns.Name, fingerprint, q.cursor); err != nil {
			h.writeError(ctx, w, err)

			return
		}
	}

	page := jobPage{ns: t.ns.Name, jobs: nil, next: "", damaged: 0, fingerprint: fingerprint}
	if err := h.scanJobs(ctx, p, &q, &page); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.answerJobList(w, r, &page)
}

// scanJobs reads the store until the page holds q.size jobs and one more, or
// the list ends, or maxScanned rows are read. It fills the jobs of the page,
// the job ID after which the next page starts (or "" at the end of the list),
// and the number of damaged rows of units that the caller may list.
func (h *Handler) scanJobs(ctx context.Context, p identity.Principal, q *listQuery, page *jobPage) error {
	jobs := make([]job.Job, 0, q.size+1)
	filter := job.Filter{
		Namespace: page.ns, UnitID: q.unit, States: q.states, Limit: scanBatch, After: q.after,
		StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
	}

	// Without a sealer the token can hold the ID of a job that the caller may
	// read only, so the scan has no limit: it ends at a full page or at the
	// end of the list.
	limited := h.cursors != nil

	for scanned := 0; !limited || scanned < maxScanned; {
		read, err := h.jobs.List(ctx, filter)
		if err != nil {
			return fmt.Errorf("list jobs: %w", err)
		}

		// A damaged row of a unit that the caller may not list is not
		// counted, but it is a row read too: it counts against the scan
		// limit either way.
		page.damaged += h.countDamaged(ctx, p, page.ns, read.Damaged)
		scanned += len(read.Damaged)

		for i := range read.Jobs {
			scanned++

			if !q.matches(&read.Jobs[i], page.ns) || !h.mayList(ctx, p, &read.Jobs[i]) {
				continue
			}

			jobs = append(jobs, read.Jobs[i])
			if len(jobs) > q.size {
				// More matches follow: the next page starts after the last job
				// of this page.
				page.jobs, page.next = jobs[:q.size], jobs[q.size-1].ID

				return nil
			}
		}

		if read.After == "" {
			page.jobs = jobs

			return nil
		}

		filter.After = read.After
	}

	// The scan limit ended the request before the end of the list.
	page.jobs, page.next = jobs, filter.After

	return nil
}

// mayList reports whether p passes every filter action of the list on the unit
// of j. Any error from the authorizer, not only a refusal, leaves the job out.
func (h *Handler) mayList(ctx context.Context, p identity.Principal, j *job.Job) bool {
	return h.mayListUnit(ctx, p, j.Spec.Unit)
}

// countDamaged returns the number of the damaged rows that p may list.
func (h *Handler) countDamaged(
	ctx context.Context, p identity.Principal, ns namespace.Name, rows []job.DamagedRow,
) int {
	n := 0

	for i := range rows {
		if h.mayListDamaged(ctx, p, ns, &rows[i]) {
			n++
		}
	}

	return n
}

// mayListDamaged reports whether p may list the unit of the damaged row row.
// The purpose and the trigger filters of the query cannot apply to a damaged
// row: its body did not decode. Any error, of the unit lookup or of the
// authorizer, leaves the row out of the count.
func (h *Handler) mayListDamaged(
	ctx context.Context, p identity.Principal, ns namespace.Name, row *job.DamagedRow,
) bool {
	unit, err := h.unitRef(ctx, ns, row.UnitID)
	if err != nil {
		return false
	}

	return h.mayListUnit(ctx, p, unit)
}

// mayListUnit reports whether p passes every filter action of the list on the
// unit obj. Any error from the authorizer, not only a refusal, leaves the
// unit out.
func (h *Handler) mayListUnit(ctx context.Context, p identity.Principal, obj identity.Object) bool {
	for _, action := range listFilters() {
		if h.authz.Authorize(ctx, p, action, obj) != nil {
			return false
		}
	}

	return true
}

// unitRef returns the reference of the live unit unitID of the namespace ns,
// with its stable ID. An unknown unit, a unit of another namespace and a
// failure of the inventory give an error.
func (h *Handler) unitRef(ctx context.Context, ns namespace.Name, unitID string) (identity.Object, error) {
	if h.units == nil || !ident.Valid(unitID, maxUnitIDLength) {
		return identity.Object{}, errUnitUnresolved
	}

	e, err := h.units.GetUnit(ctx, unitID)
	if err != nil {
		return identity.Object{}, fmt.Errorf("get unit: %w", err)
	}

	// Defense in depth: the inventory looks a unit up by its ID alone. A
	// unit of another namespace must not answer under this namespace,
	// whatever the caller may read there.
	if e.Namespace != ns || e.UnitID != unitID {
		return identity.Object{}, errUnitUnresolved
	}

	obj, err := identity.UnitObject(e.Tool, ns, e.Name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit object: %w", err)
	}

	return obj.WithUnitID(e.UnitID), nil
}

// jobPage is a page of the job list that is ready to answer.
type jobPage struct {
	ns          namespace.Name
	jobs        []job.Job
	next        job.ID
	damaged     int
	fingerprint string
}

// answerJobList writes the page. It names the pool of each job once for each
// pool ID.
func (h *Handler) answerJobList(w http.ResponseWriter, r *http.Request, p *jobPage) {
	ctx := r.Context()
	out := listJobsResponse{Jobs: make([]jobResponse, 0, len(p.jobs)), NextPageToken: "", Damaged: p.damaged}
	names := map[string]string{}

	for i := range p.jobs {
		j := &p.jobs[i]
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

	if p.next != "" {
		cursor, err := h.sealCursor(ctx, p.ns, p.fingerprint, p.next)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		token, err := pagetoken.Encode(pageTokenWire{After: cursor, Fingerprint: p.fingerprint})
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
