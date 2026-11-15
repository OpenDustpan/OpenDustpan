package workspace_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// pushMaskedOther declares a version with content in another workspace of the
// same fixture, uploads the rendering first so the marks land while the
// version is pending, then uploads the state.
func (f maskedFixture) pushMaskedOther(
	t *testing.T, ws workspace.Workspace, serial int64, content, rendering string,
) (workspace.StateVersion, token.Token) {
	t.Helper()

	ctx := t.Context()

	pending, err := f.svc.BeginState(
		ctx,
		ws.ID,
		"alice",
		newState("l1", serial, content),
		authority(noOverrides(), nil),
	)
	if err != nil {
		t.Fatalf("begin serial %d: %v", serial, err)
	}

	if rendering != "" {
		f.uploadMaskedRendering(t, pending.Version.ID, pending.Upload, rendering, serial)
	}

	sv, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload serial %d: %v", serial, err)
	}

	return sv, pending.Upload
}

// diffFromState is the older state of the diff tests: two stable resources,
// one resource the newer state drops, one plain input the newer state
// changes, and one sensitive input the newer state changes.
const diffFromState = `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"diff-lineage",` +
	`"outputs":{},"resources":[` +
	`{"mode":"managed","type":"terraform_data","name":"keep",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"keep","input":{"value":"keep","type":"string"}},` +
	`"sensitive_attributes":[] }]},` +
	`{"mode":"managed","type":"terraform_data","name":"gone",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"gone","input":{"value":"gone","type":"string"}},` +
	`"sensitive_attributes":[] }]},` +
	`{"mode":"managed","type":"terraform_data","name":"change",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"change","input":{"value":"before","type":"string"}},` +
	`"sensitive_attributes":[] }]},` +
	`{"mode":"managed","type":"terraform_data","name":"smoke",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"smoke",` +
	`"input":{"value":"s3cr3t-v1","type":"string"}},` +
	`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]] }]}]}`

// diffToState is the newer state of the diff tests: the kept resources
// unchanged, one added resource, the plain input changed, and the sensitive
// input changed.
const diffToState = `{"version":4,"terraform_version":"1.11.0","serial":2,"lineage":"diff-lineage",` +
	`"outputs":{},"resources":[` +
	`{"mode":"managed","type":"terraform_data","name":"keep",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"keep","input":{"value":"keep","type":"string"}},` +
	`"sensitive_attributes":[] }]},` +
	`{"mode":"managed","type":"terraform_data","name":"added",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"added","input":{"value":"added","type":"string"}},` +
	`"sensitive_attributes":[] }]},` +
	`{"mode":"managed","type":"terraform_data","name":"change",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"change","input":{"value":"after","type":"string"}},` +
	`"sensitive_attributes":[] }]},` +
	`{"mode":"managed","type":"terraform_data","name":"smoke",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"smoke",` +
	`"input":{"value":"s3cr3t-v2","type":"string"}},` +
	`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]] }]}]}`

// diffTestMarks are the kept marks of the diff states: the sensitive input
// of the smoke resource.
func diffTestMarks() workspace.SensitiveMarks {
	return workspace.SensitiveMarks{"terraform_data.smoke": {"/input"}}
}

// markedState returns one side of a diff: the state with its kept marks.
func markedState(state string, marks workspace.SensitiveMarks) workspace.StateWithMarks {
	return workspace.StateWithMarks{State: []byte(state), Marks: marks}
}

// diffStates returns the diff of two marked states.
func diffStates(
	t *testing.T, from, to workspace.StateWithMarks, cursor string, size int,
) (workspace.StateDiff, error) {
	t.Helper()

	return workspace.DiffStates(from, to, cursor, size)
}

// diffStandard returns the diff of the standard states with the test marks.
func diffStandard(t *testing.T, cursor string, size int) (workspace.StateDiff, error) {
	t.Helper()

	from := markedState(diffFromState, diffTestMarks())
	to := markedState(diffToState, diffTestMarks())

	return diffStates(t, from, to, cursor, size)
}

// checkDiffHoldsNoSecrets fails when any address or path of the diff holds
// one of the secret values.
func checkDiffHoldsNoSecrets(t *testing.T, diff workspace.StateDiff, secrets ...string) {
	t.Helper()

	var bodies []string

	bodies = append(bodies, diff.Added...)
	bodies = append(bodies, diff.Removed...)

	for _, entry := range diff.Changed {
		bodies = append(bodies, entry.Address)
		bodies = append(bodies, entry.ChangedPaths...)
		bodies = append(bodies, entry.ChangedSecrets...)
	}

	for _, body := range bodies {
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("diff holds %q in %q", secret, body)
			}
		}
	}
}

// diffByAddress maps the changed entries by address.
func diffByAddress(t *testing.T, diff workspace.StateDiff) map[string]workspace.StateDiffChanged {
	t.Helper()

	out := map[string]workspace.StateDiffChanged{}

	for _, entry := range diff.Changed {
		out[entry.Address] = entry
	}

	return out
}

// checkDiffEntries proves the added, removed and changed addresses of the
// standard diff.
func checkDiffEntries(t *testing.T, diff workspace.StateDiff) map[string]workspace.StateDiffChanged {
	t.Helper()

	if !slices.Equal(diff.Added, []string{"terraform_data.added"}) {
		t.Errorf("added = %q, want [terraform_data.added]", diff.Added)
	}

	if !slices.Equal(diff.Removed, []string{"terraform_data.gone"}) {
		t.Errorf("removed = %q, want [terraform_data.gone]", diff.Removed)
	}

	byAddress := diffByAddress(t, diff)

	if len(byAddress) != 2 {
		t.Fatalf("changed = %+v, want the change and smoke resources", diff.Changed)
	}

	return byAddress
}

// checkPlainChange proves the plain change entry holds the input path with
// no secret.
func checkPlainChange(t *testing.T, byAddress map[string]workspace.StateDiffChanged) {
	t.Helper()

	plain := byAddress["terraform_data.change"]
	if plain.Address == "" {
		t.Fatal("changed holds no terraform_data.change")
	}

	if len(plain.ChangedSecrets) != 0 {
		t.Errorf("plain changed secrets = %q, want none", plain.ChangedSecrets)
	}

	if !slices.Contains(plain.ChangedPaths, "/attributes/input") {
		t.Errorf("plain changed paths = %q, want /attributes/input", plain.ChangedPaths)
	}
}

// checkSensitiveChange proves the smoke entry holds the input in secrets
// with no plain path.
func checkSensitiveChange(t *testing.T, byAddress map[string]workspace.StateDiffChanged) {
	t.Helper()

	sensitive := byAddress["terraform_data.smoke"]
	if sensitive.Address == "" {
		t.Fatal("changed holds no terraform_data.smoke")
	}

	if !slices.Equal(sensitive.ChangedSecrets, []string{"/attributes/input"}) {
		t.Errorf("sensitive changed secrets = %q, want [/attributes/input]", sensitive.ChangedSecrets)
	}

	if len(sensitive.ChangedPaths) != 0 {
		t.Errorf("sensitive changed paths = %q, want none", sensitive.ChangedPaths)
	}
}

// TestDiffTofuStates proves the diff of two states: one added and one
// removed address, the plain input change as a changed path, and the
// sensitive input change as a changed secret with no value in the diff.
func TestDiffTofuStates(t *testing.T) {
	t.Parallel()

	diff, err := diffStandard(t, "", 0)
	if err != nil {
		t.Fatalf("diff states: %v", err)
	}

	byAddress := checkDiffEntries(t, diff)
	checkPlainChange(t, byAddress)
	checkSensitiveChange(t, byAddress)

	if diff.NextCursor != "" || diff.Incomplete {
		t.Errorf("diff = %+v, want a single complete page", diff)
	}

	checkDiffHoldsNoSecrets(t, diff, "s3cr3t-v1", "s3cr3t-v2", "before", "after")
}

// TestDiffTofuSameState proves the diff of a state with itself is empty.
func TestDiffTofuSameState(t *testing.T) {
	t.Parallel()

	from := markedState(diffFromState, diffTestMarks())

	diff, err := diffStates(t, from, from, "", 0)
	if err != nil {
		t.Fatalf("diff states: %v", err)
	}

	if len(diff.Added) != 0 || len(diff.Removed) != 0 || len(diff.Changed) != 0 {
		t.Fatalf("same-state diff = %+v, want no change", diff)
	}

	if diff.NextCursor != "" || diff.Incomplete {
		t.Errorf("diff = %+v, want a single complete page", diff)
	}
}

// TestDiffTofuSensitiveAttributes proves a change the state file marks
// sensitive through sensitive_attributes lands in changed secrets even with
// no kept mark for its address.
func TestDiffTofuSensitiveAttributes(t *testing.T) {
	t.Parallel()

	from := `{"version":4,"resources":[` +
		`{"mode":"managed","type":"random_password","name":"pw",` +
		`"provider":"provider[\"registry.opentofu.org/hashicorp/random\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"pw-id","result":"pw-one"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"result"}]] }]}]}`

	to := strings.ReplaceAll(from, "pw-one", "pw-two")

	marks := workspace.SensitiveMarks{"other.none": {"/id"}}

	diff, err := diffStates(t, markedState(from, marks), markedState(to, marks), "", 0)
	if err != nil {
		t.Fatalf("diff states: %v", err)
	}

	if len(diff.Changed) != 1 {
		t.Fatalf("changed = %+v, want the provider resource", diff.Changed)
	}

	entry := diff.Changed[0]
	if entry.Address != "random_password.pw" {
		t.Fatalf("changed address = %q, want random_password.pw", entry.Address)
	}

	if !slices.Contains(entry.ChangedSecrets, "/attributes/result") {
		t.Errorf("changed secrets = %q, want /attributes/result", entry.ChangedSecrets)
	}

	if len(entry.ChangedPaths) != 0 {
		t.Errorf("changed paths = %q, want none", entry.ChangedPaths)
	}
}

// TestDiffTofuWithoutMarks proves the fallback: with no kept marks for a
// version every changed attribute value lands in changed secrets.
func TestDiffTofuWithoutMarks(t *testing.T) {
	t.Parallel()

	diff, err := diffStates(t, markedState(diffFromState, nil), markedState(diffToState, nil), "", 0)
	if err != nil {
		t.Fatalf("diff states: %v", err)
	}

	if !slices.Equal(diff.Added, []string{"terraform_data.added"}) {
		t.Errorf("added = %q, want [terraform_data.added]", diff.Added)
	}

	if !slices.Equal(diff.Removed, []string{"terraform_data.gone"}) {
		t.Errorf("removed = %q, want [terraform_data.gone]", diff.Removed)
	}

	for _, entry := range diff.Changed {
		if len(entry.ChangedPaths) != 0 {
			t.Errorf("resource %q changed paths = %q, want none without marks", entry.Address, entry.ChangedPaths)
		}

		if len(entry.ChangedSecrets) == 0 {
			t.Errorf("resource %q changed secrets holds nothing, want the changed values", entry.Address)
		}
	}

	checkDiffHoldsNoSecrets(t, diff, "s3cr3t-v1", "s3cr3t-v2")
}

// TestDiffTofuOneSidedFallback proves the fallback applies when either side
// has no kept marks: a plain change then still lands in changed secrets.
func TestDiffTofuOneSidedFallback(t *testing.T) {
	t.Parallel()

	diff, err := diffStates(t, markedState(diffFromState, diffTestMarks()), markedState(diffToState, nil), "", 0)
	if err != nil {
		t.Fatalf("diff states: %v", err)
	}

	byAddress := diffByAddress(t, diff)
	plain, ok := byAddress["terraform_data.change"]

	if !ok {
		t.Fatal("changed holds no terraform_data.change")
	}

	if len(plain.ChangedPaths) != 0 {
		t.Errorf("plain changed paths = %q, want none with one side unmarked", plain.ChangedPaths)
	}

	if !slices.Contains(plain.ChangedSecrets, "/attributes/input") {
		t.Errorf("plain changed secrets = %q, want /attributes/input", plain.ChangedSecrets)
	}
}

// fullDiffAddresses returns the differing addresses of the standard full
// diff, failing the test when it holds other than four.
func fullDiffAddresses(t *testing.T, full workspace.StateDiff) map[string]bool {
	t.Helper()

	want := map[string]bool{}
	for _, address := range full.Added {
		want[address] = true
	}

	for _, address := range full.Removed {
		want[address] = true
	}

	for _, entry := range full.Changed {
		want[entry.Address] = true
	}

	if len(want) != 4 {
		t.Fatalf("full diff holds %d differing addresses, want 4", len(want))
	}

	return want
}

// collectDiffPage records the one address of a page of one, failing the test
// otherwise. It returns the cursor of the next page.
func collectDiffPage(t *testing.T, cursor string, seen map[string]bool) string {
	t.Helper()

	page, err := diffStandard(t, cursor, 1)
	if err != nil {
		t.Fatalf("diff states at %q: %v", cursor, err)
	}

	addresses := slices.Concat(page.Added, page.Removed, changedAddresses(page))
	if len(addresses) != 1 {
		t.Fatalf("page at %q holds %d entries, want 1: %+v", cursor, len(addresses), page)
	}

	if seen[addresses[0]] {
		t.Fatalf("page lists %q twice", addresses[0])
	}

	seen[addresses[0]] = true

	if cursor == "" && !page.Incomplete {
		t.Error("first page is complete, want incomplete while cut")
	}

	if page.NextCursor == "" && page.Incomplete {
		t.Error("last page is incomplete, want a closed page")
	}

	return page.NextCursor
}

// TestDiffTofuPaging proves paging over differing addresses: pages of one
// hold one entry each with incomplete while cut, and the last page closes
// the cursor.
func TestDiffTofuPaging(t *testing.T) {
	t.Parallel()

	full, err := diffStandard(t, "", 0)
	if err != nil {
		t.Fatalf("diff states: %v", err)
	}

	want := fullDiffAddresses(t, full)

	seen := map[string]bool{}
	cursor := ""

	for {
		cursor = collectDiffPage(t, cursor, seen)

		if cursor == "" {
			break
		}
	}

	if len(seen) != len(want) {
		t.Fatalf("paged addresses = %v, want %v", seen, want)
	}
}

// changedAddresses returns the addresses of the changed entries of a page.
func changedAddresses(page workspace.StateDiff) []string {
	out := make([]string, 0, len(page.Changed))

	for _, entry := range page.Changed {
		out = append(out, entry.Address)
	}

	return out
}

// TestDiffTofuSkippedIncomplete proves entries the server skips cut the diff
// short: a state with an entry without type and name still diffs, with
// incomplete set.
func TestDiffTofuSkippedIncomplete(t *testing.T) {
	t.Parallel()

	broken := strings.Replace(
		diffToState,
		`"type":"terraform_data","name":"added"`,
		`"type":"","name":""`,
		1,
	)

	diff, err := diffStates(t, markedState(diffFromState, diffTestMarks()), markedState(broken, diffTestMarks()), "", 0)
	if err != nil {
		t.Fatalf("diff states: %v", err)
	}

	if !diff.Incomplete {
		t.Error("diff is complete, want incomplete for the skipped entry")
	}

	for _, address := range diff.Added {
		if address == "terraform_data.added" {
			t.Errorf("added holds the skipped resource: %q", diff.Added)
		}
	}
}

// TestDiffTofuInvalid proves a bad cursor and a bad size fail with
// ErrInvalidState.
func TestDiffTofuInvalid(t *testing.T) {
	t.Parallel()

	for _, cursor := range []string{"many", "-1"} {
		_, err := diffStandard(t, cursor, 0)
		if !errors.Is(err, workspace.ErrInvalidState) {
			t.Errorf("cursor %q: err = %v, want ErrInvalidState", cursor, err)
		}
	}

	_, err := diffStandard(t, "", 501)
	if !errors.Is(err, workspace.ErrInvalidState) {
		t.Errorf("size 501: err = %v, want ErrInvalidState", err)
	}
}

// TestDiffThroughService proves the diff through the service: two pushes,
// then the diff answers the added, removed and changed addresses with the
// sensitive change in secrets and no secret value in the marshaled diff.
func TestDiffThroughService(t *testing.T) {
	t.Parallel()

	f := newMaskedFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	first, _ := f.pushMasked(t, 1, diffFromState, diffRendering())
	*f.now = f.now.Add(2 * time.Hour)
	second, _ := f.pushMasked(t, 2, diffToState, diffRendering())

	diff, err := f.svc.DiffStates(ctx, second.ID, first.ID, "", 0)
	if err != nil {
		t.Fatalf("diff states: %v", err)
	}

	byAddress := checkDiffEntries(t, diff)
	checkSensitiveChange(t, byAddress)
	checkDiffHoldsNoSecrets(t, diff, "s3cr3t-v1", "s3cr3t-v2")
}

// TestDiffThroughServiceOtherWorkspace proves a from version of another
// workspace answers ErrStateVersionNotFound as if it did not exist.
func TestDiffThroughServiceOtherWorkspace(t *testing.T) {
	t.Parallel()

	f := newMaskedFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	first, _ := f.pushMasked(t, 1, diffFromState, diffRendering())
	*f.now = f.now.Add(2 * time.Hour)
	second, _ := f.pushMasked(t, 2, diffToState, diffRendering())

	name, err := workspace.ParseName("other")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	other, err := f.svc.Create(ctx, workspace.Ref{Namespace: "acme", Name: name}, memPlacement("acme"))
	if err != nil {
		t.Fatalf("create other workspace: %v", err)
	}

	if _, err := f.svc.Lock(ctx, other.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock other: %v", err)
	}

	foreign, _ := f.pushMaskedOther(t, other, 1, diffFromState, diffRendering())

	if _, err := f.svc.DiffStates(ctx, second.ID, foreign.ID, "", 0); !errors.Is(
		err, workspace.ErrStateVersionNotFound,
	) {
		t.Errorf("diff with a from version of another workspace: err = %v, want ErrStateVersionNotFound", err)
	}

	if _, err := f.svc.DiffStates(ctx, second.ID, first.ID, "", 0); err != nil {
		t.Errorf("diff in the same workspace: %v", err)
	}
}

// TestDiffThroughServiceClientEncrypted proves a state either side encrypted
// fails the diff with ErrClientEncrypted.
func TestDiffThroughServiceClientEncrypted(t *testing.T) {
	t.Parallel()

	f := newMaskedFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	plain, _ := f.pushMasked(t, 1, diffFromState, diffRendering())
	*f.now = f.now.Add(2 * time.Hour)
	sealed, _ := f.pushMasked(t, 2, envelope, "")

	if _, err := f.svc.DiffStates(ctx, sealed.ID, plain.ID, "", 0); !errors.Is(
		err, workspace.ErrClientEncrypted,
	) {
		t.Errorf("diff with an encrypted to state: err = %v, want ErrClientEncrypted", err)
	}

	if _, err := f.svc.DiffStates(ctx, plain.ID, sealed.ID, "", 0); !errors.Is(
		err, workspace.ErrClientEncrypted,
	) {
		t.Errorf("diff with an encrypted from state: err = %v, want ErrClientEncrypted", err)
	}
}

// diffRendering returns a json-state rendering that marks the sensitive input
// of the smoke resource.
func diffRendering() string {
	return `{"format_version":"1.2","values":{"root_module":{"resources":[` +
		`{"address":"terraform_data.smoke","type":"terraform_data","name":"smoke",` +
		`"values":{"id":"smoke","input":"s3cr3t"},` +
		`"sensitive_values":{"input":true}}]}}}`
}
