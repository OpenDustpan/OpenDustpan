package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Server API paths of the backup and restore runs.
const (
	backupsPath  = "/api/server/backups/"
	restoresPath = "/api/server/restores/"
)

// Default dimensions of the blob flow run.
const (
	defaultFlowReps        = 20
	defaultFlowStateBytes  = 65536
	defaultFlowCheckpoint  = 262144
	defaultDatasetUnits    = 20
	defaultDatasetState    = 65536
	defaultDatasetCheck    = 262144
	defaultOperationPollMs = 200
	defaultOperationWait   = 30 * time.Minute
)

// Name prefixes of the units that the blob flow run writes.
const (
	flowStackPrefix       = "flow-"
	flowWorkspacePrefix   = "ws-flow-"
	datasetStackPrefix    = "set-"
	datasetWorkspacePrefx = "ws-set-"
)

// stateFailed is the state of a backup or a restore that ended badly. Every
// other state (running, checking, applying) has not ended yet.
const stateFailed = "failed"

// stateSucceeded is the state of a backup or a restore that ended well.
const stateSucceeded = "succeeded"

// errOperationFailed reports a backup or a restore that ended in a state
// other than succeeded.
var errOperationFailed = errors.New("operation did not succeed")

// errOperationTimeout reports a backup or a restore that did not end in time.
var errOperationTimeout = errors.New("operation did not end in time")

// BlobFlowConfig tunes the blob flow run: the CLI flows that write a blob,
// then a backup and a restore of a fixed data set.
type BlobFlowConfig struct {
	Store     string `json:"store"`
	Blobs     string `json:"blobs"`
	S3Probe   bool   `json:"s3Probe"`
	Namespace string `json:"namespace"`
	Token     string `json:"-"`
	// Reps is the count of OpenTofu state uploads and of Pulumi checkpoint
	// saves, each on its own unit.
	Reps            int `json:"reps"`
	StateBytes      int `json:"stateBytes"`
	CheckpointBytes int `json:"checkpointBytes"`
	// DatasetUnits is the count of workspaces and of stacks in the data set
	// of the backup, each with one state version or one checkpoint.
	DatasetUnits           int `json:"datasetUnits"`
	DatasetStateBytes      int `json:"datasetStateBytes"`
	DatasetCheckpointBytes int `json:"datasetCheckpointBytes"`
	// Backup turns the backup and restore steps on. It needs a backup target.
	Backup bool `json:"backup"`
	// HTTPTimeoutSecs bounds each request of the run. The CLIs set no such
	// bound, so a limit sweep sets it high: the harness must not be what
	// breaks first.
	HTTPTimeoutSecs int `json:"httpTimeoutSecs"`
}

// DefaultBlobFlowConfig answers the blob flow run with its default sizes.
func DefaultBlobFlowConfig() BlobFlowConfig {
	return BlobFlowConfig{
		Store:                  storeSQLite,
		Blobs:                  blobsFS,
		S3Probe:                true,
		Namespace:              "load",
		Token:                  "",
		Reps:                   defaultFlowReps,
		StateBytes:             defaultFlowStateBytes,
		CheckpointBytes:        defaultFlowCheckpoint,
		DatasetUnits:           defaultDatasetUnits,
		DatasetStateBytes:      defaultDatasetState,
		DatasetCheckpointBytes: defaultDatasetCheck,
		Backup:                 true,
		HTTPTimeoutSecs:        defaultHTTPTimeoutSecs,
	}
}

// FlowStats holds the latency of one flow over its attempts. Errors counts
// the attempts that failed, and FirstError holds the text of the first one.
type FlowStats struct {
	Samples    int     `json:"samples"`
	P50Ms      float64 `json:"p50Ms"`
	P99Ms      float64 `json:"p99Ms"`
	MaxMs      float64 `json:"maxMs"`
	Errors     int     `json:"errors"`
	FirstError string  `json:"firstError,omitempty"`
}

// OperationResult holds one backup or one restore: its wall time from the
// start request to the end state, and the error when it did not succeed.
type OperationResult struct {
	Ran   bool    `json:"ran"`
	Ms    float64 `json:"ms"`
	State string  `json:"state,omitempty"`
	Error string  `json:"error,omitempty"`
}

// BlobFlowReport is the output of the blob flow run. Times are milliseconds.
type BlobFlowReport struct {
	Time   string         `json:"time"`
	Config BlobFlowConfig `json:"config"`
	// DatasetMs is the wall time of the seed of the backup data set.
	DatasetMs float64 `json:"datasetMs"`
	// Backup and Restore cover the fixed data set. A restore first takes
	// a backup of the live data, so it holds that backup too.
	Backup  OperationResult `json:"backup"`
	Restore OperationResult `json:"restore"`
	// StateUpload is the OpenTofu state upload: the declare of the state
	// version and the PUT of its content.
	StateUpload FlowStats `json:"stateUpload"`
	// StateCycle is lock, state upload and unlock.
	StateCycle FlowStats `json:"stateCycle"`
	// CheckpointSave is the first Pulumi checkpoint save of an update.
	CheckpointSave FlowStats `json:"checkpointSave"`
	// UpdateCycle is update create and start, checkpoint save and complete.
	UpdateCycle FlowStats `json:"updateCycle"`
	DurationMs  float64   `json:"durationMs"`
}

// flowTally collects the samples and the errors of one flow.
type flowTally struct {
	samples []float64
	errors  int
	first   string
}

// add records one attempt: its time when err is nil, else the error.
func (f *flowTally) add(took time.Duration, err error) {
	if err != nil {
		f.errors++

		if f.first == "" {
			f.first = err.Error()
		}

		return
	}

	f.samples = append(f.samples, ms(took))
}

// stats answers the percentiles of the tally.
func (f *flowTally) stats() FlowStats {
	const p99 = 0.99

	maxMs := 0.0
	for _, s := range f.samples {
		maxMs = max(maxMs, s)
	}

	return FlowStats{
		Samples:    len(f.samples),
		P50Ms:      p50(f.samples),
		P99Ms:      percentile(f.samples, p99),
		MaxMs:      maxMs,
		Errors:     f.errors,
		FirstError: f.first,
	}
}

// ExecuteBlobFlows runs the blob flows against srv: it seeds the data set,
// takes a backup and restores it when cfg.Backup is set, then times the
// state uploads and the checkpoint saves. A failed attempt of a timed flow
// counts in the report and does not stop the run, so a run at a high latency
// still reports what broke.
func ExecuteBlobFlows(ctx context.Context, cfg BlobFlowConfig, srv servers) (BlobFlowReport, error) {
	start := time.Now()

	t := newTarget(srv.urls, cfg.Token, cfg.Namespace)
	t.served = srv
	t.client.Timeout = time.Duration(cfg.HTTPTimeoutSecs) * time.Second

	reportTime := time.Now().UTC().Format(time.RFC3339)
	seedStart := time.Now()

	if err := t.seedDataset(ctx, cfg); err != nil {
		return BlobFlowReport{}, err
	}

	datasetMs := ms(time.Since(seedStart))

	ops := operationPair{
		backup:  OperationResult{Ran: false, Ms: 0, State: "", Error: ""},
		restore: OperationResult{Ran: false, Ms: 0, State: "", Error: ""},
	}
	if cfg.Backup {
		ops = t.backupAndRestore(ctx)
	}

	states := t.timeStateUploads(ctx, cfg)
	checkpoints := t.timeCheckpointSaves(ctx, cfg)

	return BlobFlowReport{
		Time:           reportTime,
		Config:         cfg,
		DatasetMs:      datasetMs,
		Backup:         ops.backup,
		Restore:        ops.restore,
		StateUpload:    states.step,
		StateCycle:     states.cycle,
		CheckpointSave: checkpoints.step,
		UpdateCycle:    checkpoints.cycle,
		DurationMs:     ms(time.Since(start)),
	}, nil
}

// flowPair holds the stats of one step of a flow and of its whole cycle.
type flowPair struct {
	step  FlowStats
	cycle FlowStats
}

// operationPair holds a backup and the restore of it.
type operationPair struct {
	backup  OperationResult
	restore OperationResult
}

// seedDataset writes the data set of the backup: DatasetUnits workspaces
// with one state version each and DatasetUnits stacks with one checkpoint
// each.
func (t *target) seedDataset(ctx context.Context, cfg BlobFlowConfig) error {
	deployment := versionedEnvelope(buildDeployment(cfg.DatasetCheckpointBytes))

	for i := range cfg.DatasetUnits {
		name := datasetStackPrefix + padNumber(i)

		if err := t.createStack(ctx, name); err != nil {
			return err
		}

		if _, err := t.saveOneCheckpoint(ctx, name, deployment); err != nil {
			return err
		}

		ws := datasetWorkspacePrefx + padNumber(i)

		id, err := t.createWorkspace(ctx, ws)
		if err != nil {
			return err
		}

		if err := t.lockSeedWorkspace(ctx, id, ws); err != nil {
			return err
		}

		if err := t.uploadState(ctx, id, buildState(cfg.DatasetStateBytes, 1, "load-"+ws), 1); err != nil {
			return err
		}

		if err := t.unlockWorkspace(ctx, id); err != nil {
			return err
		}
	}

	return nil
}

// cycleTimes holds the time of one update cycle and of its checkpoint save.
type cycleTimes struct {
	save  time.Duration
	cycle time.Duration
}

// saveOneCheckpoint replays one update of stack: create and start, the
// first checkpoint save, and complete. It answers the time of the save and
// of the whole cycle.
func (t *target) saveOneCheckpoint(ctx context.Context, stack string, deployment []byte) (cycleTimes, error) {
	cycleStart := time.Now()

	lease, err := t.createUpdate(ctx, loadProject, stack)
	if err != nil {
		return cycleTimes{}, err
	}

	saveStart := time.Now()

	saveErr := t.saveVerbatim(ctx, verbatimSave{
		stack: stack, id: lease.id, lease: lease.lease, deployment: deployment, seq: 1,
	})
	if saveErr != nil {
		return cycleTimes{}, saveErr
	}

	save := time.Since(saveStart)

	if err := t.completeUpdate(ctx, stack, lease.id, lease.lease); err != nil {
		return cycleTimes{}, err
	}

	return cycleTimes{save: save, cycle: time.Since(cycleStart)}, nil
}

// timeStateUploads replays Reps OpenTofu applies on one workspace: lock,
// declare and PUT a state of the next serial, unlock.
func (t *target) timeStateUploads(ctx context.Context, cfg BlobFlowConfig) flowPair {
	var uploads, cycles flowTally

	ws := flowWorkspacePrefix + padNumber(0)

	id, err := t.createWorkspace(ctx, ws)
	if err != nil {
		uploads.add(0, err)
		cycles.add(0, err)

		return flowPair{step: uploads.stats(), cycle: cycles.stats()}
	}

	for i := range cfg.Reps {
		serial := int64(i + 1)
		state := buildState(cfg.StateBytes, serial, "load-"+ws)
		cycleStart := time.Now()

		if err := t.lockSeedWorkspace(ctx, id, ws); err != nil {
			cycles.add(0, err)
			uploads.add(0, err)

			continue
		}

		uploadStart := time.Now()
		uploadErr := t.uploadState(ctx, id, state, serial)
		uploads.add(time.Since(uploadStart), uploadErr)

		unlockErr := t.unlockWorkspace(ctx, id)
		cycles.add(time.Since(cycleStart), errors.Join(uploadErr, unlockErr))
	}

	return flowPair{step: uploads.stats(), cycle: cycles.stats()}
}

// timeCheckpointSaves replays Reps Pulumi updates, each on its own stack,
// with one checkpoint save each.
func (t *target) timeCheckpointSaves(ctx context.Context, cfg BlobFlowConfig) flowPair {
	var saves, cycles flowTally

	deployment := versionedEnvelope(buildDeployment(cfg.CheckpointBytes))

	for i := range cfg.Reps {
		name := flowStackPrefix + padNumber(i)

		if err := t.createStack(ctx, name); err != nil {
			saves.add(0, err)
			cycles.add(0, err)

			continue
		}

		took, err := t.saveOneCheckpoint(ctx, name, deployment)
		saves.add(took.save, err)
		cycles.add(took.cycle, err)
	}

	return flowPair{step: saves.stats(), cycle: cycles.stats()}
}

// operationDoc is the part of a backup or a restore answer that the run reads.
type operationDoc struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Error string `json:"error"`
}

// backupAndRestore takes a backup of the data set, then restores it with
// force, and answers both results. A failed backup skips the restore.
func (t *target) backupAndRestore(ctx context.Context) operationPair {
	backupStart := time.Now()

	backupDoc, err := t.runOperation(ctx, http.MethodPost, backupsPath, nil)

	backup := operationResult(time.Since(backupStart), backupDoc, err)
	if backup.Error != "" {
		return operationPair{
			backup:  backup,
			restore: OperationResult{Ran: false, Ms: 0, State: "", Error: "backup failed"},
		}
	}

	body, marshalErr := json.Marshal(map[string]any{"backupID": backupDoc.ID, "force": true})
	if marshalErr != nil {
		return operationPair{
			backup:  backup,
			restore: OperationResult{Ran: false, Ms: 0, State: "", Error: marshalErr.Error()},
		}
	}

	restoreStart := time.Now()

	restoreDoc, err := t.runOperation(ctx, http.MethodPost, restoresPath, body)

	return operationPair{backup: backup, restore: operationResult(time.Since(restoreStart), restoreDoc, err)}
}

// operationResult answers the result of one operation that took took.
func operationResult(took time.Duration, doc operationDoc, err error) OperationResult {
	out := OperationResult{Ran: true, Ms: ms(took), State: doc.State, Error: ""}

	switch {
	case err != nil:
		out.Error = err.Error()
	case doc.State != stateSucceeded:
		out.Error = doc.Error
	default:
		// Succeeded: no error.
	}

	return out
}

// runOperation starts a backup or a restore with body and polls it until it
// leaves the running state.
func (t *target) runOperation(ctx context.Context, method, collection string, body []byte) (operationDoc, error) {
	startURL := t.firstURL(collection[:len(collection)-1])

	resp, err := t.do(ctx, request{
		method: method, url: startURL, auth: tfeScheme + t.token, mediaType: jsonMediaType, body: body,
	})
	if err != nil {
		return operationDoc{}, err
	}

	if resp.status != http.StatusOK && resp.status != http.StatusAccepted && resp.status != http.StatusCreated {
		return operationDoc{}, check(method, startURL, resp.status, http.StatusAccepted, resp.body)
	}

	var started operationDoc

	if err := json.Unmarshal(resp.body, &started); err != nil {
		return operationDoc{}, fmt.Errorf("decode %s answer: %w", startURL, err)
	}

	return t.pollOperation(ctx, collection, started.ID)
}

// pollOperation reads the operation id until it leaves the running state or
// defaultOperationWait passes.
func (t *target) pollOperation(ctx context.Context, collection, id string) (operationDoc, error) {
	readURL := t.firstURL(collection + url.PathEscape(id))
	deadline := time.Now().Add(defaultOperationWait)

	for {
		resp, err := t.do(ctx, request{
			method: http.MethodGet, url: readURL, auth: tfeScheme + t.token, mediaType: "", body: nil,
		})
		if err != nil {
			return operationDoc{}, err
		}

		if checkErr := check(http.MethodGet, readURL, resp.status, http.StatusOK, resp.body); checkErr != nil {
			return operationDoc{}, checkErr
		}

		var doc operationDoc

		if err := json.Unmarshal(resp.body, &doc); err != nil {
			return operationDoc{}, fmt.Errorf("decode %s answer: %w", readURL, err)
		}

		switch doc.State {
		case stateSucceeded:
			return doc, nil
		case stateFailed:
			return doc, fmt.Errorf("%s%s: state %s: %s: %w", collection, id, doc.State, doc.Error, errOperationFailed)
		default:
			// Running, checking or applying: the operation has not ended.
		}

		if time.Now().After(deadline) {
			return doc, fmt.Errorf("%s %s after %s: %w", collection, id, defaultOperationWait, errOperationTimeout)
		}

		select {
		case <-ctx.Done():
			return doc, fmt.Errorf("poll %s: %w", readURL, ctx.Err())
		case <-time.After(defaultOperationPollMs * time.Millisecond):
		}
	}
}
