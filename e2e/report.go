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
	reportExt      = ".json"
)

// Run is everything recorded for one CLI version.
type Run struct {
	Tool           string       `json:"tool"`
	Label          string       `json:"label"`
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
		Version:        cli.Version,
		ChecksumSource: cli.ChecksumSource,
		Steps:          env.Steps(),
		Requests:       env.Recorder.Entries(),
	}
}

// WriteRun saves run as <dir>/<tool>-v<version>.json.
func WriteRun(dir string, run Run) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}

	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}

	name := filepath.Join(dir, run.Tool+"-v"+run.Version+reportExt)
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

	for _, name := range append(old, filepath.Join(dir, summaryFile)) {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old report: %w", err)
		}
	}

	return nil
}

// WriteSummary reads every run in dir and writes summary.md: a step table and
// an endpoint matrix per version. It returns nothing when dir has no runs.
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
		return cmp.Or(cmp.Compare(a.Tool, b.Tool), slices.Compare(semver(b.Version), semver(a.Version)))
	})

	if err := os.WriteFile(filepath.Join(dir, summaryFile), []byte(render(runs)), fileMode); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}

	return nil
}

func render(runs []Run) string {
	var b strings.Builder

	b.WriteString("# End-to-end report\n")

	for _, tool := range []string{ToolPulumi, ToolTofu} {
		var mine []Run

		for _, r := range runs {
			if r.Tool == tool {
				mine = append(mine, r)
			}
		}

		if len(mine) > 0 {
			fmt.Fprintf(&b, "\n# %s\n\n", toolTitle(tool))
			renderTool(&b, mine)
		}
	}

	return b.String()
}

func toolTitle(tool string) string {
	if tool == ToolTofu {
		return "OpenTofu"
	}

	return "Pulumi CLI"
}

func renderTool(b *strings.Builder, runs []Run) {
	b.WriteString("## Steps\n\n| Step |")

	for _, r := range runs {
		fmt.Fprintf(b, " %s |", versionTitle(r))
	}

	b.WriteString("\n|---|" + strings.Repeat("---|", len(runs)) + "\n")

	for _, name := range stepNames(runs) {
		fmt.Fprintf(b, "| `%s` |", name)

		for _, r := range runs {
			fmt.Fprintf(b, " %s |", stepCell(r, name))
		}

		b.WriteString("\n")
	}

	b.WriteString("\n## Requests the CLI made\n\n")
	b.WriteString("Cells list the status codes returned. " +
		"4xx and 5xx mean the server does not yet do what the CLI expects.\n\n")
	b.WriteString("| Request |")

	for _, r := range runs {
		fmt.Fprintf(b, " %s |", versionTitle(r))
	}

	b.WriteString("\n|---|" + strings.Repeat("---|", len(runs)) + "\n")

	for _, key := range requestKeys(runs) {
		fmt.Fprintf(b, "| `%s` |", key)

		for _, r := range runs {
			fmt.Fprintf(b, " %s |", statusCell(r, key))
		}

		b.WriteString("\n")
	}

	for _, r := range runs {
		fmt.Fprintf(b, "\n## Failures: %s\n\n", versionTitle(r))
		b.WriteString(failures(r))
	}

	for _, r := range runs {
		fmt.Fprintf(
			b,
			"\n## Request log: %s\n\n| # | Step | Request | Status | Accept |\n|---|---|---|---|---|\n",
			versionTitle(r),
		)

		for i := range r.Requests {
			e := &r.Requests[i]
			target := e.Path
			if e.Query != "" {
				target += "?" + e.Query
			}

			fmt.Fprintf(b, "| %d | %s | `%s %s` | %d | `%s` |\n", e.Seq, e.Step, e.Method, target, e.Status, e.Accept)
		}
	}
}

func versionTitle(r Run) string {
	if r.Label == Latest {
		return "v" + r.Version + " (latest)"
	}

	return "v" + r.Version
}

func stepNames(runs []Run) []string {
	var names []string

	for _, r := range runs {
		for _, s := range r.Steps {
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
			if s.Passed {
				return "pass"
			}

			return "**FAIL**"
		}
	}

	return "–"
}

func requestKey(e *Entry) string {
	return e.Method + " " + e.Path
}

func requestKeys(runs []Run) []string {
	var keys []string

	for _, r := range runs {
		for i := range r.Requests {
			if k := requestKey(&r.Requests[i]); !slices.Contains(keys, k) {
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
		if s.Passed {
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
