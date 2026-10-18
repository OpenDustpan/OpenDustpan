//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// stateAtResponse is the answer of the resource list at a time.
type stateAtResponse struct {
	Resources   []maskedStateItem `json:"resources"`
	Version     int               `json:"version"`
	EndedAt     int64             `json:"endedAt"`
	Running     int               `json:"running"`
	NameAtTime  string            `json:"nameAtTime"`
	CurrentName string            `json:"currentName"`
}

// stateAtPath returns the route of the resource list at the time at.
func stateAtPath(base string, at time.Time) string {
	return fmt.Sprintf("%s/state-at/%d/resources", base, at.UnixMilli())
}

// firstStateTime matches the time of the first state in the text of the 404.
var firstStateTime = regexp.MustCompile(`the first state of \S+ is at \S+ \((\d+) ms\)`)

// stateAtTimes are the two times of the masked scenario: before the first
// up, and after the first up ended and before the second up started.
type stateAtTimes struct {
	before  time.Time
	between time.Time
}

// checkMaskedStateAt proves the resource list at a time after two ups: a time
// between them answers version 1 with the URNs and masking of the version 1
// list, and a time before the first up answers 404 that names the time of the
// first update.
func checkMaskedStateAt(
	ctx context.Context, env *e2e.Env, org, secret string, times stateAtTimes,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		base := fmt.Sprintf("/api/stacks/%s/%s/masked", org, e2e.Project)

		body, err := maskedStateGet(ctx, env, stateAtPath(base, times.between))
		if err != nil {
			return err
		}

		if strings.Contains(body, secret) {
			return fmt.Errorf("%w: the list at a time holds the secret plain text", errUnexpected)
		}

		got, err := decodeStateAt(body)
		if err != nil {
			return err
		}

		if got.Version != 1 || got.Running != 0 || got.EndedAt > times.between.UnixMilli() {
			return fmt.Errorf(
				"%w: list between the ups = %+v, want version 1 and no running update",
				errUnexpected,
				got,
			)
		}

		if namesErr := checkUnrenamedNames(got); namesErr != nil {
			return namesErr
		}

		pinned, err := maskedStateGet(ctx, env, base+"/state/1/resources")
		if err != nil {
			return err
		}

		if err := checkSameURNs(got, pinned); err != nil {
			return err
		}

		return checkStateAtBeforeFirst(ctx, env, base, times.before)
	}
}

// checkUnrenamedNames proves that a stack that was never renamed has one name
// at every time.
func checkUnrenamedNames(got stateAtResponse) error {
	wantName := e2e.Project + "/masked"
	if got.NameAtTime != wantName || got.CurrentName != wantName {
		return fmt.Errorf(
			"%w: names of the list between the ups = %q and %q, want %q twice",
			errUnexpected, got.NameAtTime, got.CurrentName, wantName,
		)
	}

	return nil
}

// decodeStateAt decodes the answer of the resource list at a time.
func decodeStateAt(body string) (stateAtResponse, error) {
	var got stateAtResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		return got, fmt.Errorf("%w: decode the list at a time: %w", errUnexpected, err)
	}

	return got, nil
}

// checkSameURNs proves the list at a time holds the URNs of the list of the
// version that it names.
func checkSameURNs(got stateAtResponse, pinned string) error {
	want, err := maskedListURNs(pinned)
	if err != nil {
		return err
	}

	urns := make([]string, 0, len(got.Resources))
	for i := range got.Resources {
		urns = append(urns, got.Resources[i].URN)
	}

	slices.Sort(urns)
	slices.Sort(want)

	if !slices.Equal(urns, want) {
		return fmt.Errorf("%w: list between the ups holds %q, version 1 holds %q", errUnexpected, urns, want)
	}

	return nil
}

// checkStateAtBeforeFirst proves that a time before the first up answers 404
// with the time of the first update, which is not before that time.
func checkStateAtBeforeFirst(ctx context.Context, env *e2e.Env, base string, before time.Time) error {
	path := stateAtPath(base, before)

	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		return fmt.Errorf("%w: GET %s: %w", errUnexpected, path, err)
	}

	if status != http.StatusNotFound {
		return fmt.Errorf("%w: GET %s answers %d, want 404 (%s)", errUnexpected, path, status, body)
	}

	match := firstStateTime.FindStringSubmatch(body)
	if match == nil {
		return fmt.Errorf("%w: the 404 does not name the time of the first state (%s)", errUnexpected, body)
	}

	first, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || first < before.UnixMilli() {
		return fmt.Errorf(
			"%w: the 404 names the first state at %q, want a time after %d",
			errUnexpected,
			match[1],
			before.UnixMilli(),
		)
	}

	return nil
}

// checkNamesAfterRename proves that the state at a time answers the name of
// the stack at that time and its new name after a rename. The stack answers
// under newName, and the time is a time before the rename.
func checkNamesAfterRename(
	ctx context.Context, env *e2e.Env, org, newName string, at time.Time,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		base := fmt.Sprintf("/api/stacks/%s/%s/%s", org, e2e.Project, newName)

		body, err := maskedStateGet(ctx, env, stateAtPath(base, at))
		if err != nil {
			return err
		}

		got, err := decodeStateAt(body)
		if err != nil {
			return err
		}

		oldName, current := e2e.Project+"/masked", e2e.Project+"/"+newName
		if got.NameAtTime != oldName || got.CurrentName != current {
			return fmt.Errorf(
				"%w: names after a rename = %q and %q, want %q and %q",
				errUnexpected, got.NameAtTime, got.CurrentName, oldName, current,
			)
		}

		return nil
	}
}

// checkStateAtRunning proves that a time while an update runs answers the
// state before it and names the update. It holds an update through the API,
// as a running up would, then ends it.
func checkStateAtRunning(ctx context.Context, t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	base := fmt.Sprintf("/api/stacks/%s/%s/masked", org, e2e.Project)
	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, "masked")

	body, err := maskedStateGet(ctx, env, stateAtPath(base, time.Now()))
	if err == nil {
		var got stateAtResponse

		got, err = decodeStateAt(body)
		if err == nil && (got.Version != 2 || got.Running != 3) {
			err = fmt.Errorf(
				"%w: list while an update runs = %+v, want version 2 and running update 3",
				errUnexpected,
				got,
			)
		}
	}

	if err != nil {
		t.Error(err)
	}

	env.EndHeldUpdate(ctx, t, held, "cancelled") //nolint:misspell // The CLI's wire value; it must not change.
}

// stateAtData decodes the state version of the state-at document in e2e
// checks.
type stateAtData struct {
	ID string `json:"id"`
}

// stateAtMeta decodes the meta of the state-at document in e2e checks.
type stateAtMeta struct {
	RequestedBy string `json:"requested-by"` //nolint:tagliatelle // The wire name of this API.
	FinalizedAt int64  `json:"finalized-at"` //nolint:tagliatelle // The wire name of this API.
	NameAtTime  string `json:"name-at-time"` //nolint:tagliatelle // The wire name of this API.
	CurrentName string `json:"current-name"` //nolint:tagliatelle // The wire name of this API.
	Opaque      bool   `json:"opaque"`
}

// stateAtDoc decodes the state of a workspace at a time in e2e checks.
type stateAtDoc struct {
	Data     stateAtData          `json:"data"`
	Included []maskedResourceItem `json:"included"`
	Meta     stateAtMeta          `json:"meta"`
}

// stateAtVersions are the inputs of the OpenTofu state-at checks: the
// workspace, its state versions after the first and the second apply, and a
// time before the first apply and a time between the two applies.
type stateAtVersions struct {
	before    time.Time
	between   time.Time
	workspace string
	first     string
	second    string
}

// stateAtPathTofu returns the route of the state of a workspace at a time.
func stateAtPathTofu(workspaceID string, at time.Time) string {
	return fmt.Sprintf("/api/v2/workspaces/%s/state-at/%d", workspaceID, at.UnixMilli())
}

// firstTofuStateTime matches the time of the first state version in the text
// of the 404.
var firstTofuStateTime = regexp.MustCompile(`the first state version of \S+ is at \S+ \((\d+) ms\)`)

// checkOpaqueStateAt proves the answer for a state the client encrypted:
// opaque, with no resource.
func checkOpaqueStateAt(body string) error {
	var doc stateAtDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("decode state at a time: %w", err)
	}

	if !doc.Meta.Opaque || len(doc.Included) != 0 || doc.Data.ID == "" {
		return fmt.Errorf("%w: state at a time = %s, want opaque with no resource", errUnexpected, body)
	}

	return nil
}

// checkStateAtVersion returns the check of one state-at answer: the state
// version wantID with masked resources and no secret in plain text. It stores
// the finalize time of the answer in finalized.
func checkStateAtVersion(name, wantID, wantName string, finalized *int64) func(string) error {
	return func(body string) error {
		var doc stateAtDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			return fmt.Errorf("decode state at a time: %w", err)
		}

		if doc.Data.ID != wantID || doc.Meta.Opaque || len(doc.Included) == 0 || doc.Meta.RequestedBy == "" {
			return fmt.Errorf("%w: state at %s = %s, want %s", errUnexpected, name, body, wantID)
		}

		if strings.Contains(body, maskedSecretV1) || strings.Contains(body, maskedSecretV2) {
			return fmt.Errorf("%w: state at %s holds a secret in plain text", errUnexpected, name)
		}

		if doc.Meta.NameAtTime != wantName || doc.Meta.CurrentName != wantName {
			return fmt.Errorf(
				"%w: names of the state at %s = %q and %q, want %q twice",
				errUnexpected, name, doc.Meta.NameAtTime, doc.Meta.CurrentName, wantName,
			)
		}

		*finalized = doc.Meta.FinalizedAt

		return nil
	}
}

// checkFirstStateTime returns the check of the 404 body: its text names the
// finalize time of the first state version.
func checkFirstStateTime(firstAt *int64) func(string) error {
	return func(body string) error {
		match := firstTofuStateTime.FindStringSubmatch(body)
		if len(match) != 2 || match[1] != strconv.FormatInt(*firstAt, 10) {
			return fmt.Errorf("%w: 404 body = %s, want the first finalize time %d ms", errUnexpected, body, *firstAt)
		}

		return nil
	}
}

// maskedStateAtChecks proves the state at a time after two applies: a time
// between them answers the first state version with masked resources, now
// answers the second one, and a time before the first apply answers 404 that
// names the finalize time of the first state version.
func maskedStateAtChecks(ctx context.Context, t *testing.T, env *e2e.Env, in stateAtVersions) {
	t.Helper()

	wsID := env.WorkspaceIDByName(ctx, t, in.workspace)

	var firstAt, secondAt int64

	for _, spec := range []e2e.APIStepSpec{
		{
			Check:     checkStateAtVersion("a time between two applies", in.first, in.workspace, &firstAt),
			Name:      "masked state at a time between two applies",
			Method:    http.MethodGet,
			Path:      stateAtPathTofu(wsID, in.between),
			Body:      "",
			Want:      http.StatusOK,
			UserAgent: "",
			Scheme:    "",
		},
		{
			Check:     checkStateAtVersion("now", in.second, in.workspace, &secondAt),
			Name:      "masked state at a time now",
			Method:    http.MethodGet,
			Path:      stateAtPathTofu(wsID, time.Now()),
			Body:      "",
			Want:      http.StatusOK,
			UserAgent: "",
			Scheme:    "",
		},
		{
			Check:     checkFirstStateTime(&firstAt),
			Name:      "masked state at a time before the first apply",
			Method:    http.MethodGet,
			Path:      stateAtPathTofu(wsID, in.before),
			Body:      "",
			Want:      http.StatusNotFound,
			UserAgent: "",
			Scheme:    "",
		},
	} {
		env.APIStepA(ctx, t, spec)
	}

	if secondAt <= firstAt {
		t.Fatalf("finalize times = %d ms then %d ms, want the second after the first", firstAt, secondAt)
	}
}

// pulumiRenameNamesSteps renames the masked stack, proves that the state at
// a time before the rename and the inventory at that time name the stack by
// the name it had then and by its new name, and renames it back, so that the
// later steps find it under its first name. between is a time when the stack
// existed under its first name.
func pulumiRenameNamesSteps(ctx context.Context, t *testing.T, env *e2e.Env, org string, between time.Time) {
	t.Helper()

	const renamed = "masked-new"

	qualified := fmt.Sprintf("%s/%s/masked", org, e2e.Project)
	newQualified := fmt.Sprintf("%s/%s/%s", org, e2e.Project, renamed)
	names := unitAtNames{tool: "pulumi", atTime: e2e.Project + "/masked", current: e2e.Project + "/" + renamed}

	env.Step(ctx, t, step("stack rename masked", nil, "stack", "rename", newQualified, "--stack", qualified))
	env.Step(ctx, t, step(
		"masked state at a time names the stack by both names",
		checkNamesAfterRename(ctx, env, org, renamed, between),
		"stack", "--show-name", "--stack", newQualified,
	))
	env.Step(ctx, t, step(
		"the inventory at a time names the renamed stack by both names",
		checkUnitAtRenamed(ctx, env, org, between, names),
		"stack", "--show-name", "--stack", newQualified,
	))
	env.Step(ctx, t, step("stack rename masked back", nil, "stack", "rename", qualified, "--stack", newQualified))
}
