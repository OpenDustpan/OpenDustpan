package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// Paths of the access steps. The runner route lists the runner pools of a
// namespace and the jobs route reads one job. Both authorize on the
// namespace, so the IP allowlist of the namespace is on their path.
const (
	runnerPoolsPath = "/api/runners/"
	runnerPoolsTail = "/pools"
	jobsPathHead    = "/api/dustpan/v1/namespaces/"
	jobsPathMid     = "/jobs/"
)

// accessWarmup is the request count before each timing, so connection setup
// and first-use costs stay out of the median.
const accessWarmup = 10

// allowlistPrefixes answers the list the harness puts on the list namespace:
// the address ranges a team may list for its networks, with the loopback range
// of the harness client last, so the check reads the whole list before it
// allows the request. It holds 16 entries.
func allowlistPrefixes() []string {
	return []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10",
		"203.0.113.0/24", "198.51.100.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"2001:db8::/32", "fd00::/8", "10.1.0.0/16", "10.2.0.0/16",
		"172.20.0.0/16", "192.168.64.0/24", "169.254.0.0/16", "127.0.0.0/8",
	}
}

// refusingPrefixes answers a list that does not hold the harness client.
func refusingPrefixes() []string {
	return []string{"203.0.113.0/24", "198.51.100.0/24"}
}

// errBadJournalEntries reports a journal batch entry count that is not a
// positive even number: each resource is a begin and a success entry.
var errBadJournalEntries = errors.New("journal entries per batch must be an even number, at least 2")

// routeTiming is one GET timed requests times: the median latency with the
// status of the last answer.
type routeTiming struct {
	p50    float64
	status int
}

// timeGet sends GET url requests times after a warm-up and answers the median
// latency with the last status. A status other than want, or one of the two
// refusals when refused is set, ends the run.
func (t *target) timeGet(ctx context.Context, url string, requests int, want []int) (routeTiming, error) {
	samples := make([]float64, 0, requests)
	last := 0

	for i := range accessWarmup + requests {
		start := time.Now()

		resp, err := t.do(ctx, request{
			method: http.MethodGet, url: url, auth: tfeScheme + t.token, mediaType: "", body: nil,
		})
		took := time.Since(start)

		if err != nil {
			return routeTiming{}, err
		}

		if !statusIn(resp.status, want) {
			return routeTiming{}, check(http.MethodGet, url, resp.status, want[0], resp.body)
		}

		last = resp.status

		if i >= accessWarmup {
			samples = append(samples, ms(took))
		}
	}

	return routeTiming{p50: p50(samples), status: last}, nil
}

// statusIn reports whether status is one of want.
func statusIn(status int, want []int) bool {
	return slices.Contains(want, status)
}

// accessPaths holds the two paths of one namespace.
type accessPaths struct {
	runner string
	jobs   string
}

// accessPathsOf answers the runner route and the jobs route of namespace ns
// with job id.
func accessPathsOf(ns, id string) accessPaths {
	return accessPaths{
		runner: runnerPoolsPath + ns + runnerPoolsTail,
		jobs:   jobsPathHead + ns + jobsPathMid + id,
	}
}

// refusedStatuses answers the statuses of a route to a request that the list
// refuses: the namespace read check answers 404, and the IP allowlist check
// answers 403 on the routes that main now guards with it.
func refusedStatuses() []int {
	return []int{http.StatusNotFound, http.StatusForbidden}
}

// routeFn times one route on one server and answers its median and status.
type routeFn func(base, path string, want []int) (routeTiming, error)

// measureAccess times the runner route and the jobs route on the server
// without IP allowlists, and on the server with them with no list on the
// namespace, with a list that holds the client, and with a list that refuses
// it. The refused requests go to the deny namespace, so the harness client is
// never locked out of the namespace it measures in.
func (t *target) measureAccess(ctx context.Context, requests int) (AccessResult, error) {
	srv := t.served
	run := func(base, path string, want []int) (routeTiming, error) {
		return t.timeGet(ctx, base+path, requests, want)
	}

	ok := []int{http.StatusOK}

	off, err := t.timeBoth(run, t.urls[0], accessPathsOf(t.ns, srv.jobID), ok)
	if err != nil {
		return AccessResult{}, err
	}

	noList, err := t.timeBoth(run, srv.gateURL, accessPathsOf(t.ns, srv.jobID), ok)
	if err != nil {
		return AccessResult{}, err
	}

	listed, err := t.timeBoth(run, srv.gateURL, accessPathsOf(srv.listNS, srv.listJobID), ok)
	if err != nil {
		return AccessResult{}, err
	}

	refused, err := t.timeBoth(run, srv.gateURL, accessPathsOf(srv.denyNS, srv.denyJobID), refusedStatuses())
	if err != nil {
		return AccessResult{}, err
	}

	return AccessResult{
		Requests:    requests,
		ListEntries: len(allowlistPrefixes()),
		RunnerRoute: accessRouteOf(runnerPoolsPath+"{namespace}"+runnerPoolsTail, routeStates{
			off: off.runner, noList: noList.runner, listed: listed.runner, refused: refused.runner,
		}),
		JobsRoute: accessRouteOf(jobsPathHead+"{namespace}"+jobsPathMid+"{job_id}", routeStates{
			off: off.jobs, noList: noList.jobs, listed: listed.jobs, refused: refused.jobs,
		}),
	}, nil
}

// bothTimings holds the timing of the runner route and of the jobs route.
type bothTimings struct {
	runner routeTiming
	jobs   routeTiming
}

// timeBoth times the runner route and the jobs route at base.
func (t *target) timeBoth(run routeFn, base string, paths accessPaths, want []int) (bothTimings, error) {
	runnerTiming, err := run(base, paths.runner, want)
	if err != nil {
		return bothTimings{}, err
	}

	jobsTiming, err := run(base, paths.jobs, want)
	if err != nil {
		return bothTimings{}, err
	}

	return bothTimings{runner: runnerTiming, jobs: jobsTiming}, nil
}

// routeStates holds the four timings of one route.
type routeStates struct {
	off     routeTiming
	noList  routeTiming
	listed  routeTiming
	refused routeTiming
}

// accessRouteOf joins the four timings of the GET route at path.
func accessRouteOf(path string, states routeStates) AccessRoute {
	return AccessRoute{
		Method:           http.MethodGet,
		Path:             path,
		GateOffP50Ms:     states.off.p50,
		GateNoListP50Ms:  states.noList.p50,
		GateListP50Ms:    states.listed.p50,
		GateRefusedP50Ms: states.refused.p50,
		RefusedStatus:    states.refused.status,
	}
}

// journalEntriesValid reports an error for an entry count the batch builder
// cannot use.
func journalEntriesValid(entries []int) error {
	for _, count := range entries {
		if count < entriesPerResource || count%entriesPerResource != 0 {
			return fmt.Errorf("%w: %s", errBadJournalEntries, strconv.Itoa(count))
		}
	}

	return nil
}
