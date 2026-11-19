// Command perfcompare checks the measurements of the nightly performance job
// against perf/budgets.json and keeps one regression issue for each
// measurement that is over its budget.
//
//	perfcompare extract-load   <load-report.json   >measurements.json
//	perfcompare extract-soak   <soak.csv           >measurements.json
//	perfcompare extract-faults <go-test-json       >measurements.json
//	perfcompare check -budgets perf/budgets.json -run-id ID [-issues] measurements.json...
//
// The check command prints one table of the breaches and exits with 1 when a
// measurement is over its budget or an input is not valid. With -issues it
// also opens, updates and closes issues through the gh command, which gets its
// access from GH_TOKEN. It writes nothing outside the issue tracker of the
// repository that gh names, and it sends no data anywhere else.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/perf/compare"
)

var (
	errUsage      = errors.New("use one of: extract-load, extract-soak, extract-faults, check")
	errUnknownCmd = errors.New("unknown command")
	errCheckFlags = errors.New("check needs -run-id and at least one measurements file")
	errOverBudget = errors.New("measurements are over their budget")
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "perfcompare:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	extract := map[string]func(io.Reader) ([]compare.Measurement, error){
		"extract-load":   compare.ExtractLoad,
		"extract-soak":   compare.ExtractSoak,
		"extract-faults": compare.ExtractFaults,
	}
	if f, ok := extract[args[0]]; ok {
		ms, err := f(in)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(ms); err != nil {
			return fmt.Errorf("write measurements: %w", err)
		}
		return nil
	}
	if args[0] != "check" {
		return fmt.Errorf("%q: %w", args[0], errUnknownCmd)
	}
	return check(ctx, args[1:], out, errOut)
}

// readFile reads one input file. The path comes from the workflow, which owns
// the command line; compare checks the content, not the path.
func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // CLI argument that the workflow sets
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

func loadInputs(budgetsPath string, paths []string) (compare.Budgets, []compare.Measurement, error) {
	raw, err := readFile(budgetsPath)
	if err != nil {
		return nil, nil, err
	}
	budgets, err := compare.ParseBudgets(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	var all []compare.Measurement
	for _, path := range paths {
		raw, err := readFile(path)
		if err != nil {
			return nil, nil, err
		}
		ms, err := compare.ParseMeasurements(bytes.NewReader(raw))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		all = append(all, ms...)
	}
	return budgets, all, nil
}

func check(ctx context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	budgetsPath := fs.String("budgets", "perf/budgets.json", "budgets file")
	runID := fs.String("run-id", "", "id of the workflow run")
	withIssues := fs.Bool("issues", false, "open, update and close issues through gh")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if *runID == "" || fs.NArg() == 0 {
		return errCheckFlags
	}
	budgets, all, err := loadInputs(*budgetsPath, fs.Args())
	if err != nil {
		return err
	}
	res, err := compare.Evaluate(budgets, all)
	if err != nil {
		return fmt.Errorf("evaluate: %w", err)
	}
	var warn strings.Builder
	for _, name := range res.Stale {
		fmt.Fprintf(&warn, "warning: budget %s has no measurement in this run\n", name)
	}
	summary := fmt.Sprintf("%d measurements, %d with a budget, %d record-only\n\n%s",
		len(all), len(res.Checked), len(res.RecordOnly), compare.Table(res, *runID))
	if _, err := io.WriteString(errOut, warn.String()); err != nil {
		return fmt.Errorf("write warnings: %w", err)
	}
	if _, err := io.WriteString(out, summary); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	if *withIssues {
		if err := compare.Sync(ctx, ghIssues{}, res, *runID); err != nil {
			return fmt.Errorf("sync issues: %w", err)
		}
	}
	if len(res.Breaches) > 0 {
		return fmt.Errorf("%d: %w; show the run with: gh run view %s", len(res.Breaches), errOverBudget, *runID)
	}
	return nil
}

// ghIssues is the issue tracker port on the gh command. Each call uses an
// argument list, never a shell. The title comes from compare, which accepts
// only names from its allowlist.
type ghIssues struct{}

func gh(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...) //nolint:gosec // fixed program name, argument list, no shell
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil, fmt.Errorf("gh %s: %w: %s", args[0], err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh %s: %w", args[0], err)
	}
	return out, nil
}

func (ghIssues) Find(ctx context.Context, title string) (int, bool, error) {
	out, err := gh(ctx, "", "issue", "list", "--state", "open", "--search", `"`+title+`" in:title`,
		"--json", "number,title", "--limit", "100")
	if err != nil {
		return 0, false, err
	}
	var issues []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return 0, false, fmt.Errorf("read gh issue list: %w", err)
	}
	for _, i := range issues {
		if i.Title == title { // exact match: the search is only a filter
			return i.Number, true, nil
		}
	}
	return 0, false, nil
}

func (ghIssues) Open(ctx context.Context, title, body string) error {
	_, err := gh(ctx, body, "issue", "create", "--title", title, "--body-file", "-")
	return err
}

func (ghIssues) Comment(ctx context.Context, number int, body string) error {
	_, err := gh(ctx, body, "issue", "comment", strconv.Itoa(number), "--body-file", "-")
	return err
}

func (ghIssues) Close(ctx context.Context, number int, body string) error {
	_, err := gh(ctx, body, "issue", "close", strconv.Itoa(number), "--comment", body)
	return err
}
