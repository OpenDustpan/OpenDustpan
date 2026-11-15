//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// checkDeltaMaskedState proves the masked state read after delta saves on
// the delta-enabled server: two ups of a program with a secret output and
// a stack reference, then the resource list, one resource detail and the
// outputs on latest and on version 1. It runs on the delta-on server only,
// so the default scenario does not change.
func checkDeltaMaskedState(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	org := e2e.Namespace
	qualified := fmt.Sprintf("%s/%s/masked-delta", org, e2e.Project)
	base := fmt.Sprintf("/api/stacks/%s/%s/masked-delta", org, e2e.Project)

	env.WritePulumiProject(t, maskedStateProgram(org, ""))
	env.Step(ctx, t, step("stack init masked delta", nil, "stack", "init", qualified, "--no-select"))
	env.Step(
		ctx,
		t,
		step(
			"config set secret for masked delta",
			nil,
			"config", "set", "--secret", "--stack", qualified, "apiKey", maskedStateSecret,
		),
	)
	env.Step(ctx, t, step("up masked delta 1", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	env.WritePulumiProject(t, maskedStateProgram(org, "hello-again"))
	env.Step(ctx, t, step("up masked delta 2", nil, "up", "--yes", "--non-interactive", "--stack", qualified))
	exported := env.Step(
		ctx,
		t,
		step("stack export masked delta", nil, "stack", "export", "--stack", qualified),
	).Stdout
	firstExported := env.Step(
		ctx,
		t,
		step("stack export masked delta version 1", nil, "stack", "export", "--version", "1", "--stack", qualified),
	).Stdout
	env.Step(ctx, t, step(
		"masked delta state read",
		checkDeltaMaskedBodies(ctx, env, base, exported, firstExported),
		"stack", "--show-name", "--stack", qualified,
	))
	env.Step(ctx, t, step("stack rm masked delta", nil, "stack", "rm", "--yes", "--force", "--stack", qualified))
}

// checkDeltaMaskedBodies proves the masked bodies on the delta stack: the
// recorder shows checkpointdelta saves for the masked ups, the latest list
// holds the same URNs as the export with no envelope fields, one resource
// detail answers masked inputs and outputs, the outputs answer the secret
// masked, and the version 1 reads answer the unwrapped deployment too.
func checkDeltaMaskedBodies(
	ctx context.Context, env *e2e.Env, base, exported, firstExported string,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		if !recordedDeltaPatchInSteps(env, []string{"up masked delta 1", "up masked delta 2"}) {
			return fmt.Errorf("%w: no PATCH checkpointdelta answered 200 during the masked delta ups", errUnexpected)
		}

		stackURN, latestErr := checkDeltaMaskedLatest(ctx, env, base, exported)
		if latestErr != nil {
			return latestErr
		}

		outputs, outputsErr := maskedStateGet(ctx, env, base+"/state/latest/outputs")
		if outputsErr != nil {
			return outputsErr
		}

		if outputsErr := checkDeltaMaskedOutputs(outputs); outputsErr != nil {
			return outputsErr
		}

		return checkDeltaMaskedVersionOne(ctx, env, base, firstExported, stackURN)
	}
}

// checkDeltaMaskedLatest proves the latest reads: the list holds the same
// URNs as the export with no envelope fields, and one resource detail
// answers masked inputs and outputs. It returns the stack URN for the
// version 1 reads.
func checkDeltaMaskedLatest(ctx context.Context, env *e2e.Env, base, exported string) (string, error) {
	latest, latestErr := maskedStateGet(ctx, env, base+"/state/latest/resources")
	if latestErr != nil {
		return "", latestErr
	}

	if listErr := checkDeltaListMatchesExport(latest, exported); listErr != nil {
		return "", listErr
	}

	stackURN, urnErr := deltaMaskedStackURN(latest)
	if urnErr != nil {
		return "", urnErr
	}

	detail, detailErr := maskedStateGet(ctx, env, base+"/state/latest/resources/"+url.PathEscape(stackURN))
	if detailErr != nil {
		return "", detailErr
	}

	if detailErr := checkDeltaMaskedDetail(detail); detailErr != nil {
		return "", detailErr
	}

	return stackURN, nil
}

// checkDeltaMaskedDetail proves one resource detail answers its inputs and
// outputs with the secret masked and no envelope fields.
func checkDeltaMaskedDetail(detail string) error {
	if err := checkDeltaNoEnvelope(detail); err != nil {
		return err
	}

	if err := maskedStateCheckLeaks(detail, maskedStateSecret); err != nil {
		return err
	}

	if err := maskedStateCheckMarkers(detail); err != nil {
		return err
	}

	var doc struct {
		Inputs  map[string]any `json:"inputs"`
		Outputs map[string]any `json:"outputs"`
		Masked  []string       `json:"masked"`
	}

	if err := json.Unmarshal([]byte(detail), &doc); err != nil {
		return fmt.Errorf("%w: decode resource detail: %w", errUnexpected, err)
	}

	if doc.Inputs == nil || doc.Outputs == nil {
		return fmt.Errorf("%w: resource detail holds no inputs and outputs", errUnexpected)
	}

	if !maskedStateHasMaskedWith(detail, "/outputs/") {
		return fmt.Errorf("%w: resource detail holds no masked secret in outputs", errUnexpected)
	}

	return nil
}

// checkDeltaMaskedOutputs proves the stack outputs answer the secret masked
// with no plain text, ciphertext or signature key.
func checkDeltaMaskedOutputs(outputs string) error {
	if err := checkDeltaNoEnvelope(outputs); err != nil {
		return err
	}

	if err := maskedStateCheckLeaks(outputs, maskedStateSecret); err != nil {
		return err
	}

	if err := maskedStateCheckMarkers(outputs); err != nil {
		return err
	}

	if !maskedStateHasSecret(outputs) {
		return fmt.Errorf("%w: outputs hold no masked secret", errUnexpected)
	}

	return nil
}

// checkDeltaMaskedVersionOne proves the version 1 reads answer the
// unwrapped deployment: the list holds the same URNs as the export of
// version 1, and the detail and outputs answer masked with no leaks.
func checkDeltaMaskedVersionOne(
	ctx context.Context, env *e2e.Env, base, firstExported, stackURN string,
) error {
	first, firstErr := maskedStateGet(ctx, env, base+"/state/1/resources")
	if firstErr != nil {
		return firstErr
	}

	if listErr := checkDeltaListMatchesExport(first, firstExported); listErr != nil {
		return listErr
	}

	firstDetail, detailErr := maskedStateGet(ctx, env, base+"/state/1/resources/"+url.PathEscape(stackURN))
	if detailErr != nil {
		return detailErr
	}

	if detailErr := checkDeltaMaskedDetail(firstDetail); detailErr != nil {
		return detailErr
	}

	firstOutputs, outputsErr := maskedStateGet(ctx, env, base+"/state/1/outputs")
	if outputsErr != nil {
		return outputsErr
	}

	return checkDeltaMaskedOutputs(firstOutputs)
}

// maskedStateProgram returns the Pulumi.yaml of the masked state scenario:
// a secret output from config and a stack reference whose outputs are
// secrets. The reference name stays plain: the engine admits no secret
// input on any usable built-in resource, it rejects a secret name with
// "'name' property must be a string", so the unit tests cover input
// masking while this scenario covers real service secrets in outputs.
// greeting renders only in the second version, which is the one change
// between the two ups.
func maskedStateProgram(org, greeting string) string {
	program := "name: " + e2e.Project + "\nruntime: yaml\n" +
		"config:\n" +
		"  apiKey:\n" +
		"    type: string\n" +
		"    secret: true\n" +
		"outputs:\n" +
		"  apiKeyOut:\n" +
		"    fn::secret: ${apiKey}\n"

	if greeting != "" {
		program += "  greeting: " + greeting + "\n"
	}

	return program + "resources:\n" +
		"  ref:\n" +
		"    type: pulumi:pulumi:StackReference\n" +
		"    properties:\n" +
		"      name: " + org + "/" + e2e.Project + "/other\n" +
		"    options:\n" +
		"      additionalSecretOutputs:\n" +
		"      - outputs\n"
}

// maskedStateItem is one entry of the masked resource list in e2e checks.
type maskedStateItem struct {
	URN     string `json:"urn"`
	Type    string `json:"type"`
	Changed bool   `json:"changed"`
}

// maskedStateList is the resource list body in e2e checks.
type maskedStateList struct {
	Resources []maskedStateItem `json:"resources"`
}

// checkMaskedState proves the masked routes on the masked stack: no body
// holds the secret plain text, a ciphertext field or the secret signature;
// each marker path is in masked; the outputs hold the secret output masked;
// the stack and reference details hold their secret outputs masked while
// the plain reference input reads back verbatim; and changed is true only
// on the stack resource, which the greeting changed in version 2.
func checkMaskedState(
	ctx context.Context, env *e2e.Env, org, secret string,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		base := fmt.Sprintf("/api/stacks/%s/%s/masked", org, e2e.Project)

		lists, err := fetchMaskedLists(ctx, env, base)
		if err != nil {
			return err
		}

		if changedErr := checkMaskedChanged(lists.latest, lists.first, lists.second); changedErr != nil {
			return changedErr
		}

		stackURN, refURNs, err := maskedStateURNs(lists.latest)
		if err != nil {
			return err
		}

		stackDetail, err := maskedStateGet(ctx, env, base+"/state/latest/resources/"+url.PathEscape(stackURN))
		if err != nil {
			return err
		}

		refDetails := make([]string, 0, len(refURNs))

		for _, urn := range refURNs {
			detail, fetchErr := maskedStateGet(ctx, env, base+"/state/latest/resources/"+url.PathEscape(urn))
			if fetchErr != nil {
				return fetchErr
			}

			refDetails = append(refDetails, detail)
		}

		outputs, err := maskedStateGet(ctx, env, base+"/state/latest/outputs")
		if err != nil {
			return err
		}

		bodies := maskedStateBodies{
			latest: lists.latest, first: lists.first, second: lists.second,
			stack: stackDetail, refs: refDetails, outputs: outputs,
			refName: org + "/" + e2e.Project + "/other",
		}

		return assertMaskedBodies(ctx, env, base, bodies, secret)
	}
}

// checkMaskedReveal proves the reveal call (ADR 41 decisions 4 and 5) on
// the masked stack: reveal of one masked output path answers the secret
// value the program set, and the audit list
// holds the entry of the kind reveal with the path. A path outside the
// masked set answers 404 with no value.
func checkMaskedReveal(
	ctx context.Context, tb testing.TB, env *e2e.Env, org, secret string,
) func(e2e.Result) error {
	tb.Helper()

	return func(e2e.Result) error {
		base := fmt.Sprintf("/api/stacks/%s/%s/masked", org, e2e.Project)

		path, err := maskedOutputPath(ctx, env, base)
		if err != nil {
			return err
		}

		if err := checkPulumiReveal(ctx, tb, env, revealCheck{org: org, path: path, secret: secret}); err != nil {
			return err
		}

		return checkPulumiRevealProbe(ctx, tb, env, base, secret)
	}
}

// checkMaskedStateFilter proves the resource search of the masked stack:
// filter[q] matches by type and by URN, never by an attribute value or a
// secret value, and paging applies after the filter. It runs after an apply
// with resources, on the masked stack.
func checkMaskedStateFilter(
	ctx context.Context, env *e2e.Env, org, secret string,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		base := fmt.Sprintf("/api/stacks/%s/%s/masked", org, e2e.Project)

		latest, err := maskedStateGet(ctx, env, base+"/state/latest/resources")
		if err != nil {
			return err
		}

		var full maskedStateList

		if decodeErr := json.Unmarshal([]byte(latest), &full); decodeErr != nil {
			return fmt.Errorf("%w: decode latest list: %w", errUnexpected, decodeErr)
		}

		if len(full.Resources) < 2 {
			return fmt.Errorf(
				"%w: latest list holds %d resources, want at least the stack and a reference",
				errUnexpected,
				len(full.Resources),
			)
		}

		stackURN, refURNs, err := maskedStateURNs(latest)
		if err != nil {
			return err
		}

		if err := checkMaskedFilterByType(ctx, env, base, full); err != nil {
			return err
		}

		if err := checkMaskedFilterByURN(ctx, env, base, stackURN, refURNs[0]); err != nil {
			return err
		}

		return checkMaskedFilterPaging(ctx, env, base, secret)
	}
}

// maskedFilterList fetches the resource list with filter q.
func maskedFilterList(ctx context.Context, env *e2e.Env, base, q string) (maskedStateList, string, error) {
	path := base + "/state/latest/resources?filter[q]=" + url.QueryEscape(q)

	body, err := maskedStateGet(ctx, env, path)
	if err != nil {
		return maskedStateList{}, "", err
	}

	var list maskedStateList

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		return maskedStateList{}, "", fmt.Errorf("%w: decode filtered list: %w", errUnexpected, err)
	}

	return list, body, nil
}

// maskedFilterTypeItem is one entry of a filtered resource list when only
// the type matters.
type maskedFilterTypeItem struct {
	Type string `json:"type"`
}

// maskedFilterTypeList is a filtered resource list with only the types
// decoded.
type maskedFilterTypeList struct {
	Resources []maskedFilterTypeItem `json:"resources"`
}

// checkMaskedFilterByType proves filter[q] with a type substring answers
// only reference resources, fewer than the full list.
func checkMaskedFilterByType(
	ctx context.Context, env *e2e.Env, base string, full maskedStateList,
) error {
	list, body, err := maskedFilterList(ctx, env, base, "StackReference")
	if err != nil {
		return err
	}

	if len(list.Resources) == 0 {
		return fmt.Errorf("%w: filter by type matched no resources", errUnexpected)
	}

	if len(list.Resources) >= len(full.Resources) {
		return fmt.Errorf(
			"%w: filter by type holds %d resources, want fewer than the full %d",
			errUnexpected,
			len(list.Resources),
			len(full.Resources),
		)
	}

	var raw maskedFilterTypeList

	if decodeErr := json.Unmarshal([]byte(body), &raw); decodeErr != nil {
		return fmt.Errorf("%w: decode filtered list types: %w", errUnexpected, decodeErr)
	}

	for _, res := range raw.Resources {
		if !strings.Contains(res.Type, "StackReference") {
			return fmt.Errorf("%w: filter by type answers resource of type %q", errUnexpected, res.Type)
		}
	}

	return nil
}

// checkMaskedFilterByURN proves filter[q] with a URN substring answers the
// named resource, while an attribute value and the secret value answer none.
func checkMaskedFilterByURN(
	ctx context.Context, env *e2e.Env, base, stackURN, refURN string,
) error {
	list, _, err := maskedFilterList(ctx, env, base, stackURN)
	if err != nil {
		return err
	}

	if len(list.Resources) != 1 || list.Resources[0].URN != stackURN {
		return fmt.Errorf("%w: filter by URN answers %+v, want only the stack", errUnexpected, list.Resources)
	}

	refList, _, err := maskedFilterList(ctx, env, base, refURN)
	if err != nil {
		return err
	}

	if len(refList.Resources) != 1 || refList.Resources[0].URN != refURN {
		return fmt.Errorf("%w: filter by URN answers %+v, want only the reference", errUnexpected, refList.Resources)
	}

	for _, q := range []string{"hello-again", maskedStateSecret, "acme"} {
		// "hello-again" is the plain greeting output of version 2,
		// "acme" starts the plain reference input, and the secret is the
		// secret value: none may match, because the filter never reads
		// attribute values. The URNs hold no "acme": a Pulumi URN names
		// the stack and the project, never the organization.
		got, body, err := maskedFilterList(ctx, env, base, q)
		if err != nil {
			return err
		}

		if len(got.Resources) != 0 {
			return fmt.Errorf("%w: filter %q matched %d resources, want none", errUnexpected, q, len(got.Resources))
		}

		if strings.Contains(body, maskedStateSecret) {
			return fmt.Errorf("%w: filtered body leaks the secret", errUnexpected)
		}
	}

	return nil
}

// checkMaskedFilterPaging proves paging applies after the filter: with a
// filter that matches the stack and the references, pages of one hold only
// matching resources without overlap.
func checkMaskedFilterPaging(ctx context.Context, env *e2e.Env, base, secret string) error {
	firstBody, err := maskedStateGet(
		ctx,
		env,
		base+"/state/latest/resources?filter[q]="+url.QueryEscape("pulumi:pulumi:Stack")+"&limit=1",
	)
	if err != nil {
		return err
	}

	var first struct {
		Resources  []maskedStateItem `json:"resources"`
		NextCursor string            `json:"nextCursor"`
	}

	if decodeErr := json.Unmarshal([]byte(firstBody), &first); decodeErr != nil {
		return fmt.Errorf("%w: decode first filtered page: %w", errUnexpected, decodeErr)
	}

	if len(first.Resources) != 1 || first.NextCursor == "" {
		return fmt.Errorf("%w: first filtered page = %s, want 1 match with a cursor", errUnexpected, firstBody)
	}

	if strings.Contains(firstBody, secret) {
		return fmt.Errorf("%w: filtered page leaks the secret", errUnexpected)
	}

	secondBody, err := maskedStateGet(
		ctx,
		env,
		base+"/state/latest/resources?filter[q]="+url.QueryEscape("pulumi:pulumi:Stack")+
			"&limit=1&cursor="+url.QueryEscape(first.NextCursor),
	)
	if err != nil {
		return err
	}

	var second struct {
		Resources  []maskedStateItem `json:"resources"`
		NextCursor string            `json:"nextCursor"`
	}

	if decodeErr := json.Unmarshal([]byte(secondBody), &second); decodeErr != nil {
		return fmt.Errorf("%w: decode second filtered page: %w", errUnexpected, decodeErr)
	}

	if len(second.Resources) == 0 {
		return fmt.Errorf("%w: second filtered page holds no resources: %s", errUnexpected, secondBody)
	}

	for _, res := range second.Resources {
		if res.URN == first.Resources[0].URN {
			return fmt.Errorf("%w: filtered pages overlap on %q", errUnexpected, res.URN)
		}
	}

	return nil
}

// maskedStateLists are the three resource list bodies the masked check
// reads.
type maskedStateLists struct {
	latest string
	first  string
	second string
}

// maskedStateBodies are the masked bodies the masked check asserts on: the
// three resource lists, the stack resource detail, one detail per stack
// reference and the stack outputs. refName is the plain name input each
// reference detail must carry verbatim.
type maskedStateBodies struct {
	latest  string
	first   string
	second  string
	stack   string
	refs    []string
	outputs string
	refName string
}

// maskedStateCheckRefInputs proves the reference detail carries its plain
// name input verbatim: masking leaves non-secret inputs alone, so no
// false-positive marker rewrites them.
func maskedStateCheckRefInputs(detail, refName string) error {
	var doc struct {
		Inputs map[string]any `json:"inputs"`
	}

	if err := json.Unmarshal([]byte(detail), &doc); err != nil {
		return fmt.Errorf("%w: decode reference detail: %w", errUnexpected, err)
	}

	if doc.Inputs["name"] != refName {
		return fmt.Errorf(
			"%w: reference inputs name = %v, want the plain name %q",
			errUnexpected,
			doc.Inputs["name"],
			refName,
		)
	}

	return nil
}

// maskedStateGet fetches path with the admin token: the status and the body
// on success, or an error that names the path.
func maskedStateGet(ctx context.Context, env *e2e.Env, path string) (string, error) {
	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		return "", fmt.Errorf("%w: GET %s: %w", errUnexpected, path, err)
	}

	if status != http.StatusOK {
		return "", fmt.Errorf("%w: GET %s answers %d, want %d (%s)", errUnexpected, path, status, http.StatusOK, body)
	}

	return body, nil
}

// checkMaskedChanged proves the changed markers: every resource of version
// 1 is new, while version 2 and latest changed only the stack resource,
// which carries the greeting of version 2. The deployment also holds the
// default provider resource, which never changes.
func checkMaskedChanged(latest, first, second string) error {
	var firstList maskedStateList

	if err := json.Unmarshal([]byte(first), &firstList); err != nil {
		return fmt.Errorf("%w: decode version 1 list: %w", errUnexpected, err)
	}

	if len(firstList.Resources) < 2 {
		return fmt.Errorf(
			"%w: version 1 list holds %d resources, want at least the stack and a reference",
			errUnexpected,
			len(firstList.Resources),
		)
	}

	for _, res := range firstList.Resources {
		if !res.Changed {
			return fmt.Errorf(
				"%w: version 1 resource %q changed = false, want true for a new resource",
				errUnexpected,
				res.URN,
			)
		}
	}

	for _, version := range []struct {
		name string
		body string
	}{{"version 2", second}, {"latest", latest}} {
		if err := checkMaskedLatestChanged(version.name, version.body); err != nil {
			return err
		}
	}

	return nil
}

// checkMaskedLatestChanged proves the changed markers of one list of the
// second version: only the stack resource changed.
func checkMaskedLatestChanged(name, body string) error {
	var list maskedStateList

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		return fmt.Errorf("%w: decode %s list: %w", errUnexpected, name, err)
	}

	if len(list.Resources) < 2 {
		return fmt.Errorf(
			"%w: %s list holds %d resources, want at least the stack and a reference",
			errUnexpected,
			name,
			len(list.Resources),
		)
	}

	changed := 0

	for _, res := range list.Resources {
		wantChanged := res.Type == "pulumi:pulumi:Stack"
		if res.Changed != wantChanged {
			return fmt.Errorf(
				"%w: %s resource %q of type %q changed = %t, want %t",
				errUnexpected,
				name,
				res.URN,
				res.Type,
				res.Changed,
				wantChanged,
			)
		}

		if res.Changed {
			changed++
		}
	}

	if changed != 1 {
		return fmt.Errorf("%w: %s list changed %d resources, want only the stack", errUnexpected, name, changed)
	}

	return nil
}

// maskedStateURNs returns the URN of the stack resource and of every stack
// reference in the list.
func maskedStateURNs(latest string) (string, []string, error) {
	var list maskedStateList

	if err := json.Unmarshal([]byte(latest), &list); err != nil {
		return "", nil, fmt.Errorf("%w: decode latest list: %w", errUnexpected, err)
	}

	var stackURN string

	var refURNs []string

	for _, res := range list.Resources {
		switch res.Type {
		case "pulumi:pulumi:Stack":
			stackURN = res.URN
		case "pulumi:pulumi:StackReference":
			refURNs = append(refURNs, res.URN)
		default:
		}
	}

	if stackURN == "" {
		return "", nil, fmt.Errorf("%w: latest list holds no stack resource", errUnexpected)
	}

	if len(refURNs) == 0 {
		return "", nil, fmt.Errorf("%w: latest list holds no reference resource", errUnexpected)
	}

	return stackURN, refURNs, nil
}

// maskedStateCheckLeaks fails when body holds the secret plain text, a
// ciphertext field or the secret signature key.
func maskedStateCheckLeaks(body, secret string) error {
	for _, leaked := range []string{secret, "ciphertext", maskedStateSigKey} {
		if strings.Contains(body, leaked) {
			return fmt.Errorf("%w: masked body holds %q", errUnexpected, leaked)
		}
	}

	return nil
}

// maskedStateCheckMarkers proves each secret marker in body names a path in
// its masked list: masked is authoritative.
func maskedStateCheckMarkers(body string) error {
	var doc struct {
		Masked []string `json:"masked"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode masked body: %w", errUnexpected, err)
	}

	var raw any

	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return fmt.Errorf("%w: decode masked body: %w", errUnexpected, err)
	}

	return maskedStateWalkMarkers(raw, doc.Masked)
}

// maskedStateWalkMarkers proves each secret marker in v names a path in
// masked.
func maskedStateWalkMarkers(v any, masked []string) error {
	switch typed := v.(type) {
	case map[string]any:
		return maskedStateWalkObject(typed, masked)
	case []any:
		for _, elem := range typed {
			if err := maskedStateWalkMarkers(elem, masked); err != nil {
				return err
			}
		}

		return nil
	default:
		return nil
	}
}

// maskedStateWalkObject proves each secret marker in obj names a path in
// masked.
func maskedStateWalkObject(obj map[string]any, masked []string) error {
	secret, ok := obj["secret"].(bool)
	if ok && secret {
		path, ok := obj["path"].(string)
		if !ok || path == "" {
			return nil
		}

		if !slices.Contains(masked, path) {
			return fmt.Errorf("%w: marker for %q is not in masked %q", errUnexpected, path, masked)
		}

		return nil
	}

	for _, elem := range obj {
		if err := maskedStateWalkMarkers(elem, masked); err != nil {
			return err
		}
	}

	return nil
}

// maskedStateHasSecret reports whether the outputs body masks at least one
// secret value.
func maskedStateHasSecret(outputs string) bool {
	var doc struct {
		Masked []string `json:"masked"`
	}

	if err := json.Unmarshal([]byte(outputs), &doc); err != nil {
		return false
	}

	return len(doc.Masked) > 0
}

// maskedStateHasMaskedWith reports whether the masked list of body holds a
// value path with infix, for example "/inputs/" for a masked resource
// input or "/outputs/" for a masked output.
func maskedStateHasMaskedWith(body, infix string) bool {
	var doc struct {
		Masked []string `json:"masked"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return false
	}

	return slices.ContainsFunc(doc.Masked, func(path string) bool {
		return strings.Contains(path, infix)
	})
}

// maskedStateCheckExportSecret proves the checkpoint really holds secrets
// from the CLI: the export of the masked stack carries the secret signature
// in the outputs of the stack resource and of the reference resource. The
// masked routes above proved none of it leaks.
func maskedStateCheckExportSecret(ctx context.Context, env *e2e.Env, base string) error {
	status, body, err := env.APIStatus(ctx, http.MethodGet, base+"/export", "")
	if err != nil {
		return fmt.Errorf("%w: GET %s/export: %w", errUnexpected, base, err)
	}

	if status != http.StatusOK {
		return fmt.Errorf("%w: GET %s/export answers %d, want %d", errUnexpected, base, status, http.StatusOK)
	}

	var exported struct {
		Deployment map[string]any `json:"deployment"`
	}

	if err := json.Unmarshal([]byte(body), &exported); err != nil {
		return fmt.Errorf("%w: decode export: %w", errUnexpected, err)
	}

	resources, ok := exported.Deployment["resources"].([]any)
	if !ok {
		return fmt.Errorf("%w: export holds no resources array", errUnexpected)
	}

	stackSecret := false
	refSecret := false

	for _, elem := range resources {
		if maskedStateOutputsSecret(elem, "pulumi:pulumi:Stack") {
			stackSecret = true
		}

		if maskedStateOutputsSecret(elem, "pulumi:pulumi:StackReference") {
			refSecret = true
		}
	}

	if !stackSecret {
		return fmt.Errorf(
			"%w: export holds no secret in the stack outputs, want the secret output",
			errUnexpected,
		)
	}

	if !refSecret {
		return fmt.Errorf(
			"%w: export holds no secret in the reference outputs, want the secret resource output",
			errUnexpected,
		)
	}

	return nil
}

// maskedStateOutputsSecret reports whether elem is a resource of type typ
// whose outputs hold the secret signature key.
func maskedStateOutputsSecret(elem any, typ string) bool {
	obj, ok := elem.(map[string]any)
	if !ok {
		return false
	}

	if obj["type"] != typ {
		return false
	}

	return maskedStateHoldsSigKey(obj["outputs"])
}

// maskedStateHoldsSigKey reports whether v holds the secret signature key.
func maskedStateHoldsSigKey(v any) bool {
	switch typed := v.(type) {
	case map[string]any:
		if _, ok := typed[maskedStateSigKey]; ok {
			return true
		}

		return slices.ContainsFunc(slices.Collect(maps.Values(typed)), maskedStateHoldsSigKey)
	case []any:
		return slices.ContainsFunc(typed, maskedStateHoldsSigKey)
	default:
		return false
	}
}

// maskedStateInstance decodes one instance of a downloaded state in e2e
// checks.
type maskedStateInstance struct {
	//nolint:tagliatelle // The state file wire name; it must not change.
	SensitiveAttributes []any `json:"sensitive_attributes"`
}

// maskedStateResource decodes one resource of a downloaded state in e2e
// checks.
type maskedStateResource struct {
	Type      string                `json:"type"`
	Name      string                `json:"name"`
	Instances []maskedStateInstance `json:"instances"`
}

// maskedStateDoc decodes a downloaded state in e2e checks.
type maskedStateDoc struct {
	Resources []maskedStateResource `json:"resources"`
}

// checkMaskedResourceList proves the masked resource list holds the data and
// provider resources with changed only on the data resource, and no secret
// value in the body.
func checkMaskedResourceList(body string) error {
	for _, secret := range []string{maskedSecretV1, maskedSecretV2} {
		if strings.Contains(body, secret) {
			return fmt.Errorf("%w: resource list holds a secret value", errUnexpected)
		}
	}

	var doc maskedResourceDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode resource list: %w", errUnexpected, err)
	}

	if len(doc.Data) != 2 {
		return fmt.Errorf("%w: resource list holds %d resources, want 2: %s", errUnexpected, len(doc.Data), body)
	}

	for _, item := range doc.Data {
		if item.Type != "state-version-resources" {
			return fmt.Errorf("%w: resource %q has type %q", errUnexpected, item.ID, item.Type)
		}

		wantChanged := item.Attributes.Address == "terraform_data.smoke"
		if item.Attributes.Changed != wantChanged {
			return fmt.Errorf(
				"%w: resource %q changed = %t, want %t",
				errUnexpected,
				item.Attributes.Address,
				item.Attributes.Changed,
				wantChanged,
			)
		}
	}

	return nil
}

// checkMaskedResourceFilterBodySecrets fails when body holds a masked secret value.
func checkMaskedResourceFilterBodySecrets(body string) error {
	for _, secret := range []string{maskedSecretV1, maskedSecretV2} {
		if strings.Contains(body, secret) {
			return fmt.Errorf("%w: filtered resource list holds a secret value", errUnexpected)
		}
	}

	return nil
}

// checkMaskedResourceFilterByType proves filter[q] with a type substring answers only
// the data resource.
func checkMaskedResourceFilterByType(body string) error {
	if err := checkMaskedResourceFilterBodySecrets(body); err != nil {
		return err
	}

	doc, err := decodeMaskedResourceFilterDoc(body)
	if err != nil {
		return err
	}

	if len(doc.Data) != 1 || doc.Data[0].Attributes.Address != "terraform_data.smoke" {
		return fmt.Errorf("%w: filter by type answers %+v, want only the data resource", errUnexpected, doc.Data)
	}

	if doc.Meta.NextCursor != "" || doc.Meta.Incomplete {
		return fmt.Errorf("%w: filter by type meta = %+v, want one complete page", errUnexpected, doc.Meta)
	}

	return nil
}

// checkMaskedResourceFilterByAddress proves filter[q] with an address substring
// answers only the provider resource.
func checkMaskedResourceFilterByAddress(body string) error {
	if err := checkMaskedResourceFilterBodySecrets(body); err != nil {
		return err
	}

	doc, err := decodeMaskedResourceFilterDoc(body)
	if err != nil {
		return err
	}

	if len(doc.Data) != 1 || doc.Data[0].Attributes.Address != "random_password.pw" {
		return fmt.Errorf(
			"%w: filter by address answers %+v, want only the provider resource",
			errUnexpected,
			doc.Data,
		)
	}

	return nil
}

// checkMaskedResourceFilterEmpty proves a filter on a value answers no resources and
// no secret value.
func checkMaskedResourceFilterEmpty(body string) error {
	if err := checkMaskedResourceFilterBodySecrets(body); err != nil {
		return err
	}

	doc, err := decodeMaskedResourceFilterDoc(body)
	if err != nil {
		return err
	}

	if len(doc.Data) != 0 {
		return fmt.Errorf("%w: filter on a value matched %+v, want none", errUnexpected, doc.Data)
	}

	if doc.Meta.NextCursor != "" {
		return fmt.Errorf("%w: empty filtered list holds a next cursor", errUnexpected)
	}

	return nil
}

// checkMaskedResourceFilterFirstPage proves the first filtered page holds one
// of the two matching resources with a next cursor, and returns both.
func checkMaskedResourceFilterFirstPage(body string) (maskedResourceFilterPage, error) {
	if err := checkMaskedResourceFilterBodySecrets(body); err != nil {
		return maskedResourceFilterPage{}, err
	}

	doc, err := decodeMaskedResourceFilterDoc(body)
	if err != nil {
		return maskedResourceFilterPage{}, err
	}

	if len(doc.Data) != 1 || doc.Meta.NextCursor == "" {
		return maskedResourceFilterPage{}, fmt.Errorf(
			"%w: first filtered page = %s, want 1 match with a cursor",
			errUnexpected,
			body,
		)
	}

	return maskedResourceFilterPage{cursor: doc.Meta.NextCursor, address: doc.Data[0].Attributes.Address}, nil
}

// checkMaskedResourceFilterSecondPage proves the second filtered page holds the other
// matching resource with no next cursor.
func checkMaskedResourceFilterSecondPage(firstAddress string) func(string) error {
	return func(body string) error {
		if err := checkMaskedResourceFilterBodySecrets(body); err != nil {
			return err
		}

		doc, err := decodeMaskedResourceFilterDoc(body)
		if err != nil {
			return err
		}

		if len(doc.Data) != 1 || doc.Meta.NextCursor != "" {
			return fmt.Errorf(
				"%w: second filtered page = %s, want 1 match with no cursor",
				errUnexpected,
				body,
			)
		}

		if doc.Data[0].Attributes.Address == firstAddress {
			return fmt.Errorf("%w: filtered pages overlap on %q", errUnexpected, firstAddress)
		}

		return nil
	}
}

// checkMaskedResourceDetail proves one masked resource answers its secrets
// masked: every marker names a path in masked, the expected secret paths are
// masked, and the body holds no secret value.
func checkMaskedResourceDetail(address string) func(string) error {
	return func(body string) error {
		if err := checkBodyHoldsNoSecret(body, address); err != nil {
			return err
		}

		doc, err := decodeMaskedDetail(body, address)
		if err != nil {
			return err
		}

		masked := maskedPathSet(doc.Data.Attributes.Masked)
		if len(masked) == 0 {
			return fmt.Errorf("%w: resource %q masks nothing", errUnexpected, address)
		}

		for _, instance := range doc.Data.Attributes.Instances {
			if err := checkMaskedMarkers(instance.Attributes, masked); err != nil {
				return fmt.Errorf("%w: resource %q: %w", errUnexpected, address, err)
			}
		}

		return checkMaskedExpectedPaths(address, masked)
	}
}

// checkMaskedMarkers proves each secret marker in attributes names a path in
// masked: masked is authoritative.
func checkMaskedMarkers(attributes map[string]any, masked map[string]bool) error {
	for _, value := range attributes {
		if err := checkMaskedValue(value, masked); err != nil {
			return err
		}
	}

	return nil
}

// checkMaskedValue proves one attribute value masks its secrets.
func checkMaskedValue(value any, masked map[string]bool) error {
	switch typed := value.(type) {
	case map[string]any:
		return checkMaskedObject(typed, masked)
	case []any:
		return checkMaskedList(typed, masked)
	default:
		return nil
	}
}

// checkMaskedObject proves one object masks its secrets: a secret marker
// names a path in masked, any other object recurses.
func checkMaskedObject(obj map[string]any, masked map[string]bool) error {
	if secret, ok := obj["secret"].(bool); ok && secret {
		return checkSecretMarker(obj, masked)
	}

	return checkMaskedMarkers(obj, masked)
}

// checkMaskedList proves one array masks its secrets.
func checkMaskedList(list []any, masked map[string]bool) error {
	for _, elem := range list {
		if err := checkMaskedValue(elem, masked); err != nil {
			return err
		}
	}

	return nil
}

// checkMaskedExpectedPaths proves the secrets the scenario plants are
// masked: the sensitive input of the data resource, and the result the
// provider schema marks sensitive.
func checkMaskedExpectedPaths(address string, masked map[string]bool) error {
	var suffix string

	switch address {
	case "terraform_data.smoke":
		suffix = "/attributes/input"
	case "random_password.pw":
		suffix = "/attributes/result"
	default:
		return fmt.Errorf("%w: unexpected resource %q", errUnexpected, address)
	}

	for path := range masked {
		if strings.HasSuffix(path, suffix) {
			return nil
		}
	}

	return fmt.Errorf("%w: resource %q masks no path ending in %q", errUnexpected, address, suffix)
}

// checkMaskedOutputs proves the outputs list answers the sensitive output as
// null with its sensitivity kept, and no secret value.
func checkMaskedOutputs(body string) error {
	for _, secret := range []string{maskedSecretV1, maskedSecretV2} {
		if strings.Contains(body, secret) {
			return fmt.Errorf("%w: outputs list holds a secret value", errUnexpected)
		}
	}

	var doc maskedOutputsDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode outputs list: %w", errUnexpected, err)
	}

	for _, output := range doc.Data {
		if output.Attributes.Name != "secret_out" {
			continue
		}

		return checkSecretOutput(output.Attributes)
	}

	return fmt.Errorf("%w: outputs list holds no secret output", errUnexpected)
}
