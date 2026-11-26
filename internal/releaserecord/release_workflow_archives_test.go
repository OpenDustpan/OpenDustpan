package releaserecord_test

import (
	"errors"
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
// the checker reads in full. The operator rule keeps the workflow change in
// .github-workflows-staging/ until a person moves it into .github/workflows/.
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
	Steps       []workflowStep    `yaml:"steps"`
}

// workflowFile is the part of the release workflow that the checker reads.
type workflowFile struct {
	On          map[string]any         `yaml:"on"`
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

// releaseWorkflowJobNames lists the four jobs of the patched workflow.
func releaseWorkflowJobNames() []string {
	return []string{"archives-build", "archives-verify", "publish", "release-draft"}
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

// checkWorkflowTriggers checks that the file runs on a tag push only.
func checkWorkflowTriggers(path string, wf workflowFile) []string {
	var out []string

	if wf.On == nil {
		return []string{"workflow " + path + ": workflow guard: the file has no on block"}
	}

	if _, ok := wf.On["push"]; !ok {
		out = append(out, "workflow "+path+": workflow guard: on has no push trigger")
	}

	for _, trigger := range []string{"pull_request", "workflow_dispatch", "schedule"} {
		if _, ok := wf.On[trigger]; ok {
			out = append(out, "workflow "+path+": workflow guard: on includes "+trigger+", want a tag push only")
		}
	}

	return out
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

// checkWorkflowJobPresence checks that the four jobs exist.
func checkWorkflowJobPresence(wf workflowFile) []string {
	var out []string

	for _, name := range releaseWorkflowJobNames() {
		if _, ok := wf.Jobs[name]; !ok {
			out = append(out, "job "+name+": workflow guard: the job is missing")
		}
	}

	return out
}

// checkWorkflowGuard checks the guard of one job.
func checkWorkflowGuard(name string, job workflowJob, publishGuard string) []string {
	var out []string

	if strings.TrimSpace(job.If) == "" {
		return []string{"job " + name + ": workflow guard: the job has no guard"}
	}

	for _, clause := range []string{
		"github.event_name == 'push'",
		"startsWith(github.ref, 'refs/tags/v')",
		"github.repository == 'OpenDustpan/OpenDustpan'",
	} {
		if !strings.Contains(job.If, clause) {
			out = append(out, "job "+name+": workflow guard: the guard lacks "+clause)
		}
	}

	if name != "publish" && publishGuard != "" && job.If != publishGuard {
		out = append(out, "job "+name+": workflow guard: the guard differs from the publish job")
	}

	return out
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
		return nil
	}

	return nil
}

// checkWorkflowJobPermissions checks the permissions block of one job.
func checkWorkflowJobPermissions(name string, job workflowJob) []string {
	if len(job.Permissions) == 0 {
		return []string{"job " + name + ": permissions: the job has no permissions block"}
	}

	switch name {
	case "archives-build", "archives-verify":
		var out []string

		if job.Permissions["contents"] != "read" {
			out = append(out, "job "+name+": permissions: the job wants contents read")
		}

		if _, ok := job.Permissions["packages"]; ok {
			out = append(out, "job "+name+": permissions: the job wants no packages permission")
		}

		return out
	case "publish":
		var out []string

		if job.Permissions["contents"] != "read" {
			out = append(out, "job publish: permissions: the job wants contents read")
		}

		if job.Permissions["packages"] != "write" {
			out = append(out, "job publish: permissions: the job wants packages write")
		}

		return out
	case "release-draft":
		var out []string

		if job.Permissions["contents"] != "write" {
			out = append(out, "job release-draft: permissions: the job wants contents write")
		}

		if _, ok := job.Permissions["packages"]; ok {
			out = append(out, "job release-draft: permissions: the draft job wants no packages write")
		}

		return out
	default:
		return nil
	}
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

// stepReadsSecrets answers whether a step reads a secret.
func stepReadsSecrets(step workflowStep) bool {
	if strings.Contains(step.Run, "secrets.") {
		return true
	}

	for _, v := range step.Env {
		if s, ok := v.(string); ok && strings.Contains(s, "secrets.") {
			return true
		}
	}

	for _, v := range step.With {
		if s, ok := v.(string); ok && strings.Contains(s, "secrets.") {
			return true
		}
	}

	return false
}

// checkWorkflowRuns checks that no run block holds a template value and that
// the archive jobs read no secret.
func checkWorkflowRuns(wf workflowFile) []string {
	var out []string

	for _, name := range releaseWorkflowJobNames() {
		job := wf.Jobs[name]

		for _, step := range job.Steps {
			if strings.Contains(step.Run, "${{") {
				out = append(out, "job "+name+": version as data: a run block holds a template value")
			}

			if (name == "archives-build" || name == "archives-verify") && stepReadsSecrets(step) {
				out = append(out, "job "+name+": permissions: the archive job reads secrets.")
			}
		}
	}

	return out
}

// checkWorkflowCheckouts checks that each checkout of a new job keeps no
// credential. The publish checkout predates the change and keeps its form.
func checkWorkflowCheckouts(wf workflowFile) []string {
	var out []string

	for _, name := range []string{"archives-build", "archives-verify", "release-draft"} {
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
)

// checkWorkflowActorComparison checks that the actor step of publish and of
// release-draft refuses each permission except admin and maintain. A missing
// step of release-draft is reported by checkWorkflowAuthorization.
func checkWorkflowActorComparison(wf workflowFile) []string {
	var out []string

	for _, name := range []string{"publish", "release-draft"} {
		found := false

		for _, step := range wf.Jobs[name].Steps {
			if step.Name == actorStepName {
				found = true
				out = append(out, actorRunFindings(name, step.Run)...)
			}
		}

		if !found && name == "publish" {
			out = append(out, "job publish: authorization at the point of use: the actor step is missing")
		}
	}

	return out
}

// actorRunFindings checks the run text of one actor step.
func actorRunFindings(name, run string) []string {
	var out []string

	if !strings.Contains(run, "collaborators") {
		out = append(out, "job "+name+": authorization at the point of use: the actor step has no permission check")
	}

	if !strings.Contains(run, actorCompareText) ||
		strings.Count(run, `"$PERMISSION" `) != 2 ||
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

// checkWorkflowTagFormat checks that archives-build, publish and
// release-draft each refuse a tag that is not vX.Y.Z.
func checkWorkflowTagFormat(wf workflowFile) []string {
	var out []string

	for _, name := range []string{"archives-build", "publish", "release-draft"} {
		re, ok := releaseTagRegexp(wf.Jobs[name])
		if !ok {
			out = append(out, "job "+name+": tag format: the final version tag step is missing")

			continue
		}

		if !re.MatchString("v1.2.3") {
			out = append(out, "job "+name+": tag format: the step refuses v1.2.3")
		}

		for _, tag := range hostileReleaseTags() {
			if re.MatchString(tag) {
				out = append(out, "job "+name+": tag format: the step accepts the tag "+tag)
			}
		}
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
func releaseWorkflowFindings(path string, raw []byte) []string {
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
	findings = append(findings, checkWorkflowJobPresence(wf)...)

	publishGuard := wf.Jobs["publish"].If

	for _, name := range releaseWorkflowJobNames() {
		job, ok := wf.Jobs[name]
		if !ok {
			continue
		}

		findings = append(findings, checkWorkflowGuard(name, job, publishGuard)...)
		findings = append(findings, checkWorkflowNeeds(name, job)...)
		findings = append(findings, checkWorkflowJobPermissions(name, job)...)
	}

	findings = append(findings, checkWorkflowPins(string(raw))...)
	findings = append(findings, checkWorkflowRuns(wf)...)
	findings = append(findings, checkWorkflowCheckouts(wf)...)
	findings = append(findings, checkWorkflowAuthorization(wf)...)
	findings = append(findings, checkWorkflowActorComparison(wf)...)
	findings = append(findings, checkWorkflowTagFormat(wf)...)
	findings = append(findings, checkWorkflowChecksum(wf)...)

	return findings
}

// checkArchiveJobReadsNoSecret fails the test when an archive job reads a secret.
func checkArchiveJobReadsNoSecret(t *testing.T, wf workflowFile, name string) {
	t.Helper()

	for _, step := range wf.Jobs[name].Steps {
		if stepReadsSecrets(step) {
			t.Errorf("job %s reads secrets in step %q", name, step.Name)
		}
	}
}

// checkSameGuardAsPublish fails the test when a job guard differs from publish.
func checkSameGuardAsPublish(t *testing.T, wf workflowFile, name string) {
	t.Helper()

	if wf.Jobs[name].If != wf.Jobs["publish"].If {
		t.Errorf("job %s uses a guard text that differs from publish", name)
	}
}

// checkActorBeforeDownload fails the test when the draft actor step is
// missing or runs after the download step.
func checkActorBeforeDownload(t *testing.T, wf workflowFile) {
	t.Helper()

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
		t.Error("release-draft lacks the actor step")
	}

	if download < 0 {
		t.Error("release-draft lacks the download step")
	}

	if actor >= 0 && download >= 0 && actor > download {
		t.Error("release-draft runs the actor step after the download step")
	}
}

// TestReleaseWorkflowArchiveJobs checks the structure of the staged release
// workflow: no finding, no secret read in the archive jobs, the same guard
// text in each new job as in publish, and the actor step before the download.
func TestReleaseWorkflowArchiveJobs(t *testing.T) {
	t.Parallel()

	for _, src := range releaseWorkflowSources(t) {
		if findings := releaseWorkflowFindings(src.path, src.raw); len(findings) != 0 {
			t.Fatalf("the release workflow has %d findings:\n%s", len(findings), strings.Join(findings, "\n"))
		}
	}

	_, raw := releaseWorkflowBytes(t)

	var wf workflowFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}

	checkArchiveJobReadsNoSecret(t, wf, "archives-build")
	checkArchiveJobReadsNoSecret(t, wf, "archives-verify")
	checkSameGuardAsPublish(t, wf, "archives-build")
	checkSameGuardAsPublish(t, wf, "archives-verify")
	checkSameGuardAsPublish(t, wf, "release-draft")
	checkActorBeforeDownload(t, wf)
}

// workflowMutation is one hostile copy of the good workflow file.
type workflowMutation struct {
	name   string
	job    string
	item   string
	mutate func(string) string
}

// workflowMutations returns the hostile copies: each one must yield at least
// one finding that names the job and the item.
func workflowMutations() []workflowMutation {
	guard := "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && github.repository == 'OpenDustpan/OpenDustpan'"

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
		{
			"a pull_request trigger",
			"workflow",
			"guard",
			func(s string) string {
				return strings.Replace(
					s,
					"  push:\n    tags:\n      - \"v*\"",
					"  push:\n    tags:\n      - \"v*\"\n  pull_request:",
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
			"a checkout with no persist-credentials false",
			"archives-build",
			"persist-credentials",
			func(s string) string {
				return strings.Replace(s, "          persist-credentials: false", "", 1)
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
// the check wants the full "workflow <path>:" prefix with the item.
func checkOneMutation(t *testing.T, path, good string, tt workflowMutation) {
	t.Helper()

	mutated := tt.mutate(good)
	if mutated == good {
		t.Fatalf("the mutation %q changed nothing", tt.name)
	}

	findings := releaseWorkflowFindings(path, []byte(mutated))
	if len(findings) == 0 {
		t.Fatalf("the mutation %q yields no finding", tt.name)
	}

	for _, finding := range findings {
		if tt.job == "workflow" {
			if strings.Contains(finding, "workflow "+path+":") && strings.Contains(finding, tt.item) {
				return
			}

			continue
		}

		if strings.Contains(finding, tt.job) && strings.Contains(finding, tt.item) {
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

		findings := releaseWorkflowFindings(src.path, src.raw)

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
