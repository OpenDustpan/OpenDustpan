//go:build e2e

package e2e

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	reportDirEnv   = "E2E_REPORT_DIR"
	defaultReports = "report"
	summaryFile    = "summary.md"
	fullFile       = "full.md"
	reportExt      = ".json"
	// GitHub accepts at most 1024 KiB in a job summary.
	summaryLimit = 900 * 1024
	// maxFailureLines bounds how many failure lines summary.md keeps per run.
	maxFailureLines = 40
)

// Run is everything recorded for one CLI version on one store.
type Run struct {
	Tool           string       `json:"tool"`
	Label          string       `json:"label"`
	Store          Store        `json:"store"`
	Blobs          string       `json:"blobs"`
	Version        string       `json:"version"`
	ChecksumSource string       `json:"checksumSource"`
	Steps          []StepResult `json:"steps"`
	Requests       []Entry      `json:"requests"`
}

// ReportDir returns where reports are written.
func ReportDir() string {
	if dir := os.Getenv(reportDirEnv); dir != "" {
		return dir
	}

	return defaultReports
}

// NewRun collects the results of a finished scenario run.
func NewRun(env *Env) Run {
	cli := env.Install()

	return Run{
		Tool:           cli.Tool,
		Label:          cli.Label,
		Store:          env.Store(),
		Blobs:          env.BlobName(),
		Version:        cli.Version,
		ChecksumSource: cli.ChecksumSource,
		Steps:          env.Steps(),
		Requests:       env.Recorder.Entries(),
	}
}

// NotRun is the report of a version and store whose scenario could not run:
// the CLI did not install, the store is not configured, or the platform does
// not support it (skipped). It names the selected blob store, so a skipped S3
// run still shows what was selected.
func NotRun(tool, label string, store Store, reason string, skipped bool) Run {
	blobs, err := BlobStoreName()
	if err != nil {
		blobs = ""
	}

	return Run{
		Tool:           tool,
		Label:          label,
		Store:          store,
		Blobs:          blobs,
		Version:        "",
		ChecksumSource: "",
		Steps: []StepResult{{
			Name:     "install",
			Command:  tool + " " + label,
			Failure:  reason,
			Stderr:   "",
			ExitCode: 0,
			Passed:   false,
			Skipped:  skipped,
		}},
		Requests: nil,
	}
}

// WriteRun saves run as <dir>/<tool>-<label>-<store>-v<version>.json, with
// -<blobs> before the version when the run used no directory store. The label
// is part of the name so that "latest" and the same version pinned do not
// overwrite each other, and the store is part of it so runs on SQLite and
// PostgreSQL do not overwrite each other.
func WriteRun(dir string, run Run) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}

	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}

	base := run.Tool + "-" + strings.ReplaceAll(run.Label, "/", "_") + "-" + string(run.Store)
	if run.Blobs != "" && run.Blobs != blobFS {
		base += "-" + run.Blobs
	}

	name := filepath.Join(dir, base+"-v"+run.Version+reportExt)
	if err := os.WriteFile(name, append(data, '\n'), fileMode); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}

// ClearReports removes reports from earlier runs so the summary is not stale.
func ClearReports(dir string) error {
	old, err := filepath.Glob(filepath.Join(dir, "*"+reportExt))
	if err != nil {
		return fmt.Errorf("list old reports: %w", err)
	}

	for _, name := range append(old, filepath.Join(dir, summaryFile), filepath.Join(dir, fullFile)) {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old report: %w", err)
		}
	}

	return nil
}

// WriteSummary reads every run in dir and writes full.md with the full
// report and summary.md with the short report. It returns nothing when dir
// has no runs.
func WriteSummary(dir string) error {
	names, err := filepath.Glob(filepath.Join(dir, "*"+reportExt))
	if err != nil {
		return fmt.Errorf("list reports: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open report dir: %w", err)
	}

	defer ignoreErr(root.Close)

	runs := make([]Run, 0, len(names))

	for _, name := range names {
		data, err := root.ReadFile(filepath.Base(name))
		if err != nil {
			return fmt.Errorf("read report: %w", err)
		}

		var run Run
		if err := json.Unmarshal(data, &run); err != nil {
			return fmt.Errorf("decode %s: %w", name, err)
		}

		runs = append(runs, run)
	}

	if len(runs) == 0 {
		return nil
	}

	slices.SortFunc(runs, func(a, b Run) int {
		return cmp.Or(
			cmp.Compare(a.Tool, b.Tool),
			cmp.Compare(a.Store, b.Store),
			slices.Compare(semver(b.Version), semver(a.Version)),
		)
	})

	if err := os.WriteFile(filepath.Join(dir, fullFile), []byte(render(runs)), fileMode); err != nil {
		return fmt.Errorf("write full report: %w", err)
	}

	short := renderSummary(runs)
	if len(short) > summaryLimit {
		short = cutSummary(short)
	}

	if err := os.WriteFile(filepath.Join(dir, summaryFile), []byte(short), fileMode); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}

	return nil
}

// cutSummary cuts short at the limit on a line boundary and ends it with a
// line that names full.md.
func cutSummary(short string) string {
	note := "\nTruncated to 900 KiB, see full.md for the full report.\n"
	keep := summaryLimit - len(note)
	if keep <= 0 {
		return note
	}

	cut := short[:min(keep, len(short))]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		cut = cut[:i+1]
	}

	return cut + note
}

func render(runs []Run) string {
	var b strings.Builder

	b.WriteString("# End-to-end report\n")

	for _, tool := range []string{ToolPulumi, ToolTofu} {
		var mine []Run

		for i := range runs {
			if runs[i].Tool == tool {
				mine = append(mine, runs[i])
			}
		}

		if len(mine) > 0 {
			fmt.Fprintf(&b, "\n# %s\n\n", toolTitle(tool))
			renderTool(&b, mine)
		}
	}

	return b.String()
}

func renderSummary(runs []Run) string {
	var b strings.Builder

	b.WriteString("# End-to-end report\n")

	for _, tool := range []string{ToolPulumi, ToolTofu} {
		var mine []Run

		for i := range runs {
			if runs[i].Tool == tool {
				mine = append(mine, runs[i])
			}
		}

		if len(mine) > 0 {
			fmt.Fprintf(&b, "\n# %s\n\n", toolTitle(tool))
			renderSummaryTool(&b, mine)
		}
	}

	return b.String()
}

func renderSummaryTool(b *strings.Builder, runs []Run) {
	renderSteps(b, runs)
	renderMatrix(b, runs)

	for i := range runs {
		fmt.Fprintf(b, "\n## Failures: %s\n\n", versionTitle(runs[i]))
		b.WriteString(truncateFailures(failures(runs[i])))
	}

	for i := range runs {
		fmt.Fprintf(b, "\n## Request log: %s\n\nSee full.md for the request log.\n", versionTitle(runs[i]))
	}
}

// truncateFailures keeps the first maxFailureLines lines of text and ends it
// with a line that says how many lines were cut and names full.md.
func truncateFailures(text string) string {
	trimmed := strings.TrimSuffix(text, "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= maxFailureLines {
		return text
	}

	kept := strings.Join(lines[:maxFailureLines], "\n") + "\n"

	return fmt.Sprintf("%sCut %d lines, see full.md.\n", kept, len(lines)-maxFailureLines)
}

func toolTitle(tool string) string {
	if tool == ToolTofu {
		return "OpenTofu"
	}

	return "Pulumi CLI"
}

func renderTool(b *strings.Builder, runs []Run) {
	renderSteps(b, runs)
	renderMatrix(b, runs)

	for i := range runs {
		fmt.Fprintf(b, "\n## Failures: %s\n\n", versionTitle(runs[i]))
		b.WriteString(failures(runs[i]))
	}

	for i := range runs {
		fmt.Fprintf(
			b,
			"\n## Request log: %s\n\n| # | Step | Request | Status | Accept |\n|---|---|---|---|---|\n",
			versionTitle(runs[i]),
		)

		for j := range runs[i].Requests {
			e := &runs[i].Requests[j]
			target := e.Path
			if e.Query != "" {
				target += "?" + e.Query
			}

			fmt.Fprintf(b, "| %d | %s | `%s %s` | %d | `%s` |\n", e.Seq, e.Step, e.Method, target, e.Status, e.Accept)
		}
	}
}

func renderSteps(b *strings.Builder, runs []Run) {
	b.WriteString("## Steps\n\n| Step |")

	for i := range runs {
		fmt.Fprintf(b, " %s |", versionTitle(runs[i]))
	}

	b.WriteString("\n|---|" + strings.Repeat("---|", len(runs)) + "\n")

	for _, name := range stepNames(runs) {
		fmt.Fprintf(b, "| `%s` |", name)

		for i := range runs {
			fmt.Fprintf(b, " %s |", stepCell(runs[i], name))
		}

		b.WriteString("\n")
	}
}

func renderMatrix(b *strings.Builder, runs []Run) {
	b.WriteString("\n## Requests the CLI made\n\n")
	b.WriteString("Cells list the status codes returned. " +
		"4xx and 5xx mean the server does not yet do what the CLI expects.\n\n")
	b.WriteString("| Request |")

	for i := range runs {
		fmt.Fprintf(b, " %s |", versionTitle(runs[i]))
	}

	b.WriteString("\n|---|" + strings.Repeat("---|", len(runs)) + "\n")

	for _, key := range requestKeys(runs) {
		fmt.Fprintf(b, "| `%s` |", key)

		for i := range runs {
			fmt.Fprintf(b, " %s |", statusCell(runs[i], key))
		}

		b.WriteString("\n")
	}
}

func versionTitle(r Run) string {
	var base string

	switch {
	case r.Version == "":
		base = r.Label + " (not run)"
	case r.Label == Latest:
		base = "v" + r.Version + " (latest)"
	default:
		base = "v" + r.Version
	}

	if r.Store == "" {
		return base
	}

	base += " on " + string(r.Store)

	if r.Blobs != "" && r.Blobs != blobFS {
		base += " with " + r.Blobs
	}

	return base
}

func stepNames(runs []Run) []string {
	var names []string

	for i := range runs {
		for _, s := range runs[i].Steps {
			if !slices.Contains(names, s.Name) {
				names = append(names, s.Name)
			}
		}
	}

	return names
}

func stepCell(r Run, name string) string {
	for _, s := range r.Steps {
		if s.Name == name {
			switch {
			case s.Passed:
				return "pass"
			case s.Skipped:
				return "skipped"
			default:
				return "**FAIL**"
			}
		}
	}

	return "–"
}

func requestKey(e *Entry) string {
	return e.Method + " " + e.Path
}

func requestKeys(runs []Run) []string {
	var keys []string

	for i := range runs {
		for j := range runs[i].Requests {
			if k := requestKey(&runs[i].Requests[j]); !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}

	return keys
}

func statusCell(r Run, key string) string {
	var statuses []int

	for i := range r.Requests {
		if e := &r.Requests[i]; requestKey(e) == key && !slices.Contains(statuses, e.Status) {
			statuses = append(statuses, e.Status)
		}
	}

	if len(statuses) == 0 {
		return "–"
	}

	slices.Sort(statuses)

	parts := make([]string, len(statuses))
	for i, s := range statuses {
		parts[i] = strconv.Itoa(s)
	}

	return strings.Join(parts, ", ")
}

func failures(r Run) string {
	var b strings.Builder

	for _, s := range r.Steps {
		if s.Passed || s.Skipped {
			continue
		}

		fmt.Fprintf(&b, "- `%s`: %s", s.Command, s.Failure)

		if s.Stderr != "" {
			fmt.Fprintf(&b, "\n  ```\n  %s\n  ```", strings.ReplaceAll(strings.TrimSpace(s.Stderr), "\n", "\n  "))
		}

		b.WriteString("\n")
	}

	if b.Len() == 0 {
		return "None.\n"
	}

	return b.String()
}
