//go:build e2e

package e2e

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// ContractUpdateEnv is the variable that, set to 1, makes the suite write
	// the contract files of the versions it ran, instead of checking them.
	ContractUpdateEnv = "E2E_CONTRACT_UPDATE"
	// ContractDir is where the contract files are, next to this package.
	ContractDir = "contract"
)

var errContractFile = errors.New("contract file is wrong")

// ContractAnswer is what one CLI version got from one route with one status.
//
// ResponseKeys holds the field paths that every JSON answer with this status
// had. A path has dots, and "[]" stands for an element of an array: a path
// below "[]" is in every element of the array. Headers holds the names of the
// response headers that every answer had, and "Content-Type: <media type>".
// A field or a header that some answers lack is not part of the contract.
//
// Cut is true when some answers were longer than the recorder keeps. The
// fields of a cut answer are unknown, so ResponseKeys holds only the fields of
// the answers that were whole, and the check of this status is weaker.
type ContractAnswer struct {
	Status       int      `json:"status"`
	ResponseKeys []string `json:"responseKeys,omitempty"`
	Headers      []string `json:"headers,omitempty"`
	Cut          bool     `json:"cut,omitempty"`
}

// ContractRoute is what one CLI version got from one route of the server,
// for example "GET /api/user/stacks/{owner}/{project}/{stack}".
//
// Answers holds a status only if every tested store saw it. A status that
// only one store saw is a race or a fault of that run and is not a contract.
type ContractRoute struct {
	Route   string           `json:"route"`
	Answers []ContractAnswer `json:"answers"`
}

// Contract is the routes that one CLI version used in the e2e run.
type Contract struct {
	Tool    string          `json:"tool"`
	Version string          `json:"version"`
	Routes  []ContractRoute `json:"routes"`
}

// statusSample is what the answers with one status had.
type statusSample struct {
	// keys and headers count, for each name, the answers that had it.
	keys    map[string]int
	headers map[string]int
	// whole counts the answers whose body was kept whole and was JSON.
	whole int
	// total counts all answers.
	total int
	cut   bool
}

// routeSample is what one CLI version got from one route in one store.
type routeSample map[int]*statusSample

// add records one request of the CLI.
func (r routeSample) add(entry *Entry) {
	got := r[entry.Status]
	if got == nil {
		got = &statusSample{keys: map[string]int{}, headers: map[string]int{}, whole: 0, total: 0, cut: false}
		r[entry.Status] = got
	}

	got.total++

	for _, name := range entry.ResponseHeaders {
		got.headers[name]++
	}

	if entry.ResponseCut {
		got.cut = true

		return
	}

	keys, ok := responseKeys(entry.ResponseBody)
	if !ok {
		return
	}

	got.whole++

	for _, name := range keys {
		got.keys[name]++
	}
}

// answer turns the sample into its entry of the contract. A name stays only if
// every answer had it.
func (s *statusSample) answer(status int) ContractAnswer {
	item := ContractAnswer{Status: status, ResponseKeys: nil, Headers: nil, Cut: s.cut}

	for name, count := range s.keys {
		if count == s.whole {
			item.ResponseKeys = append(item.ResponseKeys, name)
		}
	}

	for name, count := range s.headers {
		if count == s.total {
			item.Headers = append(item.Headers, name)
		}
	}

	slices.Sort(item.ResponseKeys)
	slices.Sort(item.Headers)

	return item
}

// cliUserAgent returns the User-Agent prefix that the CLI of tool sends, or ""
// for a tool that is not known.
func cliUserAgent(tool string) string {
	switch tool {
	case ToolPulumi:
		return "pulumi-cli/"
	case ToolTofu:
		return "OpenTofu/"
	default:
		return ""
	}
}

// ScenarioRuns returns the runs of runs that the scenario of TestCLIs wrote: the
// label is a version of the matrix of its tool, with or without the suffix of
// the delta checkpoint runs. Other tests write reports to the same directory,
// for example the import test with its wrong-token step. They run when the
// whole suite runs and not when one test runs, and they finish at times that
// no one controls, so the contract must not read them.
func ScenarioRuns(runs []Run, tools ...Tool) []Run {
	var out []Run

	for i := range runs {
		for j := range tools {
			if runs[i].Tool != tools[j].Name {
				continue
			}

			label := strings.TrimSuffix(strings.TrimSuffix(runs[i].Label, "-delta-on"), "-delta-off")
			if slices.Contains(tools[j].Versions(), label) {
				out = append(out, runs[i])
			}
		}
	}

	return out
}

// ObserveContracts builds one Contract for each tool and version in runs from
// the requests of the real CLI (by its User-Agent). The requests of the harness,
// the requests that no served route holds and the server errors (5xx) do not
// count. A route counts if one store used it. A status of a route counts only
// if every store that used the route saw it. It sorts all lists, so the files
// do not change from run to run.
func ObserveContracts(served []RouteRef, runs []Run) []Contract {
	type id struct{ tool, version string }

	// stores holds, for each version and store, the samples of each route.
	stores := map[id]map[Store]map[string]routeSample{}

	for i := range runs {
		run := &runs[i]
		if run.Version == "" || cliUserAgent(run.Tool) == "" {
			continue
		}

		key := id{tool: run.Tool, version: run.Version}
		if stores[key] == nil {
			stores[key] = map[Store]map[string]routeSample{}
		}

		if stores[key][run.Store] == nil {
			stores[key][run.Store] = map[string]routeSample{}
		}

		observeRun(served, run, stores[key][run.Store])
	}

	out := make([]Contract, 0, len(stores))

	for key, perStore := range stores {
		out = append(out, mergeStores(key.tool, key.version, perStore))
	}

	slices.SortFunc(out, func(a, b Contract) int {
		return cmp.Or(cmp.Compare(a.Tool, b.Tool), slices.Compare(semver(b.Version), semver(a.Version)))
	})

	return out
}

// mergeStores builds the contract of one version from the samples of its
// stores.
func mergeStores(tool, version string, perStore map[Store]map[string]routeSample) Contract {
	contract := Contract{Tool: tool, Version: version, Routes: nil}

	names := map[string]bool{}

	for _, routes := range perStore {
		for name := range routes {
			names[name] = true
		}
	}

	for name := range names {
		// The answers of each store that used the route.
		var used []map[int]ContractAnswer

		for _, routes := range perStore {
			sample, ok := routes[name]
			if !ok {
				continue
			}

			answers := map[int]ContractAnswer{}
			for status, got := range sample {
				answers[status] = got.answer(status)
			}

			used = append(used, answers)
		}

		if answers := commonAnswers(used); len(answers) > 0 {
			contract.Routes = append(contract.Routes, ContractRoute{Route: name, Answers: answers})
		}
	}

	slices.SortFunc(contract.Routes, func(a, b ContractRoute) int { return cmp.Compare(a.Route, b.Route) })

	return contract
}

// commonAnswers keeps the statuses that every store saw. For such a status it
// keeps the fields and the headers that every store saw, and Cut if any store
// had a cut answer.
func commonAnswers(used []map[int]ContractAnswer) []ContractAnswer {
	if len(used) == 0 {
		return nil
	}

	var out []ContractAnswer

	for status, first := range used[0] {
		merged := first
		common := true

		for _, other := range used[1:] {
			seen, ok := other[status]
			if !ok {
				common = false

				break
			}

			merged.ResponseKeys = intersect(merged.ResponseKeys, seen.ResponseKeys)
			merged.Headers = intersect(merged.Headers, seen.Headers)
			merged.Cut = merged.Cut || seen.Cut
		}

		if common {
			out = append(out, merged)
		}
	}

	slices.SortFunc(out, func(a, b ContractAnswer) int { return cmp.Compare(a.Status, b.Status) })

	return out
}

// intersect returns the names in both lists.
func intersect(a, b []string) []string {
	var out []string

	for _, name := range a {
		if slices.Contains(b, name) {
			out = append(out, name)
		}
	}

	return out
}

// observeRun adds the requests of the CLI in run to routes, by route.
func observeRun(served []RouteRef, run *Run, routes map[string]routeSample) {
	for j := range run.Requests {
		entry := &run.Requests[j]
		if !isCLIRequest(served, run.Tool, entry) {
			continue
		}

		route, _ := matchRoute(served, entry.Method, entry.Path)

		got := routes[route.String()]
		if got == nil {
			got = routeSample{}
			routes[route.String()] = got
		}

		got.add(entry)
	}
}

// isCLIRequest reports whether entry is a request of the CLI of tool to a
// served route, and not a server error. The harness sends its own requests
// with other User-Agent values, and they say nothing about the CLI.
func isCLIRequest(served []RouteRef, tool string, entry *Entry) bool {
	if !strings.HasPrefix(entry.UserAgent, cliUserAgent(tool)) || entry.Status >= http.StatusInternalServerError {
		return false
	}

	_, ok := matchRoute(served, entry.Method, entry.Path)

	return ok
}

// responseKeys returns the field paths of a JSON body: an object or an array
// at the top. It answers false when the body is not one complete JSON value.
// The caller must not give it a cut body: a cut answer is not JSON, and its
// fields must not be taken for the fields of the server.
func responseKeys(body string) ([]string, bool) {
	var doc any

	decoder := json.NewDecoder(bytes.NewReader([]byte(body)))
	if err := decoder.Decode(&doc); err != nil || decoder.More() {
		return nil, false
	}

	out := keyPaths("", doc)
	slices.Sort(out)

	return out, true
}

// keyPaths returns the field paths below value. The depth is not capped: the
// recorder keeps at most maxCapturedBody bytes, so a whole answer has a bounded
// depth. A path below an array is a field that every element of the array has.
func keyPaths(prefix string, value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		out := make([]string, 0, len(typed))

		for name, inner := range typed {
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}

			out = append(out, path)
			out = append(out, keyPaths(path, inner)...)
		}

		return out
	case []any:
		path := "[]"
		if prefix != "" {
			path = prefix + ".[]"
		}

		return append([]string{path}, commonPaths(path, typed)...)
	default:
		return nil
	}
}

// commonPaths returns the field paths that every element of an array has.
func commonPaths(path string, elements []any) []string {
	var common []string

	for i, element := range elements {
		paths := keyPaths(path, element)
		if i == 0 {
			common = paths
		} else {
			common = intersect(common, paths)
		}
	}

	return common
}

// Missing returns what want has and got does not: a route, a status, a field or
// a header. Each line names one thing. It returns nothing when got keeps the
// whole contract of want. Extra routes, statuses, fields and headers in got
// are no problem.
func (want Contract) Missing(got Contract) []string {
	return want.lacking(got,
		"the CLI did not use this route", "the status %d is gone", "status %d: the answer has no field %q",
		"status %d: the answer has no header %q")
}

// New returns what got has and want does not, in the same form as Missing. It
// is the list of new reads of a CLI version that has no contract yet.
func (want Contract) New(got Contract) []string {
	return got.lacking(want,
		"new route", "new status %d", "status %d: new field %q in the answer", "status %d: new header %q")
}

// lacking returns what the receiver has and other does not. The four formats
// say the route, the status, the field and the header part of each line.
func (want Contract) lacking(other Contract, noRoute, noStatus, noField, noHeader string) []string {
	byRoute := map[string]ContractRoute{}
	for _, route := range other.Routes {
		byRoute[route.Route] = route
	}

	var out []string

	for _, route := range want.Routes {
		seen, ok := byRoute[route.Route]
		if !ok {
			out = append(out, route.Route+": "+noRoute)

			continue
		}

		for _, answer := range route.Answers {
			index := slices.IndexFunc(seen.Answers, func(a ContractAnswer) bool { return a.Status == answer.Status })
			if index < 0 {
				out = append(out, route.Route+": "+fmt.Sprintf(noStatus, answer.Status))

				continue
			}

			out = append(
				out,
				lackingNames(
					route.Route,
					answer.Status,
					answer.ResponseKeys,
					seen.Answers[index].ResponseKeys,
					noField,
				)...,
			)
			out = append(
				out,
				lackingNames(route.Route, answer.Status, answer.Headers, seen.Answers[index].Headers, noHeader)...,
			)
		}
	}

	return out
}

// lackingNames returns one line, made with format, for each name in want that
// got lacks.
func lackingNames(route string, status int, want, got []string, format string) []string {
	var out []string

	for _, name := range want {
		if !slices.Contains(got, name) {
			out = append(out, route+": "+fmt.Sprintf(format, status, name))
		}
	}

	return out
}

// ContractPath returns the file of the contract of one tool and version.
func ContractPath(tool, version string) string {
	return filepath.Join(ContractDir, tool+"-"+version+".json")
}

// ReadContracts reads every contract file of tool in dir. The files are named
// by ContractPath. The newest version comes first.
func ReadContracts(dir, tool string) ([]Contract, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("open contract directory: %w", err)
	}

	defer ignoreErr(root.Close)

	names, err := filepath.Glob(filepath.Join(dir, tool+"-*.json"))
	if err != nil {
		return nil, fmt.Errorf("list contracts: %w", err)
	}

	out := make([]Contract, 0, len(names))

	for _, name := range names {
		data, err := root.ReadFile(filepath.Base(name))
		if err != nil {
			return nil, fmt.Errorf("read contract: %w", err)
		}

		var contract Contract
		if err := json.Unmarshal(data, &contract); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}

		if contract.Tool != tool {
			return nil, fmt.Errorf("%w: %s holds the tool %q", errContractFile, name, contract.Tool)
		}

		out = append(out, contract)
	}

	slices.SortFunc(out, func(a, b Contract) int { return slices.Compare(semver(b.Version), semver(a.Version)) })

	return out, nil
}

// WriteContract writes the file of contract in dir. Encoding is stable, so the
// file changes only when the contract changes.
func WriteContract(dir string, contract Contract) error {
	data, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return fmt.Errorf("encode contract: %w", err)
	}

	if err := os.MkdirAll(dir, fileMode); err != nil {
		return fmt.Errorf("make contract directory: %w", err)
	}

	name := filepath.Join(dir, filepath.Base(ContractPath(contract.Tool, contract.Version)))
	if err := os.WriteFile(name, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write contract: %w", err)
	}

	return nil
}
