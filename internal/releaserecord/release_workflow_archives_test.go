package releaserecord_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// releaseWorkflowBytes returns the bytes of the patched release workflow.
// The operator rule keeps the workflow change in .github-workflows-staging/
// until a person moves it into .github/workflows/: the staged file is the
// change, so the test reads it when it exists and the live file otherwise.
// After the move the staged file is gone and the test reads the live file.
func releaseWorkflowBytes(t *testing.T) []byte {
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

	if staged, readErr := root.ReadFile(".github-workflows-staging/release.yml"); readErr == nil {
		return staged
	}

	live, readErr := root.ReadFile(".github/workflows/release.yml")
	if readErr != nil {
		t.Fatal(readErr)
	}

	return live
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
func checkWorkflowTriggers(wf workflowFile) []string {
	var out []string

	if wf.On == nil {
		return []string{"workflow guard: job archives-build: the file has no on block"}
	}

	if _, ok := wf.On["push"]; !ok {
		out = append(out, "workflow guard: job archives-build: on has no push trigger")
	}

	for _, trigger := range []string{"pull_request", "workflow_dispatch", "schedule"} {
		if _, ok := wf.On[trigger]; ok {
			out = append(out, "workflow guard: job archives-build: on includes "+trigger+", want a tag push only")
		}
	}

	return out
}

// checkWorkflowFilePermissions checks that no job inherits a permission.
func checkWorkflowFilePermissions(wf workflowFile) []string {
	out := make([]string, 0, len(wf.Permissions))

	for k, v := range wf.Permissions {
		out = append(out, "job archives-build: permissions: the file grants "+k+": "+v+", want empty")
	}

	return out
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

		if !strings.Contains(comment, "# v") {
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

	if actor >= 0 && !strings.Contains(draft.Steps[actor].Run, "collaborators") {
		out = append(out, "job release-draft: authorization at the point of use: "+
			"the actor step has no permission check")
	}

	return out
}

// releaseWorkflowFindings returns one finding for each missing guard of the
// release workflow: the workflow guard, the job order, the permissions, the
// pins, the version as data and the authorization at the point of use.
func releaseWorkflowFindings(raw []byte) []string {
	var wf workflowFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return []string{"workflow: the release workflow does not parse: " + err.Error()}
	}

	var findings []string

	findings = append(findings, checkWorkflowTriggers(wf)...)
	findings = append(findings, checkWorkflowFilePermissions(wf)...)
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

	raw := releaseWorkflowBytes(t)
	if findings := releaseWorkflowFindings(raw); len(findings) != 0 {
		t.Fatalf("the release workflow has %d findings:\n%s", len(findings), strings.Join(findings, "\n"))
	}

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
			"archives-build",
			"guard",
			func(s string) string {
				return strings.Replace(s, "  push:\n    tags:\n      - \"v*\"", "  push:\n    tags:\n      - \"v*\"\n  pull_request:", 1)
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
			"archives-build",
			"permissions",
			func(s string) string {
				return strings.Replace(s, "permissions: {}", "permissions:\n  contents: write", 1)
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

// checkOneMutation runs the checker on one hostile copy and wants a finding
// that names the job and the item.
func checkOneMutation(t *testing.T, good string, tt workflowMutation) {
	t.Helper()

	mutated := tt.mutate(good)
	if mutated == good {
		t.Fatalf("the mutation %q changed nothing", tt.name)
	}

	findings := releaseWorkflowFindings([]byte(mutated))
	if len(findings) == 0 {
		t.Fatalf("the mutation %q yields no finding", tt.name)
	}

	for _, finding := range findings {
		if strings.Contains(finding, tt.job) && strings.Contains(finding, tt.item) {
			return
		}
	}

	t.Errorf("the mutation %q yields no finding that names %q and %q: %q", tt.name, tt.job, tt.item, strings.Join(findings, "; "))
}

// TestReleaseWorkflowCheckerRefusesWeakWorkflows runs the checker on hostile
// copies of the good workflow: each copy yields at least one finding that
// names the job and the item.
func TestReleaseWorkflowCheckerRefusesWeakWorkflows(t *testing.T) {
	t.Parallel()

	good := string(releaseWorkflowBytes(t))

	for _, tt := range workflowMutations() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkOneMutation(t, good, tt)
		})
	}
}
