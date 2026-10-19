package main

import (
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Route paths the harness replays, shaped like the recorded CLI traffic.
// They mirror the adapter route tables in internal/api/pulumi/routes.go and
// internal/api/tfe/routes.go: an endpoint change here needs its e2e step
// first, per the backend rules.
const (
	stacksPath      = "/api/stacks/"
	updatePath      = "/update"
	verbatimPath    = "/checkpointverbatim"
	deltaPath       = "/checkpointdelta"
	completePath    = "/complete"
	importPath      = "/import"
	inventoryPath   = "/api/dustpan/v1/namespaces/"
	inventoryUnits  = "/units"
	tfeWorkspaces   = "/api/v2/organizations/"
	tfeWorkspaceMid = "/workspaces"
	tfeByID         = "/api/v2/workspaces/"
	tfeLock         = "/actions/lock"
	tfeUnlock       = "/actions/unlock"
	tfeStates       = "/state-versions"
)

// Checkpoint sequence numbers: the first save of an update is verbatim,
// later saves are deltas.
const (
	firstCheckpointSeq  = 1
	secondCheckpointSeq = 2
)

// halfUnits splits seeded units into half stacks and half workspaces.
const halfUnits = 2

// errUnexpectedLockStatus reports a lock answer that is neither won nor refused.
var errUnexpectedLockStatus = errors.New("unexpected lock status")

// updateIDResponse answers an update create with the opaque update ID.
type updateIDResponse struct {
	UpdateID string `json:"updateID"`
}

// startResponse answers an update start with the lease token.
type startResponse struct {
	Token string `json:"token"`
}

// workspaceIDData holds the ID of a created workspace.
type workspaceIDData struct {
	ID string `json:"id"`
}

// workspaceIDResponse answers a workspace create with the workspace ID.
type workspaceIDResponse struct {
	Data workspaceIDData `json:"data"`
}

// stateUploadAttributes holds the capability URL of a state version create.
type stateUploadAttributes struct {
	UploadURL string `json:"hosted-state-upload-url"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateUploadData holds the attributes of a state version create.
type stateUploadData struct {
	Attributes stateUploadAttributes `json:"attributes"`
}

// stateUploadResponse answers a state version create with the upload URL.
type stateUploadResponse struct {
	Data stateUploadData `json:"data"`
}

// workspacePagination holds the page count of a workspace list.
type workspacePagination struct {
	TotalPages int `json:"total-pages"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// workspacePageMeta holds the pagination of a workspace list.
type workspacePageMeta struct {
	Pagination workspacePagination `json:"pagination"`
}

// workspacePageResponse answers a workspace list with its page count.
type workspacePageResponse struct {
	Meta workspacePageMeta `json:"meta"`
}

// inventoryResponse answers a unit list with the opaque units.
type inventoryResponse struct {
	Units []json.RawMessage `json:"units"`
}

// createStack replays the CLI stack create in the load project.
func (t *target) createStack(ctx context.Context, name string) error {
	url := t.firstURL(stacksPath + t.ns + "/" + loadProject)

	body := []byte(`{"stackName":` + strconv.Quote(name) + `}`)

	resp, err := t.do(ctx, request{
		method:    http.MethodPost,
		url:       url,
		auth:      pulumiScheme + t.token,
		mediaType: jsonMediaType,
		body:      body,
	})
	if err != nil {
		return err
	}

	return check(http.MethodPost, url, resp.status, http.StatusOK, resp.body)
}

// updateLease is one started update: its ID with the lease token the later
// writes carry.
type updateLease struct {
	id    string
	lease string
}

// createUpdate replays the CLI update create and start: it answers the
// update ID with the lease token the later writes carry.
func (t *target) createUpdate(ctx context.Context, project, stackName string) (updateLease, error) {
	base := t.firstURL(stacksPath + t.ns + "/" + project + "/" + stackName)

	createResp, createErr := t.postUpdate(ctx, base+updatePath, base+updatePath)
	if createErr != nil {
		return updateLease{}, createErr
	}

	var created updateIDResponse

	if decodeErr := json.Unmarshal(createResp.body, &created); decodeErr != nil {
		return updateLease{}, fmt.Errorf("decode update ID: %w", decodeErr)
	}

	startURL := base + updatePath + "/" + created.UpdateID

	startResp, startErr := t.postUpdate(ctx, startURL, startURL)
	if startErr != nil {
		return updateLease{}, startErr
	}

	var started startResponse

	if decodeErr := json.Unmarshal(startResp.body, &started); decodeErr != nil {
		return updateLease{}, fmt.Errorf("decode lease token: %w", decodeErr)
	}

	return updateLease{id: created.UpdateID, lease: started.Token}, nil
}

// postUpdate posts an empty update body and answers the full response.
func (t *target) postUpdate(ctx context.Context, url, logName string) (response, error) {
	resp, err := t.do(ctx, request{
		method:    http.MethodPost,
		url:       url,
		auth:      pulumiScheme + t.token,
		mediaType: jsonMediaType,
		body:      []byte(emptyJSONBody),
	})
	if err != nil {
		return response{}, err
	}

	if checkErr := check(http.MethodPost, logName, resp.status, http.StatusOK, resp.body); checkErr != nil {
		return response{}, checkErr
	}

	return resp, nil
}

// verbatimSave is one first checkpoint save of an update.
type verbatimSave struct {
	stack      string
	id         string
	lease      string
	deployment []byte
	seq        int
}

// saveVerbatim replays the first checkpoint save of an update.
func (t *target) saveVerbatim(ctx context.Context, save verbatimSave) error {
	url := t.firstURL(
		stacksPath + t.ns + "/" + loadProject + "/" + save.stack + updatePath + "/" + save.id + verbatimPath,
	)

	body := []byte(`{"version":3,"untypedDeployment":` + string(save.deployment) +
		`,"sequenceNumber":` + strconv.Itoa(save.seq) + `}`)

	resp, err := t.do(ctx, request{
		method:    http.MethodPatch,
		url:       url,
		auth:      updateScheme + save.lease,
		mediaType: jsonMediaType,
		body:      body,
	})
	if err != nil {
		return err
	}

	return check(http.MethodPatch, url, resp.status, http.StatusOK, resp.body)
}

// deltaSave is one later checkpoint save of an update.
type deltaSave struct {
	stack string
	id    string
	lease string
	delta []byte
	hash  string
	seq   int
}

// saveDelta replays a later checkpoint save of an update.
func (t *target) saveDelta(ctx context.Context, save deltaSave) error {
	url := t.firstURL(
		stacksPath + t.ns + "/" + loadProject + "/" + save.stack + updatePath + "/" + save.id + deltaPath,
	)

	body := []byte(`{"version":3,"checkpointHash":` + strconv.Quote(save.hash) +
		`,"sequenceNumber":` + strconv.Itoa(save.seq) + `,"deploymentDelta":` + string(save.delta) + `}`)

	resp, err := t.do(ctx, request{
		method:    http.MethodPatch,
		url:       url,
		auth:      updateScheme + save.lease,
		mediaType: jsonMediaType,
		body:      body,
	})
	if err != nil {
		return err
	}

	return check(http.MethodPatch, url, resp.status, http.StatusOK, resp.body)
}

// completeUpdate replays the end of an update.
func (t *target) completeUpdate(ctx context.Context, stackName, id, lease string) error {
	url := t.firstURL(
		stacksPath + t.ns + "/" + loadProject + "/" + stackName + updatePath + "/" + id + completePath,
	)

	resp, err := t.do(ctx, request{
		method:    http.MethodPost,
		url:       url,
		auth:      updateScheme + lease,
		mediaType: jsonMediaType,
		body:      []byte(`{"status":"succeeded"}`),
	})
	if err != nil {
		return err
	}

	return check(http.MethodPost, url, resp.status, http.StatusOK, resp.body)
}

// importStack replays stack import with deployment.
func (t *target) importStack(ctx context.Context, stackName string, deployment []byte) (int64, error) {
	url := t.firstURL(stacksPath + t.ns + "/" + loadProject + "/" + stackName + importPath)

	body := []byte(`{"version":3,"deployment":` + string(deployment) + `}`)

	resp, err := t.do(ctx, request{
		method:    http.MethodPost,
		url:       url,
		auth:      pulumiScheme + t.token,
		mediaType: jsonMediaType,
		body:      body,
	})
	if err != nil {
		return 0, err
	}

	if checkErr := check(http.MethodPost, url, resp.status, http.StatusOK, resp.body); checkErr != nil {
		return 0, checkErr
	}

	return int64(len(deployment)), nil
}

// createWorkspace replays the OpenTofu workspace create.
func (t *target) createWorkspace(ctx context.Context, name string) (string, error) {
	url := t.firstURL(tfeWorkspaces + t.ns + tfeWorkspaceMid)

	body := []byte(`{"data":{"type":"workspaces","attributes":{"name":` + strconv.Quote(name) + `}}}`)

	resp, err := t.do(ctx, request{
		method:    http.MethodPost,
		url:       url,
		auth:      tfeScheme + t.token,
		mediaType: jsonMediaType,
		body:      body,
	})
	if err != nil {
		return "", err
	}

	if checkErr := check(http.MethodPost, url, resp.status, http.StatusCreated, resp.body); checkErr != nil {
		return "", checkErr
	}

	var created workspaceIDResponse

	if decodeErr := json.Unmarshal(resp.body, &created); decodeErr != nil {
		return "", fmt.Errorf("decode workspace ID: %w", decodeErr)
	}

	return created.Data.ID, nil
}

// lockWorkspace replays the OpenTofu lock with its status answered.
func (t *target) lockWorkspace(ctx context.Context, id string) (int, error) {
	url := t.firstURL(tfeByID + id + tfeLock)

	resp, err := t.do(ctx, request{
		method:    http.MethodPost,
		url:       url,
		auth:      tfeScheme + t.token,
		mediaType: jsonMediaType,
		body:      []byte(`{"data":{"attributes":{"reason":"load"}}}`),
	})
	if err != nil {
		return 0, err
	}

	return resp.status, nil
}

// unlockWorkspace replays the OpenTofu unlock.
func (t *target) unlockWorkspace(ctx context.Context, id string) error {
	url := t.firstURL(tfeByID + id + tfeUnlock)

	resp, err := t.do(ctx, request{
		method:    http.MethodPost,
		url:       url,
		auth:      tfeScheme + t.token,
		mediaType: jsonMediaType,
		body:      []byte(emptyJSONBody),
	})
	if err != nil {
		return err
	}

	return check(http.MethodPost, url, resp.status, http.StatusOK, resp.body)
}

// uploadState replays the OpenTofu state upload: declare the version, then
// PUT the content to the capability URL the create answers.
func (t *target) uploadState(ctx context.Context, id string, content []byte, serial int64) error {
	sum := md5.Sum(content) //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.

	url := t.firstURL(tfeByID + id + tfeStates)

	body := []byte(`{"data":{"attributes":{"serial":` + strconv.FormatInt(serial, 10) +
		`,"lineage":"load-` + id + `","md5":` + strconv.Quote(hex.EncodeToString(sum[:])) + `}}}`)

	resp, err := t.do(ctx, request{
		method:    http.MethodPost,
		url:       url,
		auth:      tfeScheme + t.token,
		mediaType: jsonMediaType,
		body:      body,
	})
	if err != nil {
		return err
	}

	if checkErr := check(http.MethodPost, url, resp.status, http.StatusCreated, resp.body); checkErr != nil {
		return checkErr
	}

	var created stateUploadResponse

	if decodeErr := json.Unmarshal(resp.body, &created); decodeErr != nil {
		return fmt.Errorf("decode upload URL: %w", decodeErr)
	}

	putResp, putErr := t.do(ctx, request{
		method:    http.MethodPut,
		url:       created.Data.Attributes.UploadURL,
		auth:      "",
		mediaType: streamMediaType,
		body:      content,
	})
	if putErr != nil {
		return putErr
	}

	return check(http.MethodPut, url, putResp.status, http.StatusOK, putResp.body)
}

// measureCheckpoints replays one update per size: a full save, then a delta.
func (t *target) measureCheckpoints(ctx context.Context, sizes []int) ([]CheckpointResult, error) {
	out := make([]CheckpointResult, 0, len(sizes))

	for i, size := range sizes {
		deployment := buildDeployment(size)
		made, deltaErr := makeDelta(deployment)

		if deltaErr != nil {
			return nil, deltaErr
		}

		name := checkpointStack + "-" + strconv.Itoa(i)

		if stackErr := t.createStack(ctx, name); stackErr != nil {
			return nil, stackErr
		}

		started, updateErr := t.createUpdate(ctx, loadProject, name)
		if updateErr != nil {
			return nil, updateErr
		}

		fullTook, fullPeak, fullErr := timed(func() error {
			return t.saveVerbatim(ctx, verbatimSave{
				stack:      name,
				id:         started.id,
				lease:      started.lease,
				deployment: deployment,
				seq:        firstCheckpointSeq,
			})
		})
		if fullErr != nil {
			return nil, fullErr
		}

		deltaTook, deltaPeak, saveErr := timed(func() error {
			return t.saveDelta(ctx, deltaSave{
				stack: name,
				id:    started.id,
				lease: started.lease,
				delta: made.delta,
				hash:  made.hash,
				seq:   secondCheckpointSeq,
			})
		})
		if saveErr != nil {
			return nil, saveErr
		}

		if completeErr := t.completeUpdate(ctx, name, started.id, started.lease); completeErr != nil {
			return nil, completeErr
		}

		out = append(out, CheckpointResult{
			SizeBytes:           len(deployment),
			FullMs:              ms(fullTook),
			DeltaMs:             ms(deltaTook),
			FullPeakAllocBytes:  fullPeak,
			DeltaPeakAllocBytes: deltaPeak,
		})
	}

	return out, nil
}

// measureImports replays one stack import per size.
func (t *target) measureImports(ctx context.Context, sizes []int) ([]ImportResult, error) {
	out := make([]ImportResult, 0, len(sizes))

	for i, size := range sizes {
		deployment := buildDeployment(size)
		name := importStackName + "-" + strconv.Itoa(i)

		if stackErr := t.createStack(ctx, name); stackErr != nil {
			return nil, stackErr
		}

		var stored int64

		took, peak, err := timed(func() error {
			var importErr error

			stored, importErr = t.importStack(ctx, name, deployment)

			return importErr
		})
		if err != nil {
			return nil, err
		}

		out = append(out, ImportResult{
			SizeBytes:       len(deployment),
			ImportMs:        ms(took),
			PeakAllocBytes:  peak,
			CheckpointBytes: stored,
		})
	}

	return out, nil
}

// seedOneStack creates one stack unit.
func (t *target) seedOneStack(ctx context.Context, i int) error {
	return t.createStack(ctx, "load-"+padNumber(i))
}

// lockSeedWorkspace locks the seeded workspace and requires the win: seeding
// runs each workspace alone, so a conflict means another run interferes.
func (t *target) lockSeedWorkspace(ctx context.Context, id, name string) error {
	status, err := t.lockWorkspace(ctx, id)
	if err != nil {
		return err
	}

	return check(http.MethodPost, "lock "+name, status, http.StatusOK, nil)
}

// seedOneWorkspace creates one workspace unit with a small state version, so
// the list routes render relationships as with real CLI traffic.
func (t *target) seedOneWorkspace(ctx context.Context, i int) error {
	name := workspacePrefix + padNumber(i)

	id, createErr := t.createWorkspace(ctx, name)
	if createErr != nil {
		return createErr
	}

	if lockErr := t.lockSeedWorkspace(ctx, id, name); lockErr != nil {
		return lockErr
	}

	const seedStateBytes = 2048

	uploadErr := t.uploadState(ctx, id, buildState(seedStateBytes, 1, "load-"+name), 1)
	if uploadErr != nil {
		return uploadErr
	}

	return t.unlockWorkspace(ctx, id)
}

// padNumber answers i zero-padded for stable unit names.
func padNumber(i int) string {
	s := strconv.Itoa(i)

	for len(s) < stackNameWidth {
		s = "0" + s
	}

	return s
}

// seedJob is one unit to create: a stack or a workspace.
type seedJob struct {
	workspace bool
	n         int
}

// runSeedJob creates the unit j names.
func (t *target) runSeedJob(ctx context.Context, j seedJob) error {
	if j.workspace {
		return t.seedOneWorkspace(ctx, j.n)
	}

	return t.seedOneStack(ctx, j.n)
}

// seedCollector keeps the first seed error and stops the feed.
type seedCollector struct {
	mu     sync.Mutex
	first  error
	cancel context.CancelFunc
}

// fail keeps err when it is the first and stops the feed.
func (c *seedCollector) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.first == nil {
		c.first = err
		c.cancel()
	}
}

// err answers the first seed error, or nil.
func (c *seedCollector) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.first
}

// feedSeedJobs sends half stacks then half workspaces, until ctx ends.
func (t *target) feedSeedJobs(ctx context.Context, jobs chan<- seedJob, units int) {
	stacks := units / halfUnits

	for i := range stacks {
		select {
		case <-ctx.Done():
			return
		case jobs <- seedJob{workspace: false, n: i}:
		}
	}

	for i := range units - stacks {
		select {
		case <-ctx.Done():
			return
		case jobs <- seedJob{workspace: true, n: i}:
		}
	}
}

// seedUnits creates units half stacks and half workspaces with a worker pool.
func (t *target) seedUnits(ctx context.Context, units, workers int) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	collector := &seedCollector{mu: sync.Mutex{}, first: nil, cancel: cancel}
	jobs := make(chan seedJob)

	var wg sync.WaitGroup

	for range workers {
		wg.Go(func() {
			for j := range jobs {
				if err := t.runSeedJob(ctx, j); err != nil {
					collector.fail(err)

					return
				}
			}
		})
	}

	go func() {
		defer close(jobs)

		t.feedSeedJobs(ctx, jobs, units)
	}()

	wg.Wait()

	return collector.err()
}

// measureFirstPages reads the first workspace page reps times and answers
// the latencies with the total page count.
func (t *target) measureFirstPages(ctx context.Context, reps int) ([]float64, int, error) {
	var latencies []float64

	var pages int

	for range reps {
		start := time.Now()

		resp, reqErr := t.do(ctx, request{
			method: http.MethodGet,
			url: t.firstURL(
				tfeWorkspaces + t.ns + tfeWorkspaceMid + "?page[size]=" + strconv.Itoa(defaultTFEPageSize),
			),
			auth:      tfeScheme + t.token,
			mediaType: "",
			body:      nil,
		})
		if reqErr != nil {
			return nil, 0, reqErr
		}

		if checkErr := check(http.MethodGet, "workspace list", resp.status, http.StatusOK, resp.body); checkErr != nil {
			return nil, 0, checkErr
		}

		latencies = append(latencies, ms(time.Since(start)))

		if pages == 0 {
			var page workspacePageResponse

			if decodeErr := json.Unmarshal(resp.body, &page); decodeErr != nil {
				return nil, 0, fmt.Errorf("decode page count: %w", decodeErr)
			}

			pages = page.Meta.Pagination.TotalPages
		}
	}

	return latencies, pages, nil
}

// measureFullList walks every workspace page and answers the total time.
func (t *target) measureFullList(ctx context.Context, pages int) (time.Duration, error) {
	fullStart := time.Now()

	for page := 1; page <= pages; page++ {
		url := t.firstURL(
			tfeWorkspaces + t.ns + tfeWorkspaceMid +
				"?page[size]=" + strconv.Itoa(defaultTFEPageSize) +
				"&page[number]=" + strconv.Itoa(page),
		)

		resp, err := t.do(ctx, request{
			method:    http.MethodGet,
			url:       url,
			auth:      tfeScheme + t.token,
			mediaType: "",
			body:      nil,
		})
		if err != nil {
			return 0, err
		}

		if checkErr := check(http.MethodGet, "workspace list", resp.status, http.StatusOK, resp.body); checkErr != nil {
			return 0, checkErr
		}
	}

	return time.Since(fullStart), nil
}

// measureInventory reads the inventory and answers the time with the units.
func (t *target) measureInventory(ctx context.Context) (time.Duration, inventoryResponse, error) {
	invStart := time.Now()

	resp, err := t.do(ctx, request{
		method:    http.MethodGet,
		url:       t.firstURL(inventoryPath + t.ns + inventoryUnits),
		auth:      pulumiScheme + t.token,
		mediaType: "",
		body:      nil,
	})
	if err != nil {
		return 0, inventoryResponse{}, err
	}

	if checkErr := check(http.MethodGet, "inventory", resp.status, http.StatusOK, resp.body); checkErr != nil {
		return 0, inventoryResponse{}, checkErr
	}

	inventoryTook := time.Since(invStart)

	var listed inventoryResponse

	if decodeErr := json.Unmarshal(resp.body, &listed); decodeErr != nil {
		return 0, inventoryResponse{}, fmt.Errorf("decode inventory: %w", decodeErr)
	}

	return inventoryTook, listed, nil
}

// measureLists reads the workspace list and the inventory with a full store.
func (t *target) measureLists(ctx context.Context, units, reps int) (ListResult, error) {
	first, pages, firstErr := t.measureFirstPages(ctx, reps)
	if firstErr != nil {
		return ListResult{}, firstErr
	}

	fullTook, fullErr := t.measureFullList(ctx, pages)
	if fullErr != nil {
		return ListResult{}, fullErr
	}

	inventoryTook, listed, invErr := t.measureInventory(ctx)
	if invErr != nil {
		return ListResult{}, invErr
	}

	return ListResult{
		Units:               units,
		UnitsListed:         len(listed.Units),
		WorkspaceListP50Ms:  p50(first),
		WorkspaceListFullMs: ms(fullTook),
		InventoryMs:         ms(inventoryTook),
	}, nil
}

// lockTally counts one lock contention run under a mutex.
type lockTally struct {
	mu        sync.Mutex
	locks     int
	conflicts int
	latencies []float64
	first     error
}

// addLatency records one attempt latency.
func (c *lockTally) addLatency(latency float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.latencies = append(c.latencies, latency)
}

// fail keeps err when it is the first.
func (c *lockTally) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.first == nil {
		c.first = err
	}
}

// won counts one won lock.
func (c *lockTally) won() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.locks++
}

// refused counts one 409 conflict.
func (c *lockTally) refused() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.conflicts++
}

// result answers the lock report with the first error, when one happened.
func (c *lockTally) result(goroutines, rounds int) (LockResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.first != nil {
		return LockResult{}, c.first
	}

	return LockResult{
		Goroutines: goroutines,
		Rounds:     rounds,
		Locks:      c.locks,
		Conflicts:  c.conflicts,
		P50Ms:      p50(c.latencies),
	}, nil
}

// lockRound runs one lock attempt on id: a win unlocks at once, a 409 counts
// as refused, and any other answer fails the run.
func (t *target) lockRound(ctx context.Context, id string, tally *lockTally) error {
	start := time.Now()

	status, err := t.lockWorkspace(ctx, id)

	tally.addLatency(ms(time.Since(start)))

	if err != nil {
		tally.fail(err)

		return err
	}

	switch status {
	case http.StatusOK:
		tally.won()

		if unlockErr := t.unlockWorkspace(ctx, id); unlockErr != nil {
			tally.fail(unlockErr)

			return unlockErr
		}
	case http.StatusConflict:
		tally.refused()
	default:
		statusErr := fmt.Errorf("lock: status %d: %w", status, errUnexpectedLockStatus)
		tally.fail(statusErr)

		return statusErr
	}

	return nil
}

// measureWorkspaceLocks hammers one workspace with lock rounds.
func (t *target) measureWorkspaceLocks(
	ctx context.Context,
	goroutines, rounds int,
) (LockResult, error) {
	id, createErr := t.createWorkspace(ctx, "lock-contend")
	if createErr != nil {
		return LockResult{}, createErr
	}

	tally := &lockTally{mu: sync.Mutex{}, locks: 0, conflicts: 0, latencies: nil, first: nil}

	var wg sync.WaitGroup

	for range goroutines {
		wg.Go(func() {
			for range rounds {
				if err := t.lockRound(ctx, id, tally); err != nil {
					return
				}
			}
		})
	}

	wg.Wait()

	return tally.result(goroutines, rounds)
}

// startOutcome is one update start attempt: whether it started, whether the
// stack lock refused it with 409, and how long the attempt took.
type startOutcome struct {
	started  bool
	conflict bool
	latency  float64
}

// tryStartUpdate creates and starts one update on the stack name. An update
// left running holds the stack lock for the later attempts on the same stack.
func (t *target) tryStartUpdate(ctx context.Context, name string) (startOutcome, error) {
	start := time.Now()
	latency := func() float64 { return ms(time.Since(start)) }

	base := t.firstURL(stacksPath + t.ns + "/" + loadProject + "/" + name)

	createResp, createErr := t.do(ctx, request{
		method:    http.MethodPost,
		url:       base + updatePath,
		auth:      pulumiScheme + t.token,
		mediaType: jsonMediaType,
		body:      []byte(emptyJSONBody),
	})
	if createErr != nil {
		return startOutcome{started: false, conflict: false, latency: latency()}, createErr
	}

	if createResp.status == http.StatusConflict {
		return startOutcome{started: false, conflict: true, latency: latency()}, nil
	}

	createCheckErr := check(http.MethodPost, base+updatePath, createResp.status, http.StatusOK, createResp.body)
	if createCheckErr != nil {
		return startOutcome{started: false, conflict: false, latency: latency()}, createCheckErr
	}

	var created updateIDResponse

	if decodeErr := json.Unmarshal(createResp.body, &created); decodeErr != nil {
		return startOutcome{started: false, conflict: false, latency: latency()}, fmt.Errorf(
			"decode update ID: %w",
			decodeErr,
		)
	}

	startURL := base + updatePath + "/" + created.UpdateID

	startResp, startErr := t.do(ctx, request{
		method:    http.MethodPost,
		url:       startURL,
		auth:      pulumiScheme + t.token,
		mediaType: jsonMediaType,
		body:      []byte(emptyJSONBody),
	})
	if startErr != nil {
		return startOutcome{started: false, conflict: false, latency: latency()}, startErr
	}

	if startResp.status == http.StatusConflict {
		return startOutcome{started: false, conflict: true, latency: latency()}, nil
	}

	startCheckErr := check(http.MethodPost, startURL, startResp.status, http.StatusOK, startResp.body)
	if startCheckErr != nil {
		return startOutcome{started: false, conflict: false, latency: latency()}, startCheckErr
	}

	return startOutcome{started: true, conflict: false, latency: latency()}, nil
}

// updateAttempt is one contention outcome with its failure, when one happened.
type updateAttempt struct {
	outcome startOutcome
	failed  error
}

// measureUpdateConflicts starts many updates on one stack at once. The stack
// lock lets one start and refuses the others with 409, so this run measures
// the conflict path.
func (t *target) measureUpdateConflicts(ctx context.Context, attempts int) (UpdateResult, error) {
	if stackErr := t.createStack(ctx, contentionStack); stackErr != nil {
		return UpdateResult{}, stackErr
	}

	names := make([]string, attempts)
	for i := range names {
		names[i] = contentionStack
	}

	return t.startUpdatesAtOnce(ctx, names)
}

// measureNamespaceUpdates creates one stack for each attempt in the same
// namespace, then starts one update on each stack at once. No stack lock
// refuses a start, so this run measures contention on the namespace fence and
// the store.
func (t *target) measureNamespaceUpdates(ctx context.Context, attempts int) (UpdateResult, error) {
	names := make([]string, attempts)

	for i := range names {
		names[i] = namespaceStackPrefix + padNumber(i)

		if stackErr := t.createStack(ctx, names[i]); stackErr != nil {
			return UpdateResult{}, stackErr
		}
	}

	return t.startUpdatesAtOnce(ctx, names)
}

// startUpdatesAtOnce starts one update for each entry of names, all at once,
// and tallies the starts, the 409 refusals and the median attempt latency.
func (t *target) startUpdatesAtOnce(ctx context.Context, names []string) (UpdateResult, error) {
	attempts := len(names)
	outcomes := make(chan updateAttempt, attempts)

	var wg sync.WaitGroup

	for _, name := range names {
		wg.Go(func() {
			outcome, err := t.tryStartUpdate(ctx, name)
			outcomes <- updateAttempt{outcome: outcome, failed: err}
		})
	}

	wg.Wait()
	close(outcomes)

	var (
		started   int
		conflicts int
		latencies []float64
	)

	for o := range outcomes {
		if o.failed != nil {
			return UpdateResult{}, o.failed
		}

		latencies = append(latencies, o.outcome.latency)

		if o.outcome.started {
			started++
		}

		if o.outcome.conflict {
			conflicts++
		}
	}

	return UpdateResult{
		Attempts:  attempts,
		Started:   started,
		Conflicts: conflicts,
		P50Ms:     p50(latencies),
	}, nil
}

// probeFirstInstance creates one stack and starts its update on instance 0,
// answering the start latency.
func (t *target) probeFirstInstance(ctx context.Context, i int) (float64, error) {
	name := "db-probe-" + strconv.Itoa(i)

	if stackErr := t.createStack(ctx, name); stackErr != nil {
		return 0, stackErr
	}

	base := stacksPath + t.ns + "/" + loadProject + "/" + name

	start := time.Now()

	if _, err := t.postUpdate(ctx, t.urlFor(0, base+updatePath), base); err != nil {
		return 0, err
	}

	return ms(time.Since(start)), nil
}

// probeSecondInstance creates one stack and starts its update on instance 1,
// answering the start latency.
func (t *target) probeSecondInstance(ctx context.Context, i int) (float64, error) {
	name := "db-probe-second-" + strconv.Itoa(i)

	if stackErr := t.createStack(ctx, name); stackErr != nil {
		return 0, stackErr
	}

	base := stacksPath + t.ns + "/" + loadProject + "/" + name

	start := time.Now()

	if _, err := t.postUpdate(ctx, t.urlFor(1, base+updatePath), base); err != nil {
		return 0, err
	}

	return ms(time.Since(start)), nil
}

// dbReps is the update count per instance of the database run.
const dbReps = 8

// measureDatabase starts updates round-robin over the instances: one update
// per instance per rep, so the second p50 stays 0 with one instance.
func (t *target) measureDatabase(ctx context.Context) (DatabaseResult, error) {
	first := make([]float64, 0, dbReps)
	second := make([]float64, 0, dbReps)

	for i := range dbReps {
		firstTook, firstErr := t.probeFirstInstance(ctx, i)
		if firstErr != nil {
			return DatabaseResult{}, firstErr
		}

		first = append(first, firstTook)

		if len(t.urls) > 1 {
			secondTook, secondErr := t.probeSecondInstance(ctx, i)
			if secondErr != nil {
				return DatabaseResult{}, secondErr
			}

			second = append(second, secondTook)
		}
	}

	return DatabaseResult{
		Instances:     len(t.urls),
		StoresOpen:    len(t.urls),
		FirstP50Ms:    p50(first),
		SecondP50Ms:   p50(second),
		SecondPresent: len(t.urls) > 1,
	}, nil
}

// Execute replays the recorded sequences and answers the report.
func Execute(ctx context.Context, cfg Config, urls []string) (Report, error) {
	start := time.Now()

	t := newTarget(urls, cfg.Token, cfg.Namespace)

	checkpoints, checkpointsErr := t.measureCheckpoints(ctx, cfg.CheckpointSizes)
	if checkpointsErr != nil {
		return Report{}, checkpointsErr
	}

	imports, importsErr := t.measureImports(ctx, cfg.ImportSizes)
	if importsErr != nil {
		return Report{}, importsErr
	}

	conflicts, conflictsErr := t.measureUpdateConflicts(ctx, cfg.ContentionAttempts)
	if conflictsErr != nil {
		return Report{}, conflictsErr
	}

	namespaceUpdates, namespaceErr := t.measureNamespaceUpdates(ctx, cfg.ContentionAttempts)
	if namespaceErr != nil {
		return Report{}, namespaceErr
	}

	seedStart := time.Now()

	if seedErr := t.seedUnits(ctx, cfg.Units, cfg.SeedWorkers); seedErr != nil {
		return Report{}, seedErr
	}

	seedTook := time.Since(seedStart)

	lists, listsErr := t.measureLists(ctx, cfg.Units, cfg.ListReps)
	if listsErr != nil {
		return Report{}, listsErr
	}

	locks, locksErr := t.measureWorkspaceLocks(ctx, cfg.LockGoroutines, cfg.LockRounds)
	if locksErr != nil {
		return Report{}, locksErr
	}

	database, databaseErr := t.measureDatabase(ctx)
	if databaseErr != nil {
		return Report{}, databaseErr
	}

	return Report{
		Time:             time.Now().UTC().Format(time.RFC3339),
		Store:            cfg.Store,
		Namespace:        cfg.Namespace,
		Units:            cfg.Units,
		DurationMs:       ms(time.Since(start)),
		SeedMs:           ms(seedTook),
		Checkpoints:      checkpoints,
		Imports:          imports,
		WorkspaceLocks:   locks,
		UpdateConflicts:  conflicts,
		NamespaceUpdates: namespaceUpdates,
		Database:         database,
		Lists:            lists,
	}, nil
}
