package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// plan holds the repetition counts of the run.
type plan struct {
	Sizes        []sizeReps `json:"sizes"`
	SequenceReps int        `json:"sequenceReps"`
	SequenceBody int        `json:"sequenceBodyBytes"`
	SweepObjects int        `json:"sweepObjects"`
	// Load harness runs of a profile and of a limit step: the timed
	// repetitions, the units of the backup data set and the bound of the
	// run in minutes.
	LoadReps     int `json:"loadReps"`
	LoadDataset  int `json:"loadDatasetUnits"`
	LoadMinutes  int `json:"loadMaxMinutes"`
	LimitReps    int `json:"limitReps"`
	LimitDataset int `json:"limitDatasetUnits"`
	LimitMinutes int `json:"limitMaxMinutes"`
}

// Body sizes of the blob measurement.
const (
	size10KiB  = 10 << 10
	size1MiB   = 1 << 20
	size100MiB = 100 << 20
	// sequenceBody is the body of each modeled write: a checkpoint of a
	// small program.
	sequenceBody = 256 << 10
	// sweepObjects is the orphan count of each sweep.
	sweepObjects = 1000
)

// Repetition counts of the full run and of the quick run.
const (
	fullSmallReps   = 100
	fullMidReps     = 50
	fullLargeReps   = 8
	fullSequences   = 50
	quickSmallReps  = 20
	quickMidReps    = 10
	quickLargeReps  = 2
	quickSequences  = 10
	fullLoadReps    = 20
	quickLoadReps   = 5
	loadMinutes     = 60
	limitReps       = 3
	limitMinutes    = 20
	resultsFileName = "results.json"
	reportFileName  = "results.md"
	loadDirName     = "load"
)

// fullPlan answers the repetition counts of a full run.
func fullPlan() plan {
	return plan{
		Sizes: []sizeReps{
			{Bytes: size10KiB, Reps: fullSmallReps},
			{Bytes: size1MiB, Reps: fullMidReps},
			{Bytes: size100MiB, Reps: fullLargeReps},
		},
		SequenceReps: fullSequences, SequenceBody: sequenceBody, SweepObjects: sweepObjects,
		LoadReps: fullLoadReps, LoadDataset: fullLoadReps, LoadMinutes: loadMinutes,
		LimitReps: limitReps, LimitDataset: limitReps, LimitMinutes: limitMinutes,
	}
}

// quickPlan answers the repetition counts of a CI smoke run.
func quickPlan() plan {
	return plan{
		Sizes: []sizeReps{
			{Bytes: size10KiB, Reps: quickSmallReps},
			{Bytes: size1MiB, Reps: quickMidReps},
			{Bytes: size100MiB, Reps: quickLargeReps},
		},
		SequenceReps: quickSequences, SequenceBody: sequenceBody, SweepObjects: sweepObjects,
		LoadReps: quickLoadReps, LoadDataset: quickLoadReps, LoadMinutes: loadMinutes,
		LimitReps: limitReps, LimitDataset: limitReps, LimitMinutes: limitMinutes,
	}
}

// secrets holds the throwaway credentials of the run, read from files that
// scripts/bench-s3.sh writes. No method prints them.
type secrets struct {
	byName map[string]string
}

// readSecrets reads each credential file of dir.
func readSecrets(dir string) (secrets, error) {
	if dir == "" {
		return secrets{}, fmt.Errorf("%w: -secrets is required", errBadFlag)
	}

	out := secrets{byName: map[string]string{}}

	for _, name := range []string{"minio-access", "minio-secret", "garage-access", "garage-secret", "pg-password"} {
		data, err := readIn(dir, name)
		if err != nil {
			return secrets{}, fmt.Errorf("read the credential file %s: %w", name, err)
		}

		out.byName[name] = strings.TrimSpace(string(data))
	}

	return out, nil
}

// access answers the access key of store.
func (s secrets) access(store string) string { return s.byName[store+"-access"] }

// secret answers the secret key of store.
func (s secrets) secret(store string) string { return s.byName[store+"-secret"] }

// redact removes each credential from text, so a captured error line never
// holds one.
func (s secrets) redact(text string) string {
	for _, v := range s.byName {
		if v != "" {
			text = strings.ReplaceAll(text, v, "[redacted]")
		}
	}

	return text
}

// readIn reads the file name inside dir. The name cannot leave dir.
func readIn(dir, name string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}

	data, readErr := root.ReadFile(name)

	if err := errors.Join(readErr, root.Close()); err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	return data, nil
}

// writeIn writes data to the file name inside dir, which it creates.
func writeIn(dir, name string, data []byte) error {
	const (
		dirPerm  = 0o750
		filePerm = 0o600
	)

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("make %s: %w", dir, err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}

	writeErr := root.WriteFile(name, data, filePerm)

	if err := errors.Join(writeErr, root.Close()); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	return nil
}

// readResults reads results.json of the run in dir.
func readResults(dir string) (*results, error) {
	data, err := readIn(dir, resultsFileName)
	if err != nil {
		return nil, fmt.Errorf("%w: run s3bench setup first", err)
	}

	var res results

	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("decode %s: %w", resultsFileName, err)
	}

	if res.Conditional == nil {
		res.Conditional = map[string]conditionalCheck{}
	}

	return &res, nil
}

// writeResults writes results.json and results.md of the run to dir.
func writeResults(dir string, res *results) error {
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}

	if err := writeIn(dir, resultsFileName, append(data, '\n')); err != nil {
		return err
	}

	return writeIn(dir, reportFileName, []byte(renderMarkdown(res)))
}

// flowStats mirrors the FlowStats of the load harness report.
type flowStats struct {
	Samples    int     `json:"samples"`
	P50Ms      float64 `json:"p50Ms"`
	P99Ms      float64 `json:"p99Ms"`
	MaxMs      float64 `json:"maxMs"`
	Errors     int     `json:"errors"`
	FirstError string  `json:"firstError,omitempty"`
}

// operationResult mirrors the OperationResult of the load harness report.
type operationResult struct {
	Ran   bool    `json:"ran"`
	Ms    float64 `json:"ms"`
	State string  `json:"state,omitempty"`
	Error string  `json:"error,omitempty"`
}

// flowReport mirrors the parts of the blob flow report that the bench reads.
type flowReport struct {
	DatasetMs      float64         `json:"datasetMs"`
	Backup         operationResult `json:"backup"`
	Restore        operationResult `json:"restore"`
	StateUpload    flowStats       `json:"stateUpload"`
	StateCycle     flowStats       `json:"stateCycle"`
	CheckpointSave flowStats       `json:"checkpointSave"`
	UpdateCycle    flowStats       `json:"updateCycle"`
	DurationMs     float64         `json:"durationMs"`
}

// loadResult is one run of the load harness: its report, or the end of its
// log when it wrote none.
type loadResult struct {
	Report *flowReport `json:"report,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// maxLogTail is how much of the log of a failed harness run the results
// keep.
const maxLogTail = 1024

// readLoad reads the load harness report of step on store from the load/
// directory of dir: <step>-<store>.json, or the end of <step>-<store>.log
// when the run wrote no report.
func readLoad(dir, step, store string, creds secrets) *loadResult {
	base := filepath.Join(dir, loadDirName)
	name := step + "-" + store

	data, err := readIn(base, name+".json")
	if err == nil {
		var rep flowReport

		if decodeErr := json.Unmarshal(data, &rep); decodeErr != nil {
			return &loadResult{Report: nil, Error: "decode the report: " + decodeErr.Error()}
		}

		return &loadResult{Report: &rep, Error: ""}
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return &loadResult{Report: nil, Error: err.Error()}
	}

	log, err := readIn(base, name+".log")
	if err != nil {
		return nil
	}

	text := strings.TrimSpace(string(log))
	if len(text) > maxLogTail {
		text = text[len(text)-maxLogTail:]
	}

	return &loadResult{Report: nil, Error: creds.redact(text)}
}

// render reads the load harness reports into results.json and writes
// results.md.
func render(o options) error {
	creds, err := readSecrets(o.secretsDir)
	if err != nil {
		return err
	}

	res, err := readResults(o.out)
	if err != nil {
		return err
	}

	for i := range res.Profiles {
		for j := range res.Profiles[i].Stores {
			s := &res.Profiles[i].Stores[j]
			s.Load = readLoad(o.out, res.Profiles[i].Profile.Name, s.Store, creds)
		}
	}

	for i := range res.Limits {
		l := &res.Limits[i]
		l.Load = readLoad(o.out, l.Profile.Name, l.Store, creds)
	}

	return writeResults(o.out, res)
}
