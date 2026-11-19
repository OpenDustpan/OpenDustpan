package compare

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

const (
	minSoakRows = 2 // a header and one sample
	rankP50     = 0.50
	rankP95     = 0.95

	bytesPerKiB = 1024
)

// ErrBadCSV means that the soak CSV does not have the expected shape.
var ErrBadCSV = errors.New("soak csv is not valid")

// kib rounds a byte count to whole KiB. The harness sizes vary by a few bytes,
// and a budget name must stay the same from run to run.
func kib(n int) int {
	return (n + bytesPerKiB/2) / bytesPerKiB
}

// single makes a measurement of one figure: the harness gives one value, so
// p50, p95 and max hold it.
func single(name string, v float64, unit, scope string) Measurement {
	return Measurement{Name: name, P50: v, P95: v, Max: v, Unit: unit, Scope: scope}
}

type loadCheckpoint struct {
	SizeBytes int     `json:"sizeBytes"`
	FullMs    float64 `json:"fullMs"`
	DeltaMs   float64 `json:"deltaMs"`
}

type loadImport struct {
	SizeBytes int     `json:"sizeBytes"`
	ImportMs  float64 `json:"importMs"`
}

type loadP50 struct {
	P50Ms float64 `json:"p50Ms"`
}

type loadLists struct {
	WorkspaceListP50Ms  float64 `json:"workspaceListP50Ms"`
	WorkspaceListFullMs float64 `json:"workspaceListFullMs"`
	InventoryMs         float64 `json:"inventoryMs"`
}

type loadJournalRun struct {
	EntriesPerBatch int     `json:"entriesPerBatch"`
	AppendP50Ms     float64 `json:"appendP50Ms"`
	AppendMaxMs     float64 `json:"appendMaxMs"`
	CompleteMs      float64 `json:"completeMs"`
}

type loadJournal struct {
	Runs []loadJournalRun `json:"runs"`
}

type loadReport struct {
	Checkpoints      []loadCheckpoint `json:"checkpoints"`
	Imports          []loadImport     `json:"imports"`
	WorkspaceLocks   *loadP50         `json:"workspaceLocks"`
	UpdateConflicts  *loadP50         `json:"updateConflicts"`
	NamespaceUpdates *loadP50         `json:"namespaceUpdates"`
	Lists            *loadLists       `json:"lists"`
	Journal          *loadJournal     `json:"journal"`
}

// ExtractLoad turns the report of the load harness into measurements. The
// harness reports one value for most figures, so those measurements hold the
// value in p50, p95 and max. It reads the fields it knows and ignores others.
func ExtractLoad(r io.Reader) ([]Measurement, error) {
	var rep loadReport
	if err := json.NewDecoder(io.LimitReader(r, maxInput)).Decode(&rep); err != nil {
		return nil, fmt.Errorf("read load report: %w", err)
	}
	const scope = "load"
	var ms []Measurement
	for _, c := range rep.Checkpoints {
		ms = append(ms,
			single(fmt.Sprintf("load.checkpoint.full.%dk", kib(c.SizeBytes)), c.FullMs, "ms", scope),
			single(fmt.Sprintf("load.checkpoint.delta.%dk", kib(c.SizeBytes)), c.DeltaMs, "ms", scope))
	}
	for _, i := range rep.Imports {
		ms = append(ms, single(fmt.Sprintf("load.import.%dk", kib(i.SizeBytes)), i.ImportMs, "ms", scope))
	}
	if rep.WorkspaceLocks != nil {
		ms = append(ms, single("load.workspace-lock.attempt", rep.WorkspaceLocks.P50Ms, "ms", scope))
	}
	if rep.UpdateConflicts != nil {
		ms = append(ms, single("load.update-conflict.start", rep.UpdateConflicts.P50Ms, "ms", scope))
	}
	if rep.NamespaceUpdates != nil {
		ms = append(ms, single("load.namespace-update.start", rep.NamespaceUpdates.P50Ms, "ms", scope))
	}
	if l := rep.Lists; l != nil {
		ms = append(ms,
			single("load.list.workspace-page", l.WorkspaceListP50Ms, "ms", scope),
			single("load.list.workspace-walk", l.WorkspaceListFullMs, "ms", scope),
			single("load.list.inventory", l.InventoryMs, "ms", scope))
	}
	if rep.Journal != nil {
		for _, j := range rep.Journal.Runs {
			ms = append(
				ms,
				Measurement{
					Name:  fmt.Sprintf("load.journal.append.%d", j.EntriesPerBatch),
					P50:   j.AppendP50Ms,
					P95:   j.AppendMaxMs,
					Max:   j.AppendMaxMs,
					Unit:  "ms",
					Scope: scope,
				},
				single(fmt.Sprintf("load.journal.complete.%d", j.EntriesPerBatch), j.CompleteMs, "ms", scope),
			)
		}
	}
	if len(ms) == 0 {
		return nil, ErrNoMeasurement
	}
	return ms, nil
}

// ExtractSoak turns the CSV of the soak run into one measurement for each
// series: p50, p95 and max over the samples.
func ExtractSoak(r io.Reader) ([]Measurement, error) {
	rows, err := csv.NewReader(bufio.NewReader(io.LimitReader(r, maxInput))).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read soak csv: %w", err)
	}
	if len(rows) < minSoakRows {
		return nil, ErrNoMeasurement
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	series := []struct{ header, name, unit string }{
		{"goroutines", "soak.goroutines", "count"},
		{"heap_bytes", "soak.heap", "bytes"},
		{"fds", "soak.fds", "count"},
		{"error_rate", "soak.error-rate", "ratio"},
	}
	var ms []Measurement
	for _, s := range series {
		idx, ok := col[s.header]
		if !ok {
			return nil, fmt.Errorf("no column %q: %w", s.header, ErrBadCSV)
		}
		var vals []float64
		for _, row := range rows[1:] {
			if idx >= len(row) {
				return nil, fmt.Errorf("a row has %d fields, want more than %d: %w", len(row), idx, ErrBadCSV)
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(row[idx]), 64)
			if err != nil || !validFigure(v) {
				return nil, fmt.Errorf("column %q holds %q, want a number: %w", s.header, row[idx], ErrBadCSV)
			}
			vals = append(vals, v)
		}
		slices.Sort(vals)
		ms = append(ms, Measurement{
			Name: s.name, P50: percentile(vals, rankP50), P95: percentile(vals, rankP95), Max: percentile(vals, 1),
			Unit: s.unit, Scope: "soak",
		})
	}
	return ms, nil
}

// percentile returns the nearest-rank percentile of sorted values.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(rank, 0)]
}

// ExtractFaults turns the test2json output of the fault tests into one
// measurement for each passing top-level test: its elapsed seconds. A failing
// test is no measurement; the test step itself fails the job.
func ExtractFaults(r io.Reader) ([]Measurement, error) {
	dec := json.NewDecoder(io.LimitReader(r, maxInput))
	var ms []Measurement
	for {
		var ev struct {
			Action  string  `json:"Action"`  //nolint:tagliatelle // wire name of go test -json
			Test    string  `json:"Test"`    //nolint:tagliatelle // wire name of go test -json
			Elapsed float64 `json:"Elapsed"` //nolint:tagliatelle // wire name of go test -json
		}
		if err := dec.Decode(&ev); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("read fault test output: %w", err)
		}
		if ev.Action != "pass" || ev.Test == "" || strings.Contains(ev.Test, "/") || !validFigure(ev.Elapsed) {
			continue
		}
		m := single("faults.test."+ev.Test, ev.Elapsed, "s", "faults")
		if !nameRE.MatchString(m.Name) {
			continue
		}
		ms = append(ms, m)
	}
	if len(ms) == 0 {
		return nil, ErrNoMeasurement
	}
	return ms, nil
}
