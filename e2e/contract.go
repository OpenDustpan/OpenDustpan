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
	// maxKeyDepth bounds how deep ResponseKeys goes into a JSON answer.
	maxKeyDepth = 4
)

var errContractFile = errors.New("contract file is wrong")

// ContractRoute is what one CLI version got from one route of the server in
// the e2e run: the statuses and the fields that every answer had.
//
// A route is a pattern of the server, for example
// "GET /api/user/stacks/{owner}/{project}/{stack}". Statuses holds every status
// the CLI got, so a probe that the server must answer 404 stays in the
// contract. ResponseKeys holds only the fields that every JSON answer with the
// status 2xx had (the path of a field has dots, and "[]" stands for the first
// element of an array). A field that is sometimes absent is not a contract.
type ContractRoute struct {
	Route        string   `json:"route"`
	Statuses     []int    `json:"statuses"`
	ResponseKeys []string `json:"responseKeys,omitempty"`
}

// Contract is the routes that one CLI version used in the e2e run.
type Contract struct {
	Tool    string          `json:"tool"`
	Version string          `json:"version"`
	Routes  []ContractRoute `json:"routes"`
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

// routeSample is what one CLI version got from one route.
type routeSample struct {
	statuses map[int]bool
	// keys counts, for each field, the JSON answers that had it.
	keys    map[string]int
	answers int
}

// add records one request of the CLI.
func (s *routeSample) add(entry *Entry) {
	s.statuses[entry.Status] = true

	if entry.Status < http.StatusOK || entry.Status >= http.StatusMultipleChoices {
		return
	}

	keys, ok := responseKeys(entry.ResponseBody)
	if !ok {
		return
	}

	s.answers++

	for _, name := range keys {
		s.keys[name]++
	}
}

// route turns the sample into its entry of the contract. A field stays only if
// every JSON answer had it.
func (s *routeSample) route(name string) ContractRoute {
	item := ContractRoute{Route: name, Statuses: nil, ResponseKeys: nil}

	for status := range s.statuses {
		item.Statuses = append(item.Statuses, status)
	}

	slices.Sort(item.Statuses)

	for field, count := range s.keys {
		if count == s.answers {
			item.ResponseKeys = append(item.ResponseKeys, field)
		}
	}

	slices.Sort(item.ResponseKeys)

	return item
}

// ObserveContracts builds one Contract for each tool and version in runs from
// the requests of the real CLI (by its User-Agent). The requests of the harness,
// the requests that no served route holds and the server errors do not count.
// It sorts all lists, so the files do not change from run to run.
func ObserveContracts(served []RouteRef, runs []Run) []Contract {
	type id struct{ tool, version string }

	samples := map[id]map[string]*routeSample{}

	for i := range runs {
		run := &runs[i]
		if run.Version == "" || cliUserAgent(run.Tool) == "" {
			continue
		}

		key := id{tool: run.Tool, version: run.Version}
		if samples[key] == nil {
			samples[key] = map[string]*routeSample{}
		}

		observeRun(served, run, samples[key])
	}

	out := make([]Contract, 0, len(samples))

	for key, routes := range samples {
		contract := Contract{Tool: key.tool, Version: key.version, Routes: make([]ContractRoute, 0, len(routes))}
		for name, got := range routes {
			contract.Routes = append(contract.Routes, got.route(name))
		}

		slices.SortFunc(contract.Routes, func(a, b ContractRoute) int { return cmp.Compare(a.Route, b.Route) })
		out = append(out, contract)
	}

	slices.SortFunc(out, func(a, b Contract) int {
		return cmp.Or(cmp.Compare(a.Tool, b.Tool), slices.Compare(semver(b.Version), semver(a.Version)))
	})

	return out
}

// observeRun adds the requests of the CLI in run to routes, by route.
func observeRun(served []RouteRef, run *Run, routes map[string]*routeSample) {
	for j := range run.Requests {
		entry := &run.Requests[j]
		if !isCLIRequest(served, run.Tool, entry) {
			continue
		}

		route, _ := matchRoute(served, entry.Method, entry.Path)

		got := routes[route.String()]
		if got == nil {
			got = &routeSample{statuses: map[int]bool{}, keys: map[string]int{}, answers: 0}
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

// responseKeys returns the field paths of a JSON object body. It answers false
// when the body is not a complete JSON object: the recorder keeps only the
// first bytes of a long answer, and a cut answer must not remove a field.
func responseKeys(body string) ([]string, bool) {
	var doc map[string]any

	decoder := json.NewDecoder(bytes.NewReader([]byte(body)))
	if err := decoder.Decode(&doc); err != nil || doc == nil {
		return nil, false
	}

	var out []string

	collectKeys("", doc, 1, &out)
	slices.Sort(out)

	return out, true
}

func collectKeys(prefix string, value any, depth int, out *[]string) {
	if depth > maxKeyDepth {
		return
	}

	switch typed := value.(type) {
	case map[string]any:
		for name, inner := range typed {
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}

			*out = append(*out, path)

			collectKeys(path, inner, depth+1, out)
		}
	case []any:
		if len(typed) > 0 {
			collectKeys(prefix+".[]", typed[0], depth+1, out)
		}
	default:
	}
}

// Missing returns what want has and got does not: a route, a status or a
// field. Each line names one thing. It returns nothing when got keeps the
// whole contract of want. Extra routes and fields in got are no problem.
func (want Contract) Missing(got Contract) []string {
	return want.lacking(got,
		"the CLI did not use this route", "the status %d is gone (now %v)", "the answer has no field %q")
}

// New returns what got has and want does not, in the same form as Missing. It
// is the list of new reads of a CLI version that has no contract yet.
func (want Contract) New(got Contract) []string {
	return got.lacking(want, "new route", "new status %d (the contract has %v)", "new field %q in the answer")
}

// lacking returns what the receiver has and other does not. The three formats say the
// route, the status and the field part of each line.
func (want Contract) lacking(other Contract, noRoute, noStatus, noField string) []string {
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

		for _, status := range route.Statuses {
			if !slices.Contains(seen.Statuses, status) {
				out = append(out, route.Route+": "+fmt.Sprintf(noStatus, status, seen.Statuses))
			}
		}

		for _, name := range route.ResponseKeys {
			if !slices.Contains(seen.ResponseKeys, name) {
				out = append(out, route.Route+": "+fmt.Sprintf(noField, name))
			}
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
