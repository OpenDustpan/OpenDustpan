package releaserecord_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	stagedWorkflowPath = ".github-workflows-staging/release.yml"
	liveWorkflowPath   = ".github/workflows/release.yml"
)

var (
	errBothHoldArchivesJob = errors.New(
		"both .github-workflows-staging/release.yml and .github/workflows/release.yml hold the archives-build job: the staged copy was left behind",
	)
	errReadDenied              = errors.New("permission denied")
	errNeitherHoldsArchivesJob = errors.New(
		".github-workflows-staging/release.yml does not exist and .github/workflows/release.yml does not hold the archives-build job",
	)
)

// workflowHoldsArchivesBuild answers whether the workflow bytes hold the
// archives-build job.
func workflowHoldsArchivesBuild(raw []byte) bool {
	var wf workflowFile

	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return strings.Contains(string(raw), "archives-build")
	}

	_, ok := wf.Jobs["archives-build"]

	return ok
}

// workflowSource is one workflow file that the checker reads in full.
type workflowSource struct {
	path string
	raw  []byte
}

// chooseWorkflowFile applies the guard rule for the two workflow paths. It
// returns each file that the checker must read in full; the first one is the
// primary file. While the staged copy exists, it is the primary file, whatever
// it holds, so a staged copy without the archives-build job fails through its
// missing-job finding. The live file is also checked when it holds the job,
// so a weak live guard cannot hide behind a staged copy. With no staged copy,
// the live file is checked when it holds the job. It fails when both hold the
// job (a move that left the staged copy behind), and when there is no staged
// copy and the live file has no job. No path is skipped, so the guard is not
// fail-open.
func chooseWorkflowFile(stagedExists bool, stagedRaw, liveRaw []byte) ([]workflowSource, error) {
	liveHolds := workflowHoldsArchivesBuild(liveRaw)
	live := workflowSource{liveWorkflowPath, liveRaw}

	switch {
	case stagedExists && liveHolds && workflowHoldsArchivesBuild(stagedRaw):
		return nil, errBothHoldArchivesJob
	case stagedExists && liveHolds:
		return []workflowSource{{stagedWorkflowPath, stagedRaw}, live}, nil
	case stagedExists:
		return []workflowSource{{stagedWorkflowPath, stagedRaw}}, nil
	case liveHolds:
		return []workflowSource{live}, nil
	default:
		return nil, errNeitherHoldsArchivesJob
	}
}

// readOptionalWorkflow reads a file that may be absent. Only a missing file
// counts as absent; any other read error is returned, so an unreadable file
// is never skipped.
func readOptionalWorkflow(read func(string) ([]byte, error), path string) ([]byte, bool, error) {
	raw, err := read(path)
	if err == nil {
		return raw, true, nil
	}

	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	return nil, false, err
}

// releaseWorkflowSources returns each patched release workflow file that
// the checker reads in full. The staged copy in .github-workflows-staging/
// holds the patched workflow until it moves to .github/workflows/.
func releaseWorkflowSources(t *testing.T) []workflowSource {
	t.Helper()

	root, openErr := os.OpenRoot("../..")
	if openErr != nil {
		t.Fatal(openErr)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()

	stagedRaw, stagedExists, stagedErr := readOptionalWorkflow(root.ReadFile, stagedWorkflowPath)
	if stagedErr != nil {
		t.Fatal(stagedErr)
	}

	liveRaw, readErr := root.ReadFile(liveWorkflowPath)
	if readErr != nil {
		t.Fatal(readErr)
	}

	sources, chooseErr := chooseWorkflowFile(stagedExists, stagedRaw, liveRaw)
	if chooseErr != nil {
		t.Fatal(chooseErr)
	}

	for _, src := range sources {
		t.Logf("checking workflow file: %s", src.path)
	}

	return sources
}

// releaseWorkflowBytes returns the path and the bytes of the primary file of
// the patched release workflow.
func releaseWorkflowBytes(t *testing.T) (string, []byte) {
	t.Helper()

	sources := releaseWorkflowSources(t)

	return sources[0].path, sources[0].raw
}

// workflowStep is one step of a job of the release workflow.
type workflowStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
	Env  map[string]any `yaml:"env"`
	Run  string         `yaml:"run"`
}

// workflowJob is one job of the release workflow.
type workflowJob struct {
	If          string            `yaml:"if"`
	Needs       any               `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Env         map[string]any    `yaml:"env"`
	Steps       []workflowStep    `yaml:"steps"`
}

// workflowFile is the part of the release workflow that the checker reads.
type workflowFile struct {
	On          map[string]any         `yaml:"on"`
	Permissions map[string]string      `yaml:"permissions"`
	Env         map[string]any         `yaml:"env"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

// releaseWorkflowJobNames lists the four jobs of the patched workflow.
func releaseWorkflowJobNames() []string {
	return []string{"archives-build", "archives-verify", "publish", "release-draft"}
}

// expectedReleaseGuard is the exact guard text of each job of the patched
// workflow: a tag push in the main repository.
const expectedReleaseGuard = "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && github.repository == 'OpenDustpan/OpenDustpan'"

// isReleaseWorkflowJob answers whether name is one of the four jobs.
func isReleaseWorkflowJob(name string) bool {
	return slices.Contains(releaseWorkflowJobNames(), name)
}

// expectedReleasePermissions answers the exact permissions map of a job.
func expectedReleasePermissions(name string) map[string]string {
	switch name {
	case "archives-build", "archives-verify":
		return map[string]string{"contents": "read"}
	case "publish":
		return map[string]string{"contents": "read", "packages": "write"}
	case "release-draft":
		return map[string]string{"contents": "write"}
	default:
		return nil
	}
}

// releaseWorkflowNeeds answers the needs of a job as a list.
func releaseWorkflowNeeds(v any) []string {
	switch n := v.(type) {
	case nil:
		return nil
	case string:
		return []string{n}
	case []any:
		var out []string

		for _, item := range n {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}

		return out
	default:
		return nil
	}
}

// releaseWorkflowJobForLine answers the job that holds lines[idx]: the last
// job header at two spaces of indent after the jobs section.
func releaseWorkflowJobForLine(lines []string, idx int) string {
	job := "workflow"
	header := regexp.MustCompile(`^ {2}([A-Za-z0-9_-]+):\s*$`)
	jobsAt := -1

	for i, line := range lines {
		if line == "jobs:" {
			jobsAt = i

			break
		}
	}

	for i := range idx + 1 {
		if jobsAt >= 0 && i <= jobsAt {
			continue
		}

		if m := header.FindStringSubmatch(lines[i]); m != nil {
			job = m[1]
		}
	}

	return job
}

// checkWorkflowPush checks that on push is exactly tags ["v*"] with no branch
// filter and no extra key.
func checkWorkflowPush(path string, pushMap map[string]any) []string {
	var out []string

	if _, hasBranches := pushMap["branches"]; hasBranches {
		out = append(out, "workflow "+path+": workflow guard: on push has a branch filter, want tags only")
	}

	if _, hasIgnore := pushMap["branches-ignore"]; hasIgnore {
		out = append(out, "workflow "+path+": workflow guard: on push has a branch filter, want tags only")
	}

	tags, ok := pushMap["tags"]
	if !ok {
		return append(out, "workflow "+path+": workflow guard: on push has no tags, want [\"v*\"]")
	}

	tagList, ok := tags.([]any)
	if !ok || len(tagList) != 1 {
		return append(
			out,
			"workflow "+path+": workflow guard: on push tags is not [\"v*\"], want a tag push only",
		)
	}

	if s, ok := tagList[0].(string); !ok || s != "v*" {
		out = append(out, "workflow "+path+": workflow guard: on push tags is not [\"v*\"], want a tag push only")
	}

	if len(pushMap) != 1 {
		out = append(out, "workflow "+path+": workflow guard: on push has an extra key, want tags only")
	}

	return out
}

// checkWorkflowTriggers checks that the file runs on a tag push only: on is
// exactly push with tags ["v*"], no other event and no branch filter.
func checkWorkflowTriggers(path string, wf workflowFile) []string {
	var out []string

	if wf.On == nil {
		return []string{"workflow " + path + ": workflow guard: the file has no on block"}
	}

	for trigger := range wf.On {
		if trigger != "push" {
			out = append(out, "workflow "+path+": workflow guard: on includes "+trigger+", want a tag push only")
		}
	}

	push, ok := wf.On["push"]
	if !ok {
		out = append(out, "workflow "+path+": workflow guard: on has no push trigger")

		return out
	}

	pushMap, ok := push.(map[string]any)
	if !ok {
		return append(out, "workflow "+path+": workflow guard: on push is not a mapping with tags [\"v*\"]")
	}

	return append(out, checkWorkflowPush(path, pushMap)...)
}

// checkWorkflowFilePermissions checks that no job inherits a permission. It
// reads the YAML node, not a decoded map, because a decoded map cannot tell an
// absent key from an empty mapping: the top-level permissions key must be
// present and its value must be an empty mapping.
func checkWorkflowFilePermissions(path string, doc *yaml.Node) []string {
	top := doc
	if top.Kind == yaml.DocumentNode && len(top.Content) > 0 {
		top = top.Content[0]
	}

	if top.Kind != yaml.MappingNode {
		return []string{"workflow " + path + ": permissions: the file is not a mapping"}
	}

	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "permissions" {
			continue
		}

		value := top.Content[i+1]
		if value.Kind == yaml.MappingNode && len(value.Content) == 0 {
			return nil
		}

		return []string{"workflow " + path + ": permissions: the file grants a permission, want an empty mapping"}
	}

	return []string{
		"workflow " + path + ": permissions: the file has no top-level permissions key, want an empty mapping",
	}
}

// allowedWorkflowTopLevelKeys answers the allowlist of the top-level keys of
// the workflow. A YAML comment is not a key, so a comment stays allowed.
func allowedWorkflowTopLevelKeys() []string {
	return []string{"name", "on", "permissions", "env", "jobs"}
}

// checkWorkflowTopLevelKeys answers a finding for each top-level key outside
// the allowlist, such as defaults. It reads the YAML node, not a decoded
// struct, because a decoded struct drops an unknown top-level key.
func checkWorkflowTopLevelKeys(path string, doc *yaml.Node) []string {
	top := doc
	if top.Kind == yaml.DocumentNode && len(top.Content) > 0 {
		top = top.Content[0]
	}

	if top.Kind != yaml.MappingNode {
		return []string{"workflow " + path + ": top-level keys: the file is not a mapping"}
	}

	var out []string

	for i := 0; i+1 < len(top.Content); i += 2 {
		key := top.Content[i].Value
		if !slices.Contains(allowedWorkflowTopLevelKeys(), key) {
			out = append(out, "workflow "+path+": top-level keys: the file holds the key "+
				key+", which the allowlist refuses")
		}
	}

	return out
}

// allowedWorkflowJobKeys answers the allowlist of the keys of a job.
func allowedWorkflowJobKeys() []string {
	return []string{"env", "if", "needs", "permissions", "runs-on", "steps"}
}

// allowedWorkflowStepKeys answers the allowlist of the keys of a step. In
// particular, "if" and "continue-on-error" are not allowed on a step.
func allowedWorkflowStepKeys() []string {
	return []string{"env", "name", "run", "uses", "with"}
}

// yamlMappingField returns the value of key in a mapping node.
func yamlMappingField(node *yaml.Node, key string) (*yaml.Node, bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, false
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1], true
		}
	}

	return nil, false
}

// checkJobKeys answers a finding for each key of one job outside the allowlist.
func checkJobKeys(name string, job *yaml.Node) []string {
	var out []string

	for j := 0; j+1 < len(job.Content); j += 2 {
		key := job.Content[j].Value
		if !slices.Contains(allowedWorkflowJobKeys(), key) {
			out = append(out, "job "+name+": job keys: the job holds the key "+key+
				", which the allowlist refuses")
		}
	}

	return out
}

// checkOneStepKeys answers a finding for each key of one step outside the allowlist.
func checkOneStepKeys(name, label string, step *yaml.Node) []string {
	var out []string

	for k := 0; k+1 < len(step.Content); k += 2 {
		key := step.Content[k].Value
		if !slices.Contains(allowedWorkflowStepKeys(), key) {
			out = append(out, "job "+name+": step keys: the "+label+" holds the key "+key+
				", which the allowlist refuses")
		}
	}

	return out
}

// checkStepsKeys answers a finding for each step key of one job outside the allowlist.
func checkStepsKeys(name string, job *yaml.Node) []string {
	var out []string

	steps, ok := yamlMappingField(job, "steps")
	if !ok || steps.Kind != yaml.SequenceNode {
		return nil
	}

	for idx, step := range steps.Content {
		if step.Kind != yaml.MappingNode {
			continue
		}

		label := fmt.Sprintf("step %d", idx+1)
		if n, ok := yamlMappingField(step, "name"); ok && n.Kind == yaml.ScalarNode {
			label = "step " + n.Value
		}

		out = append(out, checkOneStepKeys(name, label, step)...)
	}

	return out
}

// checkWorkflowKeyAllowlist checks that each job and each step holds only an
// allowed key. It reads the YAML node, not a decoded struct, because a decoded
// struct drops an unknown key such as a step "if" or "continue-on-error".
func checkWorkflowKeyAllowlist(doc *yaml.Node) []string {
	var out []string

	top := doc
	if top.Kind == yaml.DocumentNode && len(top.Content) > 0 {
		top = top.Content[0]
	}

	jobs, ok := yamlMappingField(top, "jobs")
	if !ok || jobs.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i+1 < len(jobs.Content); i += 2 {
		name := jobs.Content[i].Value
		job := jobs.Content[i+1]
		if job.Kind != yaml.MappingNode {
			continue
		}

		out = append(out, checkJobKeys(name, job)...)
		out = append(out, checkStepsKeys(name, job)...)
	}

	return out
}

// checkWorkflowJobPresence checks that the four jobs exist and that no other
// job exists: a job name outside the allowlist of four is refused.
func checkWorkflowJobPresence(wf workflowFile) []string {
	var out []string

	for _, name := range releaseWorkflowJobNames() {
		if _, ok := wf.Jobs[name]; !ok {
			out = append(out, "job "+name+": workflow guard: the job is missing")
		}
	}

	for name := range wf.Jobs {
		if !isReleaseWorkflowJob(name) {
			out = append(
				out,
				"job "+name+": workflow guard: unknown job, want one of the four jobs",
			)
		}
	}

	return out
}

// checkWorkflowGuard checks the guard of one job against one exact constant
// string: each job guard must equal expectedReleaseGuard.
func checkWorkflowGuard(name string, job workflowJob) []string {
	if strings.TrimSpace(job.If) == "" {
		return []string{"job " + name + ": workflow guard: the job has no guard"}
	}

	if job.If != expectedReleaseGuard {
		// Name the missing clause so that a person can fix it, and keep the
		// exact comparison so that each change of the guard fails.
		for _, clause := range []string{
			"github.event_name == 'push'",
			"startsWith(github.ref, 'refs/tags/v')",
			"github.repository == 'OpenDustpan/OpenDustpan'",
		} {
			if !strings.Contains(job.If, clause) {
				return []string{"job " + name + ": workflow guard: the guard lacks " + clause}
			}
		}

		if strings.Contains(job.If, "|| always()") {
			return []string{"job " + name + ": workflow guard: the guard holds || always()"}
		}

		return []string{"job " + name + ": workflow guard: the guard differs from " + expectedReleaseGuard}
	}

	return nil
}

// checkWorkflowNeeds checks the needs chain of one job.
func checkWorkflowNeeds(name string, job workflowJob) []string {
	needs := releaseWorkflowNeeds(job.Needs)

	switch name {
	case "archives-build":
		if len(needs) != 0 {
			return []string{"job archives-build: job order: the build job wants no needs"}
		}
	case "archives-verify":
		if !slices.Contains(needs, "archives-build") {
			return []string{"job archives-verify: job order: the verify job needs archives-build"}
		}
	case "publish":
		if !slices.Contains(needs, "archives-verify") {
			return []string{"job publish: job order: the publish job needs archives-verify"}
		}
	case "release-draft":
		if !slices.Contains(needs, "archives-verify") || !slices.Contains(needs, "publish") {
			return []string{"job release-draft: job order: the draft job needs archives-verify and publish"}
		}
	default:
		return []string{"job " + name + ": job order: unknown job holds a needs block"}
	}

	return nil
}

// checkWorkflowJobPermissions checks the permissions block of one job against
// one exact expected map: archives-build and archives-verify hold contents
// read only, publish holds contents read and packages write only, and
// release-draft holds contents write only. contents write is allowed only on
// release-draft. An extra scope fails the exact comparison.
func checkWorkflowJobPermissions(name string, job workflowJob) []string {
	if len(job.Permissions) == 0 {
		return []string{"job " + name + ": permissions: the job has no permissions block"}
	}

	want := expectedReleasePermissions(name)
	if want == nil {
		return []string{"job " + name + ": permissions: unknown job holds a permissions block"}
	}

	if len(job.Permissions) != len(want) {
		return []string{"job " + name + ": permissions: the job permissions differ from the allowlist"}
	}

	for k, v := range want {
		if job.Permissions[k] != v {
			return []string{"job " + name + ": permissions: the job wants " + k + " " + v}
		}
	}

	for k := range job.Permissions {
		if _, ok := want[k]; !ok {
			return []string{"job " + name + ": permissions: the job holds an extra scope " + k}
		}
	}

	return nil
}

// checkWorkflowPins checks that each uses value is a full SHA with a release
// comment. The comment is not in the parsed value, so this check reads lines.
func checkWorkflowPins(raw string) []string {
	var out []string

	lines := strings.Split(raw, "\n")
	usesPin := regexp.MustCompile(`uses:\s*(\S+)`)
	hex40 := regexp.MustCompile(`^[0-9a-f]{40}$`)
	// The pin comment names a release # vX.Y.Z, that is # v[0-9]+\.[0-9]+\.[0-9]+.
	releaseComment := regexp.MustCompile(`# v\d+\.\d+\.\d+`)

	for i, line := range lines {
		m := usesPin.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		job := releaseWorkflowJobForLine(lines, i)
		ref := m[1]
		head, pin, _ := strings.CutLast(ref, "@")

		if head == "" {
			out = append(out, "job "+job+": pins: the uses line has no pin: "+strings.TrimSpace(line))

			continue
		}

		if !hex40.MatchString(pin) {
			out = append(out, "job "+job+": pins: the pin is not a full 40-digit SHA: "+strings.TrimSpace(line))
		}

		comment := ""
		if idx := strings.Index(line, "#"); idx >= 0 {
			comment = line[idx:]
		}

		if !releaseComment.MatchString(comment) {
			out = append(out, "job "+job+": pins: the pin has no release comment: "+strings.TrimSpace(line))
		}
	}

	return out
}

// secretWord matches the word secrets in any expression: the dot form
// ${{ secrets.NAME }}, the index form ${{ secrets['NAME'] }} and a call such
// as ${{ toJSON(secrets) }}. The word boundaries keep a longer word from a
// match.
func secretWord() *regexp.Regexp {
	return regexp.MustCompile(`\bsecrets\b`)
}

// readsSecret answers whether one value is an expression that reads a secret.
func readsSecret(v any) bool {
	s, ok := v.(string)

	return ok && secretWord().MatchString(s)
}

// envReadsSecrets answers whether an env map reads a secret.
func envReadsSecrets(env map[string]any) bool {
	for _, v := range env {
		if readsSecret(v) {
			return true
		}
	}

	return false
}

// stepReadsSecrets answers whether a step reads a secret.
func stepReadsSecrets(step workflowStep) bool {
	if secretWord().MatchString(step.Run) {
		return true
	}

	if envReadsSecrets(step.Env) {
		return true
	}

	for _, v := range step.With {
		if readsSecret(v) {
			return true
		}
	}

	return false
}

// stepUndeclaredVersionReads returns a finding for each of $RELEASE_VERSION
// and $TAG that the run block of step reads with no env declaration.
func stepUndeclaredVersionReads(job string, step workflowStep) []string {
	var out []string

	for _, key := range []string{"RELEASE_VERSION", "TAG"} {
		if _, ok := step.Env[key]; !ok && strings.Contains(step.Run, "$"+key) {
			out = append(out, "job "+job+": version as data: a run block reads $"+key+
				" with no env declaration")
		}
	}

	return out
}

// checkWorkflowRuns checks that no run block holds a template value and that
// no job outside publish and release-draft reads a secret. It runs on every
// job in the file, so an extra job cannot hide a template or a secret read.
func checkWorkflowRuns(wf workflowFile) []string {
	var out []string

	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		job := wf.Jobs[name]

		for _, step := range job.Steps {
			if strings.Contains(step.Run, "${{") {
				out = append(out, "job "+name+": version as data: a run block holds a template value")
			}

			out = append(out, stepUndeclaredVersionReads(name, step)...)

			if name != "publish" && name != "release-draft" && stepReadsSecrets(step) {
				out = append(out, "job "+name+": permissions: the job reads secrets.")
			}
		}
	}

	return out
}

// checkWorkflowEnvSecrets checks the file env and the job env of every job
// that must not read a secret. The step env is part of checkWorkflowRuns.
// An archive job reads no secret at the file, the job or the step level.
func checkWorkflowEnvSecrets(path string, wf workflowFile) []string {
	var out []string

	if envReadsSecrets(wf.Env) {
		out = append(out, "workflow "+path+": permissions: the file env reads secrets.")
	}

	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		if name == "publish" || name == "release-draft" {
			continue
		}

		if envReadsSecrets(wf.Jobs[name].Env) {
			out = append(out, "job "+name+": permissions: the job env reads secrets.")
		}
	}

	return out
}

// checkWorkflowCheckouts checks that each checkout of every job keeps no
// credential. It runs on every job in the file, so an extra job cannot hide
// a checkout that keeps its token.
func checkWorkflowCheckouts(wf workflowFile) []string {
	var out []string

	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		for _, step := range wf.Jobs[name].Steps {
			if !strings.HasPrefix(step.Uses, "actions/checkout@") {
				continue
			}

			keep, hasKeep := step.With["persist-credentials"]
			if !hasKeep {
				out = append(out, "job "+name+": permissions: the checkout lacks persist-credentials false")
				continue
			}

			disabled, isBool := keep.(bool)
			if !isBool || disabled {
				out = append(out, "job "+name+": permissions: the checkout lacks persist-credentials false")
			}
		}
	}

	return out
}

// checkWorkflowAuthorization checks that the actor check of release-draft
// runs before the download of the stored files.
func checkWorkflowAuthorization(wf workflowFile) []string {
	var out []string

	draft, ok := wf.Jobs["release-draft"]
	if !ok {
		return nil
	}

	actor := -1
	download := -1

	for i, step := range draft.Steps {
		if step.Name == "Check that the actor has release permission" {
			actor = i
		}

		if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
			download = i
		}
	}

	if actor < 0 {
		out = append(out, "job release-draft: authorization at the point of use: the actor step is missing")
	}

	if download < 0 {
		out = append(out, "job release-draft: job order: the draft job downloads no artifact")
	}

	if actor >= 0 && download >= 0 && actor > download {
		out = append(out, "job release-draft: authorization at the point of use: "+
			"the actor step comes after the download step")
	}

	return out
}

const (
	actorStepName    = "Check that the actor has release permission"
	tagStepName      = "Check that the tag is a final version tag"
	actorCompareText = `[ "$PERMISSION" != "admin" ] && [ "$PERMISSION" != "maintain" ]`
	checksumText     = "sha256sum --check --strict SHA256SUMS"

	actorRunText = `PERMISSION="$(gh api "repos/$REPO/collaborators/$ACTOR/permission" --jq .permission)" || {
  echo "::error::Release permission check failed for the actor. An actor with release permission must start the tag."
  exit 1
}
if ` + actorCompareText + `; then
  echo "::error::The actor has repository permission $PERMISSION, not release permission. An actor with release permission must start the tag."
  exit 1
fi
`
)

// expectedActorStep returns the exact actor step of publish and release-draft.
func expectedActorStep() workflowStep {
	env := map[string]any{
		"ACTOR": "${{ github.actor }}",
		"REPO":  "${{ github.repository }}",
	}
	// The key is split so that gosec G101 does not flag this test fixture as
	// a hardcoded credential: the value is a workflow expression, not a token.
	env["GH_"+"TOKEN"] = "${{ secrets.GITHUB_TOKEN }}"

	return workflowStep{
		Name: actorStepName,
		Uses: "",
		With: nil,
		Env:  env,
		Run:  actorRunText,
	}
}

// expectedTagMessage answers the message of the exact tag-format step of one job.
func expectedTagMessage(name string) string {
	switch name {
	case "archives-build":
		return "This run builds no archive."
	case "publish":
		return "This run publishes no image."
	case "release-draft":
		return "This run creates no release."
	default:
		return ""
	}
}

// expectedTagStep returns the exact tag-format step of one job.
func expectedTagStep(name string) workflowStep {
	message := expectedTagMessage(name)

	return workflowStep{
		Name: tagStepName,
		Uses: "",
		With: nil,
		Env:  map[string]any{"TAG": "${{ github.ref_name }}"},
		Run: `if ! echo "$TAG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "::error::Not a final version tag (vX.Y.Z with no suffix). ` + message + `"
  exit 1
fi
`,
	}
}

// sameEnv answers whether two env maps hold the same keys and values.
func sameEnv(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range b {
		if got, ok := a[k]; !ok || got != v {
			return false
		}
	}

	return true
}

// stepMatches answers whether the name, the env map and the run text of two
// steps are equal.
func stepMatches(got, want workflowStep) bool {
	return got.Name == want.Name && sameEnv(got.Env, want.Env) && got.Run == want.Run
}

// checkWorkflowActorComparison checks that the actor step of publish and of
// release-draft is exactly the expected step and that its run text refuses
// each permission except admin and maintain. A missing step of release-draft
// is reported by checkWorkflowAuthorization.
func checkWorkflowActorComparison(wf workflowFile) []string {
	var out []string

	want := expectedActorStep()

	for _, name := range []string{"publish", "release-draft"} {
		found := false

		for _, step := range wf.Jobs[name].Steps {
			if step.Name != actorStepName {
				continue
			}

			found = true

			if !stepMatches(step, want) {
				out = append(out, "job "+name+": authorization at the point of use: "+
					"the actor step differs from the expected actor step")
			}

			out = append(out, actorRunFindings(name, step.Run)...)
		}

		if !found && name == "publish" {
			out = append(out, "job publish: authorization at the point of use: the actor step is missing")
		}
	}

	return out
}

// actorRunFindings checks the run text of one actor step. Each condition is
// separate so that a test row can isolate it.
func actorRunFindings(name, run string) []string {
	var out []string

	if !strings.Contains(run, "collaborators") {
		out = append(out, "job "+name+": authorization at the point of use: the actor step has no permission check")
	}

	if strings.Count(run, `"$PERMISSION" `) != 2 ||
		strings.Contains(run, `"$PERMISSION" ==`) ||
		!strings.Contains(run, "exit 1") {
		out = append(out, "job "+name+": authorization at the point of use: "+
			"the actor step must refuse each permission except admin and maintain")
	}

	return out
}

// hostileReleaseTags lists tags that the tag-format step must refuse.
func hostileReleaseTags() []string {
	return []string{"v1.2.3-rc1", "v1.2.3+build", "v1.2", "v1.2.3.4", "1.2.3", "v1.2.3 ", "vx.y.z", "xv1.2.3"}
}

// releaseTagRegexp reads the pattern of the grep call in the tag-format step
// of a job. The bool is false when the step or the pattern is missing.
func releaseTagRegexp(job workflowJob) (*regexp.Regexp, bool) {
	grep := regexp.MustCompile(`grep -Eq '([^']+)'`)

	for _, step := range job.Steps {
		if step.Name != tagStepName {
			continue
		}

		m := grep.FindStringSubmatch(step.Run)
		if m == nil || !strings.Contains(step.Run, "exit 1") {
			return nil, false
		}

		re, err := regexp.Compile(m[1])
		if err != nil {
			return nil, false
		}

		return re, true
	}

	return nil, false
}

// checkOneJobTagExact answers a finding when the tag-format step of one job
// differs from the exact expected step.
func checkOneJobTagExact(name string, job workflowJob, want workflowStep) ([]string, bool) {
	var out []string

	found := false

	for _, step := range job.Steps {
		if step.Name != tagStepName {
			continue
		}

		found = true

		if !stepMatches(step, want) {
			out = append(out, "job "+name+": tag format: "+
				"the tag step differs from the expected tag step")
		}
	}

	return out, found
}

// checkOneJobTagRegexp answers a finding when the tag pattern of one job
// accepts a hostile tag or refuses a final version tag.
func checkOneJobTagRegexp(name string, job workflowJob) []string {
	var out []string

	re, ok := releaseTagRegexp(job)
	if !ok {
		return []string{"job " + name + ": tag format: the final version tag step is missing"}
	}

	if !re.MatchString("v1.2.3") {
		out = append(out, "job "+name+": tag format: the step refuses v1.2.3")
	}

	for _, tag := range hostileReleaseTags() {
		if re.MatchString(tag) {
			out = append(out, "job "+name+": tag format: the step accepts the tag "+tag)
		}
	}

	return out
}

// checkOneJobTagFormat checks the exact tag-format step and the tag pattern of one job.
func checkOneJobTagFormat(name string, job workflowJob) []string {
	want := expectedTagStep(name)

	exact, found := checkOneJobTagExact(name, job, want)
	if !found {
		return append(exact, "job "+name+": tag format: the final version tag step is missing")
	}

	return append(exact, checkOneJobTagRegexp(name, job)...)
}

// checkWorkflowTagFormat checks that archives-build, publish and
// release-draft each hold the exact tag-format step and refuse a tag that is
// not vX.Y.Z.
func checkWorkflowTagFormat(wf workflowFile) []string {
	out := make([]string, 0, 3)

	for _, name := range []string{"archives-build", "publish", "release-draft"} {
		out = append(out, checkOneJobTagFormat(name, wf.Jobs[name])...)
	}

	return out
}

// checkWorkflowChecksum checks that archives-verify runs sha256sum in strict
// check mode on SHA256SUMS.
func checkWorkflowChecksum(wf workflowFile) []string {
	for _, step := range wf.Jobs["archives-verify"].Steps {
		if strings.Contains(step.Run, checksumText) {
			return nil
		}
	}

	return []string{"job archives-verify: checksum check: the job lacks " + checksumText}
}

// releaseWorkflowFindings returns one finding for each missing guard of the
// release workflow: the workflow guard, the job order, the permissions, the
// pins, the version as data and the authorization at the point of use. The
// path names the file checked: file-level findings read "workflow <path>:",
// job findings name the job and the item.
func releaseWorkflowFindingsAt(path string, raw []byte) []string {
	var wf workflowFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return []string{"workflow " + path + ": the release workflow does not parse: " + err.Error()}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return []string{"workflow " + path + ": the release workflow does not parse: " + err.Error()}
	}

	var findings []string

	findings = append(findings, checkWorkflowTriggers(path, wf)...)
	findings = append(findings, checkWorkflowFilePermissions(path, &doc)...)
	findings = append(findings, checkWorkflowTopLevelKeys(path, &doc)...)
	findings = append(findings, checkWorkflowKeyAllowlist(&doc)...)
	findings = append(findings, checkWorkflowJobPresence(wf)...)

	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		job := wf.Jobs[name]

		findings = append(findings, checkWorkflowGuard(name, job)...)
		findings = append(findings, checkWorkflowNeeds(name, job)...)
		findings = append(findings, checkWorkflowJobPermissions(name, job)...)
	}

	findings = append(findings, checkWorkflowPins(string(raw))...)
	findings = append(findings, checkWorkflowRuns(wf)...)
	findings = append(findings, checkWorkflowEnvSecrets(path, wf)...)
	findings = append(findings, checkWorkflowCheckouts(wf)...)
	findings = append(findings, checkWorkflowAuthorization(wf)...)
	findings = append(findings, checkWorkflowActorComparison(wf)...)
	findings = append(findings, checkWorkflowTagFormat(wf)...)
	findings = append(findings, checkWorkflowChecksum(wf)...)

	return findings
}

// releaseWorkflowFindings checks raw as the live release workflow. A caller
// that checks another file uses releaseWorkflowFindingsAt.
func releaseWorkflowFindings(raw []byte) []string {
	return releaseWorkflowFindingsAt(liveWorkflowPath, raw)
}

// checkArchiveJobReadsNoSecret fails the test when an archive job reads a
// secret at the file, the job or the step level.
func checkArchiveJobReadsNoSecret(tb testing.TB, wf workflowFile, name string) {
	tb.Helper()

	if envReadsSecrets(wf.Env) {
		tb.Errorf("the file env gives job %s a secret", name)
	}

	if envReadsSecrets(wf.Jobs[name].Env) {
		tb.Errorf("job %s reads a secret in its job env", name)
	}

	for _, step := range wf.Jobs[name].Steps {
		if stepReadsSecrets(step) {
			tb.Errorf("job %s reads secrets in step %q", name, step.Name)
		}
	}
}

// checkSameGuardAsPublish fails the test when a job guard differs from publish.
func checkSameGuardAsPublish(tb testing.TB, wf workflowFile, name string) {
	tb.Helper()

	if wf.Jobs[name].If != wf.Jobs["publish"].If {
		tb.Errorf("job %s uses a guard text that differs from publish", name)
	}
}

// checkActorBeforeDownload fails the test when the draft actor step is
// missing or runs after the download step.
func checkActorBeforeDownload(tb testing.TB, wf workflowFile) {
	tb.Helper()

	draft := wf.Jobs["release-draft"]
	actor := -1
	download := -1

	for i, step := range draft.Steps {
		if step.Name == "Check that the actor has release permission" {
			actor = i
		}

		if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
			download = i
		}
	}

	if actor < 0 {
		tb.Error("release-draft lacks the actor step")
	}

	if download < 0 {
		tb.Error("release-draft lacks the download step")
	}

	if actor >= 0 && download >= 0 && actor > download {
		tb.Error("release-draft runs the actor step after the download step")
	}
}

// releaseWorkflowFindingsForSources runs releaseWorkflowFindings on every
// source and returns the combined findings, so a weak second file cannot hide
// behind a clean first file.
func releaseWorkflowFindingsForSources(sources []workflowSource) []string {
	all := make([]string, 0, len(sources))

	for _, src := range sources {
		all = append(all, releaseWorkflowFindingsAt(src.path, src.raw)...)
	}

	return all
}

// TestReleaseWorkflowArchiveJobs checks the structure of the staged release
// workflow: no finding, no secret read in the archive jobs, the same guard
// text in each new job as in publish, and the actor step before the download.
// It checks every source that releaseWorkflowSources returns, not only the
// first one, so a weak live file cannot hide behind a clean staged copy.
func TestReleaseWorkflowArchiveJobs(t *testing.T) {
	t.Parallel()

	checkReleaseWorkflowSources(t, releaseWorkflowSources(t))
}

// checkReleaseWorkflowSources runs every check of the archive jobs on every
// source: the findings, the secret read, the guard text and the actor step.
func checkReleaseWorkflowSources(tb testing.TB, sources []workflowSource) {
	tb.Helper()

	if findings := releaseWorkflowFindingsForSources(sources); len(findings) != 0 {
		tb.Errorf(
			"the release workflow has %d findings:\n%s",
			len(findings),
			strings.Join(findings, "\n"),
		)
	}

	for _, src := range sources {
		var wf workflowFile
		if err := yaml.Unmarshal(src.raw, &wf); err != nil {
			tb.Fatal(err)
		}

		checkArchiveJobReadsNoSecret(tb, wf, "archives-build")
		checkArchiveJobReadsNoSecret(tb, wf, "archives-verify")
		checkSameGuardAsPublish(tb, wf, "archives-build")
		checkSameGuardAsPublish(tb, wf, "archives-verify")
		checkSameGuardAsPublish(tb, wf, "release-draft")
		checkActorBeforeDownload(tb, wf)
	}
}

// recordingTB records the errors that a check reports, so a test can assert
// that a check finds a weak file without failing itself.
type recordingTB struct {
	testing.TB

	messages []string
}

func (r *recordingTB) Error(args ...any) {
	r.messages = append(r.messages, fmt.Sprint(args...))
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

// TestReleaseWorkflowArchiveJobsChecksEverySource fails when the archive jobs
// test checks only the first source that releaseWorkflowSources returns: the
// second row holds a weak live file whose guard differs from publish, so a
// test that reads only the first file misses it.
func TestReleaseWorkflowArchiveJobsChecksEverySource(t *testing.T) {
	t.Parallel()

	cleanPath, clean := releaseWorkflowBytes(t)

	if findings := releaseWorkflowFindingsAt(cleanPath, clean); len(findings) != 0 {
		t.Fatalf(
			"the clean workflow has %d findings:\n%s",
			len(findings),
			strings.Join(findings, "\n"),
		)
	}

	weakRaw := replaceInJob(string(clean), "release-draft", expectedReleaseGuard, "github.event_name == 'push'")
	if weakRaw == string(clean) {
		t.Fatal("the every-source row changed nothing: the weak guard equals the clean guard")
	}

	weakFindings := releaseWorkflowFindings([]byte(weakRaw))

	foundWeak := false

	for _, finding := range weakFindings {
		if strings.HasPrefix(finding, "job release-draft:") && strings.Contains(finding, "guard") {
			foundWeak = true
		}
	}

	if !foundWeak {
		t.Fatalf(
			"the weak live file yields no guard finding:\n%s",
			strings.Join(weakFindings, "\n"),
		)
	}

	sources := []workflowSource{
		{path: cleanPath, raw: clean},
		{path: liveWorkflowPath, raw: []byte(weakRaw)},
	}

	// The clean first source alone raises no error.
	first := &recordingTB{TB: t, messages: nil}
	checkReleaseWorkflowSources(first, sources[:1])

	if len(first.messages) != 0 {
		t.Fatalf("the clean first source raises errors:\n%s", strings.Join(first.messages, "\n"))
	}

	// The clean-first, weak-second list must raise a release-draft guard error.
	both := &recordingTB{TB: t, messages: nil}
	checkReleaseWorkflowSources(both, sources)

	found := false

	for _, msg := range both.messages {
		if strings.Contains(msg, "job release-draft:") && strings.Contains(msg, "guard") {
			found = true
		}
	}

	if !found {
		t.Fatalf(
			"checkReleaseWorkflowSources misses the weak second file, want a guard finding for release-draft:\n%s",
			strings.Join(both.messages, "\n"),
		)
	}
}

// workflowMutation is one hostile copy of the good workflow file.
type workflowMutation struct {
	name   string
	job    string
	item   string
	mutate func(string) string
}

// workflowGuardTriggerMutations returns hostile copies for the guard and the
// trigger allowlist.
func workflowGuardTriggerMutations(guard string) []workflowMutation {
	trigger := func(name, extra string) workflowMutation {
		return workflowMutation{
			name,
			"workflow",
			"guard",
			func(s string) string {
				return strings.Replace(
					s,
					"  push:\n    tags:\n      - \"v*\"",
					"  push:\n    tags:\n      - \"v*\"\n"+extra,
					1,
				)
			},
		}
	}

	return []workflowMutation{
		{
			"a job with no guard",
			"archives-build",
			"guard",
			func(s string) string {
				old := "  archives-build:\n    # Same guard as the publish job: a tag event in the main repository.\n    if: " + guard
				return strings.Replace(s, old, "  archives-build:", 1)
			},
		},
		{
			"no repository clause",
			"archives-build",
			"guard",
			func(s string) string {
				return strings.Replace(s, " && github.repository == 'OpenDustpan/OpenDustpan'", "", 1)
			},
		},
		trigger("a pull_request trigger", "  pull_request:"),
		trigger("a pull_request_target trigger", "  pull_request_target:"),
		trigger("a workflow_run trigger", "  workflow_run:"),
		trigger("a workflow_dispatch trigger", "  workflow_dispatch:"),
		trigger("a schedule trigger", "  schedule:"),
		{
			"a push with a branch filter",
			"workflow",
			"guard",
			func(s string) string {
				return strings.Replace(
					s,
					"  push:\n    tags:\n      - \"v*\"",
					"  push:\n    tags:\n      - \"v*\"\n    branches:\n      - main",
					1,
				)
			},
		},
		{
			"on push with an extra key",
			"workflow",
			"extra key",
			func(s string) string {
				return strings.Replace(
					s,
					"  push:\n    tags:\n      - \"v*\"",
					"  push:\n    tags:\n      - \"v*\"\n    paths:\n      - \"**\"",
					1,
				)
			},
		},
		{
			"on push with an extra tag",
			"workflow",
			"tags is not",
			func(s string) string {
				return strings.Replace(
					s,
					"  push:\n    tags:\n      - \"v*\"",
					"  push:\n    tags:\n      - \"v*\"\n      - latest",
					1,
				)
			},
		},
		{
			"publish with no needs",
			"publish",
			"job order",
			func(s string) string {
				return strings.Replace(s, "    needs: archives-verify\n", "", 1)
			},
		},
		{
			"release-draft with no needs publish",
			"release-draft",
			"job order",
			func(s string) string {
				return strings.Replace(s, "needs: [archives-verify, publish]", "needs: [archives-verify]", 1)
			},
		},
		{
			"archives-verify with no needs archives-build",
			"archives-verify",
			"job order",
			func(s string) string {
				return strings.Replace(s, "    needs: archives-build\n", "", 1)
			},
		},
	}
}

// workflowPermissionMutations returns hostile copies for the exact permission
// maps and for the extra job.
func workflowPermissionMutations() []workflowMutation {
	return []workflowMutation{
		{
			"a job with no permissions",
			"archives-build",
			"permissions",
			func(s string) string {
				return strings.Replace(s, "    permissions:\n      contents: read\n", "", 1)
			},
		},
		{
			"contents write for the file",
			"workflow",
			"permissions",
			func(s string) string {
				return strings.Replace(s, "permissions: {}", "permissions:\n  contents: write", 1)
			},
		},
		{
			"the file permissions line deleted",
			"workflow",
			"permissions",
			func(s string) string {
				return strings.Replace(s, "permissions: {}\n", "", 1)
			},
		},
		{
			"a non-empty file permissions map",
			"workflow",
			"permissions",
			func(s string) string {
				return strings.Replace(s, "permissions: {}", "permissions:\n  contents: read", 1)
			},
		},
		{
			"contents write in archives-build",
			"archives-build",
			"permissions",
			func(s string) string {
				return strings.Replace(s, "      contents: read", "      contents: write", 1)
			},
		},
		{
			"packages write in release-draft",
			"release-draft",
			"permissions",
			func(s string) string {
				old := "    permissions:\n      contents: write"
				return strings.Replace(s, old, "    permissions:\n      contents: write\n      packages: write", 1)
			},
		},
		{
			"an extra id-token scope in release-draft",
			"release-draft",
			"permissions",
			func(s string) string {
				old := "    permissions:\n      contents: write"
				return strings.Replace(s, old, "    permissions:\n      contents: write\n      id-token: write", 1)
			},
		},
		{
			"an extra actions scope in release-draft",
			"release-draft",
			"permissions",
			func(s string) string {
				old := "    permissions:\n      contents: write"
				return strings.Replace(s, old, "    permissions:\n      contents: write\n      actions: write", 1)
			},
		},
		{
			"an extra id-token scope in archives-build",
			"archives-build",
			"permissions",
			func(s string) string {
				return replaceInJob(
					s,
					"archives-build",
					"      contents: read",
					"      contents: read\n      id-token: write",
				)
			},
		},
		{
			"an extra actions scope in archives-build",
			"archives-build",
			"permissions",
			func(s string) string {
				return replaceInJob(
					s,
					"archives-build",
					"      contents: read",
					"      contents: read\n      actions: write",
				)
			},
		},
		{
			"an extra job",
			"extra",
			"guard",
			func(s string) string {
				return s + "\n  extra:\n    runs-on: ubuntu-26.04\n    permissions:\n      contents: write\n    steps:\n      - run: echo \"${{ github.event.head_commit.message }}\"\n"
			},
		},
	}
}

// workflowPinRunMutations returns hostile copies for pins, run blocks,
// checkouts and the actor step.
func workflowPinRunMutations() []workflowMutation {
	return []workflowMutation{
		{
			"a tag pin",
			"archives-build",
			"pins",
			func(s string) string {
				old := "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
				return strings.Replace(s, old, "actions/checkout@v7", 1)
			},
		},
		{
			"a short SHA",
			"archives-build",
			"pins",
			func(s string) string {
				old := "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
				return strings.Replace(s, old, "actions/checkout@3d3c42e5 # v7.0.1", 1)
			},
		},
		{
			"a pin with no release comment",
			"archives-build",
			"pins",
			func(s string) string {
				old := "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
				return strings.Replace(s, old, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", 1)
			},
		},
		{
			"a pin with comment # v1",
			"archives-build",
			"pins",
			func(s string) string {
				old := "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
				return strings.Replace(s, old, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v1", 1)
			},
		},
		{
			"a pin with comment # vfoo",
			"archives-build",
			"pins",
			func(s string) string {
				old := "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1"
				return strings.Replace(s, old, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # vfoo", 1)
			},
		},
		{
			"a run line with a template value",
			"archives-build",
			"version as data",
			func(s string) string {
				old := "run: ./scripts/release-build.sh \"$RELEASE_VERSION\" \"$RELEASE_OUT\""
				return strings.Replace(s, old, "run: echo \"${{ github.ref_name }}\"", 1)
			},
		},
		{
			"the build step with no env block",
			"archives-build",
			"no env declaration",
			func(s string) string {
				old := "          RELEASE_VERSION: ${{ github.ref_name }}\n          RELEASE_OUT: ${{ runner.temp }}/release\n        run: ./scripts/release-build.sh"
				return strings.Replace(s, old, "        run: ./scripts/release-build.sh", 1)
			},
		},
		{
			"a checkout with no persist-credentials false",
			"archives-build",
			"persist-credentials",
			func(s string) string {
				return strings.Replace(s, "          persist-credentials: false", "", 1)
			},
		},
		{
			"a checkout with persist-credentials true",
			"archives-build",
			"persist-credentials",
			func(s string) string {
				return strings.Replace(
					s,
					"          persist-credentials: false",
					"          persist-credentials: true",
					1,
				)
			},
		},
		{
			"a checkout with persist-credentials as the string false",
			"archives-build",
			"persist-credentials",
			func(s string) string {
				return strings.Replace(
					s,
					"          persist-credentials: false",
					"          persist-credentials: \"false\"",
					1,
				)
			},
		},
		{
			"a publish checkout with no persist-credentials false",
			"publish",
			"persist-credentials",
			func(s string) string {
				return replaceInJob(s, "publish", "          persist-credentials: false", "")
			},
		},
		{
			"the actor step removed",
			"release-draft",
			"authorization",
			func(s string) string {
				start := strings.Index(s, "  release-draft:")
				if start < 0 {
					return s
				}

				head := s[:start]
				tail := s[start:]
				step := "      - name: Check that the actor has release permission"
				idx := strings.Index(tail, step)
				if idx < 0 {
					return s
				}

				rest := tail[idx:]
				next := strings.Index(rest, "      - name: Check that the tag is a final version tag")
				if next < 0 {
					return head + tail[:idx]
				}

				return head + tail[:idx] + rest[next:]
			},
		},
	}
}

// workflowMutations returns the hostile copies: each one must yield at least
// one finding that names the job and the item.
func workflowMutations() []workflowMutation {
	guard := "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && github.repository == 'OpenDustpan/OpenDustpan'"

	out := workflowGuardTriggerMutations(guard)
	out = append(out, workflowPermissionMutations()...)
	out = append(out, workflowPinRunMutations()...)
	out = append(out, securityReviewMutations()...)

	return out
}

// mutateJob applies fn to the text of one job and leaves the rest unchanged.
// A job that is not in the file leaves the file unchanged.
func mutateJob(s, job string, fn func(string) string) string {
	start := strings.Index(s, "\n  "+job+":\n")
	if start < 0 {
		return s
	}

	start++

	end := len(s)
	if next := regexp.MustCompile(`\n {2}[A-Za-z0-9_-]+:\n`).FindStringIndex(s[start:]); next != nil {
		end = start + next[0] + 1
	}

	return s[:start] + fn(s[start:end]) + s[end:]
}

// replaceInJob replaces the first old text inside one job.
func replaceInJob(s, job, old, repl string) string {
	return mutateJob(s, job, func(j string) string { return strings.Replace(j, old, repl, 1) })
}

// removeStepInJob removes the step with the given name from one job.
func removeStepInJob(s, job, stepName string) string {
	return mutateJob(s, job, func(j string) string {
		idx := strings.Index(j, "      - name: "+stepName)
		if idx < 0 {
			return j
		}

		_, after, found := strings.Cut(j[idx:], "\n\n")
		if !found {
			return j[:idx]
		}

		return j[:idx] + after
	})
}

// moreWorkflowMutations returns the hostile copies for the guard of each job,
// the tag format, the actor comparison, the checksum check, the secret read and
// the step order.
func moreWorkflowMutations() []workflowMutation {
	guardLine := "    if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && " +
		"github.repository == 'OpenDustpan/OpenDustpan'\n"
	compare := actorCompareText
	rows := make([]workflowMutation, 0, 22)
	rows = append(rows, []workflowMutation{
		{"archives-verify with no guard", "archives-verify", "guard", func(s string) string {
			return replaceInJob(s, "archives-verify", guardLine, "")
		}},
		{"publish with no guard", "publish", "guard", func(s string) string {
			return replaceInJob(s, "publish", guardLine, "")
		}},
		{"release-draft with no guard", "release-draft", "guard", func(s string) string {
			return replaceInJob(s, "release-draft", guardLine, "")
		}},
		{"release-draft with no repository clause", "release-draft", "guard", func(s string) string {
			return replaceInJob(s, "release-draft", " && github.repository == 'OpenDustpan/OpenDustpan'", "")
		}},
		{"publish with no repository clause", "publish", "guard", func(s string) string {
			return replaceInJob(s, "publish", " && github.repository == 'OpenDustpan/OpenDustpan'", "")
		}},
		{"archives-verify with no checksum check", "archives-verify", "checksum check", func(s string) string {
			return replaceInJob(s, "archives-verify", "          (cd \"$RELEASE_OUT\" && "+checksumText+")\n", "")
		}},
		{"archives-verify with a lax checksum check", "archives-verify", "checksum check", func(s string) string {
			return replaceInJob(s, "archives-verify", "--check --strict", "--check")
		}},
		{"archives-build reads a secret", "archives-build", "secrets", func(s string) string {
			return replaceInJob(s, "archives-build", "          RELEASE_OUT:",
				"          TOKEN: ${{ secrets.GITHUB_TOKEN }}\n          RELEASE_OUT:")
		}},
		{"archives-verify reads a secret", "archives-verify", "secrets", func(s string) string {
			return replaceInJob(s, "archives-verify", "          RELEASE_OUT:",
				"          TOKEN: ${{ secrets.GITHUB_TOKEN }}\n          RELEASE_OUT:")
		}},
		{"the actor step after the download", "release-draft", "authorization", func(s string) string {
			return mutateJob(s, "release-draft", func(j string) string {
				a := strings.Index(j, "      - name: "+actorStepName)
				b := strings.Index(j, "      - name: "+tagStepName)
				c := strings.Index(j, "      - name: Create the draft release")

				if a < 0 || b < a || c < b {
					return j
				}

				return j[:a] + j[b:c] + j[a:b] + j[c:]
			})
		}},
		{"release-draft accepts admin only", "release-draft", "authorization", func(s string) string {
			return replaceInJob(s, "release-draft", compare, `[ "$PERMISSION" != "admin" ]`)
		}},
		{"release-draft accepts write", "release-draft", "authorization", func(s string) string {
			return replaceInJob(s, "release-draft", `"$PERMISSION" != "maintain"`, `"$PERMISSION" != "write"`)
		}},
		{"release-draft accepts any one of the two", "release-draft", "authorization", func(s string) string {
			return replaceInJob(s, "release-draft", `] && [`, `] || [`)
		}},
		{"publish accepts maintain only", "publish", "authorization", func(s string) string {
			return replaceInJob(s, "publish", compare, `[ "$PERMISSION" != "maintain" ]`)
		}},
		{"publish with no permission lookup", "publish", "authorization", func(s string) string {
			return replaceInJob(s, "publish", "collaborators", "contributors")
		}},
		{"all four guards with no repository clause", "archives-build", "guard", func(s string) string {
			return strings.ReplaceAll(s, " && github.repository == 'OpenDustpan/OpenDustpan'", "")
		}},
		{"all four guards with no push event clause", "archives-build", "guard", func(s string) string {
			return strings.ReplaceAll(s, "github.event_name == 'push' && ", "")
		}},
		{"all four guards with || always()", "archives-build", "guard", func(s string) string {
			return strings.ReplaceAll(
				s,
				"github.repository == 'OpenDustpan/OpenDustpan'",
				"github.repository == 'OpenDustpan/OpenDustpan' || always()",
			)
		}},
		{"release-draft with == comparison", "release-draft", "authorization", func(s string) string {
			return replaceInJob(s, "release-draft", `[ "$PERMISSION" != "admin" ]`, `[ "$PERMISSION" == "admin" ]`)
		}},
		{"publish with == for maintain", "publish", "authorization", func(s string) string {
			return replaceInJob(s, "publish", `[ "$PERMISSION" != "maintain" ]`, `[ "$PERMISSION" == "maintain" ]`)
		}},
		{"release-draft with one permission comparison", "release-draft", "authorization", func(s string) string {
			return replaceInJob(s, "release-draft", ` && [ "$PERMISSION" != "maintain" ]`, "")
		}},
		{"release-draft actor step with no exit 1", "release-draft", "authorization", func(s string) string {
			return mutateJob(s, "release-draft", func(j string) string {
				a := strings.Index(j, "      - name: "+actorStepName)
				b := strings.Index(j, "      - name: "+tagStepName)

				if a < 0 || b < a {
					return j
				}

				return j[:a] + strings.ReplaceAll(j[a:b], "exit 1", "exit 0") + j[b:]
			})
		}},
	}...)

	for _, job := range []string{"archives-build", "publish", "release-draft"} {
		rows = append(rows,
			workflowMutation{"the tag step removed from " + job, job, "tag format", func(s string) string {
				return removeStepInJob(s, job, tagStepName)
			}},
			// The pattern with no end anchor accepts v1.2.3-rc1.
			workflowMutation{"a tag step that accepts v1.2.3-rc1 in " + job, job, "tag format", func(s string) string {
				return replaceInJob(s, job, `[0-9]+$'`, `[0-9]+'`)
			}},
		)
	}

	return rows
}

// securityReviewMutations returns the hostile copies of the staged file from
// the security review: a step "if" or "continue-on-error", a job
// "continue-on-error", an actor env value, a job-level secret, a file-level
// secret, a step guard, an early exit, the index and toJSON forms of a secret
// at each level and a top-level defaults key.
func securityReviewMutations() []workflowMutation {
	return []workflowMutation{
		{
			"P1 continue-on-error true on the release-draft actor step",
			"release-draft",
			"step keys",
			func(s string) string {
				return replaceInJob(s, "release-draft",
					"      - name: "+actorStepName+"\n",
					"      - name: "+actorStepName+"\n        continue-on-error: true\n")
			},
		},
		{
			"P2 if false on the release-draft actor step",
			"release-draft",
			"step keys",
			func(s string) string {
				return replaceInJob(s, "release-draft",
					"      - name: "+actorStepName+"\n",
					"      - name: "+actorStepName+"\n        if: false\n")
			},
		},
		{
			"P3 if false on the publish actor step",
			"publish",
			"step keys",
			func(s string) string {
				return replaceInJob(s, "publish",
					"      - name: "+actorStepName+"\n",
					"      - name: "+actorStepName+"\n        if: false\n")
			},
		},
		{
			"P4 continue-on-error true on the archives-build tag step",
			"archives-build",
			"step keys",
			func(s string) string {
				return replaceInJob(s, "archives-build",
					"      - name: "+tagStepName+"\n",
					"      - name: "+tagStepName+"\n        continue-on-error: true\n")
			},
		},
		{
			"continue-on-error true on the archives-build job",
			"archives-build",
			"job keys",
			func(s string) string {
				return replaceInJob(s, "archives-build",
					"    runs-on: ubuntu-26.04\n",
					"    runs-on: ubuntu-26.04\n    continue-on-error: true\n")
			},
		},
		{
			"P5 an actor value other than github.actor",
			"release-draft",
			"authorization",
			func(s string) string {
				return replaceInJob(s, "release-draft",
					"          ACTOR: ${{ github.actor }}",
					"          ACTOR: some-admin")
			},
		},
		{
			"P6 a job-level secret in archives-build",
			"archives-build",
			"secrets",
			func(s string) string {
				return replaceInJob(s, "archives-build",
					"    runs-on: ubuntu-26.04\n",
					"    runs-on: ubuntu-26.04\n    env:\n      TOKEN: ${{ secrets.DOCKERHUB_TOKEN }}\n")
			},
		},
		{
			"P8 a file-level secret",
			"workflow",
			"secrets",
			func(s string) string {
				return strings.Replace(
					s,
					"  HUB_IMAGE: docker.io/opendustpan/opendustpan\n",
					"  HUB_IMAGE: docker.io/opendustpan/opendustpan\n"+
						"  DOCKERHUB_TOKEN: ${{ secrets.DOCKERHUB_TOKEN }}\n",
					1,
				)
			},
		},
		{
			"P9 if always() on the draft release step",
			"release-draft",
			"step keys",
			func(s string) string {
				return replaceInJob(s, "release-draft",
					"      - name: Create the draft release\n",
					"      - name: Create the draft release\n        if: always()\n")
			},
		},
		{
			"P10 exit 0 before the actor permission call",
			"release-draft",
			"authorization",
			func(s string) string {
				return replaceInJob(s, "release-draft",
					`          PERMISSION="$(gh api`,
					"          exit 0\n"+`          PERMISSION="$(gh api`)
			},
		},
		{
			"B6 the index form in a step env",
			"archives-build",
			"secrets",
			func(s string) string {
				return replaceInJob(s, "archives-build",
					"          RELEASE_OUT: ${{ runner.temp }}/release\n",
					"          RELEASE_OUT: ${{ runner.temp }}/release\n"+
						"          TOKEN: ${{ secrets['DOCKERHUB_TOKEN'] }}\n")
			},
		},
		{
			"B6 toJSON(secrets) in a step env",
			"archives-build",
			"secrets",
			func(s string) string {
				return replaceInJob(s, "archives-build",
					"          RELEASE_OUT: ${{ runner.temp }}/release\n",
					"          RELEASE_OUT: ${{ runner.temp }}/release\n"+
						"          TOKEN: ${{ toJSON(secrets) }}\n")
			},
		},
		{
			"B6 the index form in a step with",
			"archives-build",
			"secrets",
			func(s string) string {
				return replaceInJob(s, "archives-build",
					"          name: release-archives\n",
					"          name: release-archives\n"+
						"          token: ${{ secrets['DOCKERHUB_TOKEN'] }}\n")
			},
		},
		{
			"B6 toJSON(secrets) in a step with",
			"archives-build",
			"secrets",
			func(s string) string {
				return replaceInJob(s, "archives-build",
					"          name: release-archives\n",
					"          name: release-archives\n"+
						"          token: ${{ toJSON(secrets) }}\n")
			},
		},
		{
			"B6 the index form in a job env",
			"archives-build",
			"secrets",
			func(s string) string {
				return replaceInJob(s, "archives-build",
					"    runs-on: ubuntu-26.04\n",
					"    runs-on: ubuntu-26.04\n    env:\n"+
						"      TOKEN: ${{ secrets['DOCKERHUB_TOKEN'] }}\n")
			},
		},
		{
			"B6 toJSON(secrets) in a job env",
			"archives-build",
			"secrets",
			func(s string) string {
				return replaceInJob(s, "archives-build",
					"    runs-on: ubuntu-26.04\n",
					"    runs-on: ubuntu-26.04\n    env:\n"+
						"      TOKEN: ${{ toJSON(secrets) }}\n")
			},
		},
		{
			"B6 the index form in the file env",
			"workflow",
			"secrets",
			func(s string) string {
				return strings.Replace(
					s,
					"  HUB_IMAGE: docker.io/opendustpan/opendustpan\n",
					"  HUB_IMAGE: docker.io/opendustpan/opendustpan\n"+
						"  DOCKERHUB_TOKEN: ${{ secrets['DOCKERHUB_TOKEN'] }}\n",
					1,
				)
			},
		},
		{
			"B6 toJSON(secrets) in the file env",
			"workflow",
			"secrets",
			func(s string) string {
				return strings.Replace(
					s,
					"  HUB_IMAGE: docker.io/opendustpan/opendustpan\n",
					"  HUB_IMAGE: docker.io/opendustpan/opendustpan\n"+
						"  DOCKERHUB_TOKEN: ${{ toJSON(secrets) }}\n",
					1,
				)
			},
		},
		{
			"B6 a top-level defaults key",
			"workflow",
			"top-level keys",
			func(s string) string {
				return strings.Replace(
					s,
					"permissions: {}\n",
					"permissions: {}\ndefaults:\n  run:\n    shell: bash\n",
					1,
				)
			},
		},
	}
}

// TestReleaseWorkflowTagFormatRefusesHostileTags runs the pattern of each
// tag-format step on hostile tags and on a final version tag.
func TestReleaseWorkflowTagFormatRefusesHostileTags(t *testing.T) {
	t.Parallel()

	_, raw := releaseWorkflowBytes(t)

	var wf workflowFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"archives-build", "publish", "release-draft"} {
		re, ok := releaseTagRegexp(wf.Jobs[name])
		if !ok {
			t.Errorf("job %s has no tag-format step", name)

			continue
		}

		if !re.MatchString("v1.2.3") {
			t.Errorf("job %s refuses v1.2.3", name)
		}

		for _, tag := range hostileReleaseTags() {
			if re.MatchString(tag) {
				t.Errorf("job %s accepts the tag %q", name, tag)
			}
		}
	}
}

// checkOneMutation runs the checker on one hostile copy and wants a finding
// that names the job and the item. File-level findings read
// "workflow <path>:", so a file-level row names "workflow" as the job and
// the check wants the full "workflow <path>:" prefix with the item. Job
// findings must match the prefix "job <name>:", not a substring of the job
// name.
func checkOneMutation(t *testing.T, path, good string, tt workflowMutation) {
	t.Helper()

	mutated := tt.mutate(good)
	if mutated == good {
		t.Fatalf("the mutation %q changed nothing", tt.name)
	}

	findings := releaseWorkflowFindingsAt(path, []byte(mutated))
	if len(findings) == 0 {
		t.Fatalf("the mutation %q yields no finding", tt.name)
	}

	for _, finding := range findings {
		if tt.job == "workflow" {
			if strings.HasPrefix(finding, "workflow "+path+":") && strings.Contains(finding, tt.item) {
				return
			}

			continue
		}

		if strings.HasPrefix(finding, "job "+tt.job+":") && strings.Contains(finding, tt.item) {
			return
		}
	}

	t.Errorf(
		"the mutation %q yields no finding that names %q and %q: %q",
		tt.name,
		tt.job,
		tt.item,
		strings.Join(findings, "; "),
	)
}

// TestReleaseWorkflowCheckerRefusesWeakWorkflows runs the checker on hostile
// copies of the good workflow: each copy yields at least one finding that
// names the job and the item.
func TestReleaseWorkflowCheckerRefusesWeakWorkflows(t *testing.T) {
	t.Parallel()

	path, raw := releaseWorkflowBytes(t)
	good := string(raw)

	for _, tt := range append(workflowMutations(), moreWorkflowMutations()...) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkOneMutation(t, path, good, tt)
		})
	}
}

// TestActorRunFindingsIsolatesSubConditions proves that each actor
// sub-condition is load-bearing. Each row changes exactly one condition of the
// good run, and actorRunFindings refuses it. The full-step comparison is
// checked by TestReleaseWorkflowCheckerRefusesWeakWorkflows.
func TestActorRunFindingsIsolatesSubConditions(t *testing.T) {
	t.Parallel()

	good := expectedActorStep().Run

	cases := []struct {
		name string
		run  string
	}{
		{
			"one permission comparison",
			strings.Replace(good, ` && [ "$PERMISSION" != "maintain" ]`, "", 1),
		},
		{
			"== comparison",
			strings.Replace(good, `[ "$PERMISSION" != "admin" ]`, `[ "$PERMISSION" == "admin" ]`, 1),
		},
		{
			"no exit 1",
			strings.ReplaceAll(good, "exit 1", "exit 0"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.run == good {
				t.Fatal("the row changed nothing")
			}

			if findings := actorRunFindings("release-draft", tc.run); len(findings) == 0 {
				t.Errorf("actorRunFindings accepts the run with %s", tc.name)
			}
		})
	}
}

// sourceWant is what the checker must report for one file that
// chooseWorkflowFile returns.
type sourceWant struct {
	path       string
	missingJob bool
	finding    string
}

// sourceGuardCase is one case of the guard rule for the two workflow paths.
type sourceGuardCase struct {
	name         string
	stagedExists bool
	stagedRaw    []byte
	liveRaw      []byte
	want         []sourceWant
	wantErr      error
}

// checkSourceGuardCase runs chooseWorkflowFile and then the checker on each
// file that it returns.
func checkSourceGuardCase(t *testing.T, tc sourceGuardCase) {
	t.Helper()

	sources, err := chooseWorkflowFile(tc.stagedExists, tc.stagedRaw, tc.liveRaw)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("want error %v, got %v and %d files", tc.wantErr, err, len(sources))
		}

		return
	}

	if err != nil {
		t.Fatalf("want %d files, got error: %v", len(tc.want), err)
	}

	if len(sources) != len(tc.want) {
		t.Fatalf("want %d files, got %d", len(tc.want), len(sources))
	}

	missing := "job archives-build: workflow guard: the job is missing"

	for i, src := range sources {
		want := tc.want[i]
		if src.path != want.path {
			t.Fatalf("file %d: want path %q, got %q", i, want.path, src.path)
		}

		findings := releaseWorkflowFindingsAt(src.path, src.raw)

		if got := slices.Contains(findings, missing); got != want.missingJob {
			t.Errorf("%s: want missing-job finding %t, got %t", src.path, want.missingJob, got)
		}

		if want.finding != "" && !slices.Contains(findings, want.finding) {
			t.Errorf("%s: want finding %q, got %q", src.path, want.finding, findings)
		}
	}
}

// TestReadOptionalWorkflow checks that only a missing file counts as absent.
func TestReadOptionalWorkflow(t *testing.T) {
	t.Parallel()

	missing := func(string) ([]byte, error) { return nil, fs.ErrNotExist }
	denied := func(string) ([]byte, error) { return nil, errReadDenied }
	readable := func(string) ([]byte, error) { return []byte("a"), nil }

	if _, exists, err := readOptionalWorkflow(missing, "x"); err != nil || exists {
		t.Errorf("a missing file: want absent and no error, got exists %t, error %v", exists, err)
	}

	if _, _, err := readOptionalWorkflow(denied, "x"); !errors.Is(err, errReadDenied) {
		t.Errorf("an unreadable file: want the read error, got %v", err)
	}

	if raw, exists, err := readOptionalWorkflow(readable, "x"); err != nil || !exists || string(raw) != "a" {
		t.Errorf("a readable file: got %q, %t, %v", raw, exists, err)
	}
}

// TestReleaseWorkflowSourceGuard checks the guard rule for the two workflow
// paths: while the staged copy exists, it is the file checked in full, and a
// staged copy without the archives-build job fails through its missing-job
// finding. With no staged copy, the live file is checked. Both holding the
// job fails, and no staged copy with a live file without the job fails. The
// checker runs on each file that is returned, so a weak live file gives its
// own findings.
func TestReleaseWorkflowSourceGuard(t *testing.T) {
	t.Parallel()

	_, clean := releaseWorkflowBytes(t)
	weakJob := []byte("jobs:\n  archives-build:\n    runs-on: ubuntu-26.04\n")
	withoutJob := []byte("jobs:\n  publish:\n    runs-on: ubuntu-26.04\n")
	noGuard := "job archives-build: workflow guard: the job has no guard"

	stagedNoGuard := sourceWant{path: stagedWorkflowPath, missingJob: false, finding: noGuard}
	stagedMissing := sourceWant{path: stagedWorkflowPath, missingJob: true, finding: ""}
	liveNoGuard := sourceWant{path: liveWorkflowPath, missingJob: false, finding: noGuard}

	cases := []sourceGuardCase{
		{
			name:         "only staged holds the job",
			stagedExists: true,
			stagedRaw:    weakJob,
			liveRaw:      withoutJob,
			want:         []sourceWant{stagedNoGuard},
			wantErr:      nil,
		},
		{
			name:         "only live holds it",
			stagedExists: false,
			stagedRaw:    nil,
			liveRaw:      weakJob,
			want:         []sourceWant{liveNoGuard},
			wantErr:      nil,
		},
		{
			name:         "live holds it and a staged copy without the job is left",
			stagedExists: true,
			stagedRaw:    withoutJob,
			liveRaw:      weakJob,
			want:         []sourceWant{stagedMissing, liveNoGuard},
			wantErr:      nil,
		},
		{
			name:         "neither holds the job and the staged copy exists",
			stagedExists: true,
			stagedRaw:    withoutJob,
			liveRaw:      withoutJob,
			want:         []sourceWant{stagedMissing},
			wantErr:      nil,
		},
		{
			name:         "no staged copy and the live file has no job",
			stagedExists: false,
			stagedRaw:    nil,
			liveRaw:      withoutJob,
			want:         nil,
			wantErr:      errNeitherHoldsArchivesJob,
		},
		{
			name:         "both hold it",
			stagedExists: true,
			stagedRaw:    weakJob,
			liveRaw:      weakJob,
			want:         nil,
			wantErr:      errBothHoldArchivesJob,
		},
		{
			name:         "a clean staged copy and a weak live file",
			stagedExists: true,
			stagedRaw:    clean,
			liveRaw:      weakJob,
			want:         nil,
			wantErr:      errBothHoldArchivesJob,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSourceGuardCase(t, tc)
		})
	}
}
