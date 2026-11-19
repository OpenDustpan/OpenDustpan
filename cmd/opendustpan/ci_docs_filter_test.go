package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docsPathsReadByGoTests holds one path for each docs tree that a Go test
// reads. The go job of CI runs these tests only when the changes job sets
// code=true, so each path must match a pattern of the filter that sets
// code=true. Add a path here when a Go test starts to read a new docs tree.
//
//   - docs/guides, docs/reference: TestDocsRoutesAreServed (this package), the
//     e2e page tests and TestConfigurationReference.
//   - docs/reference/code-rules.md: the code rules catalog test.
//   - docs/tfe-api.md: the TFE endpoint list test.
//   - docs/adr, docs/rfc, docs/explanation and every other .md file under
//     docs/: TestCheckRealTree in internal/adrnumbers walks docs/ for ADR
//     citations.
//   - docs/commands-checked-pages.txt: the page lists of the docs commands
//     check in e2e.
func docsPathsReadByGoTests() []string {
	return []string{
		"docs/guides/context.md",
		"docs/reference/configuration.md",
		"docs/reference/code-rules.md",
		"docs/reference/helm-values.md",
		"docs/tfe-api.md",
		"docs/adr/0001-example.md",
		"docs/rfc/0001-example.md",
		"docs/explanation/example.md",
		"docs/roadmap.md",
		"docs/commands-checked-pages.txt",
	}
}

// docsPathsReadByE2ETests holds one path for each docs tree that a test of the
// e2e package reads. The e2e job of CI runs only when the changes job sets
// e2e=true, so each path must match a pattern of the filter that sets e2e=true.
//
//   - docs/guides, docs/reference, README.md: the check of the commands in the
//     pages (docs_commands_test.go) and the page tests (*_docs_test.go).
//   - docs/commands-checked-pages.txt, docs/commands-pending-pages.txt: the
//     page lists of that check.
func docsPathsReadByE2ETests() []string {
	return []string{
		"docs/guides/install.md",
		"docs/guides/terragrunt.md",
		"docs/reference/configuration.md",
		"docs/commands-checked-pages.txt",
		"docs/commands-pending-pages.txt",
		"README.md",
	}
}

const (
	classifyCaseStart = `case "$f" in`
	classifyCaseEnd   = "esac"
	classifyCodeTrue  = "code=true"
	classifyE2ETrue   = "e2e=true"
)

var classifyArm = regexp.MustCompile(`^\s*([^\s)][^)]*)\)\s*(.*?)\s*;;\s*$`)

// classifyRule is one arm of a case statement of the classify step.
type classifyRule struct {
	pattern string
	body    string
}

// rulesSetting returns the arms of the case statement in the classify step of
// the workflow that sets the output, for example code=true, in the order of
// the file.
func rulesSetting(t *testing.T, workflow, output string) []classifyRule {
	t.Helper()

	var (
		rules   []classifyRule
		current []classifyRule
		inCase  bool
		found   bool
	)

	for line := range strings.SplitSeq(workflow, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "#"):
		case strings.HasPrefix(trimmed, classifyCaseStart):
			inCase, current = true, nil
		case inCase && trimmed == classifyCaseEnd:
			inCase = false

			if hasArmSetting(current, output) {
				rules, found = current, true
			}
		case inCase:
			if m := classifyArm.FindStringSubmatch(line); m != nil {
				current = append(current, classifyRule{pattern: m[1], body: m[2]})
			}
		default:
		}
	}

	if !found {
		t.Fatalf("no case statement sets %s in the workflow: follow the classify step",
			output)
	}

	return rules
}

func hasArmSetting(rules []classifyRule, output string) bool {
	for _, r := range rules {
		if strings.Contains(r.body, output) {
			return true
		}
	}

	return false
}

// globToRegexp turns a shell case pattern with * and ? into a regexp. As in a
// shell case statement, * also matches a slash.
func globToRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder

	b.WriteString("^")

	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}

	b.WriteString("$")

	return regexp.MustCompile(b.String())
}

// sets reports whether the first arm that matches path sets the output. A
// shell case statement runs the first arm that matches and no other.
func sets(rules []classifyRule, path, output string) bool {
	for _, r := range rules {
		for alt := range strings.SplitSeq(r.pattern, "|") {
			if globToRegexp(strings.TrimSpace(alt)).MatchString(path) {
				return strings.Contains(r.body, output)
			}
		}
	}

	return false
}

// TestCIFilterRunsGoTestsForDocsTheyRead fails when a docs path that a Go test
// reads is not code=true in the classify step of the CI workflow. A change of
// such a path must run the go job: a docs-only change that the filter skips can
// break main, and the merge queue is the only gate.
func TestCIFilterRunsGoTestsForDocsTheyRead(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read the CI workflow: %v", err)
	}

	rules := rulesSetting(t, string(raw), classifyCodeTrue)

	for _, path := range docsPathsReadByGoTests() {
		if !sets(rules, path, classifyCodeTrue) {
			t.Errorf("ci.yml does not set code=true for %s, which a Go test reads: "+
				"a docs-only change of it skips the go job", path)
		}
	}
}

// TestCIFilterRunsE2EForDocsTheyRead fails when a docs path that a test of the
// e2e package reads is not e2e=true in the classify step of the CI workflow.
// A change of such a path must run the e2e job for the same reason as above.
func TestCIFilterRunsE2EForDocsTheyRead(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read the CI workflow: %v", err)
	}

	rules := rulesSetting(t, string(raw), classifyE2ETrue)

	for _, path := range docsPathsReadByE2ETests() {
		if !sets(rules, path, classifyE2ETrue) {
			t.Errorf("ci.yml does not set e2e=true for %s, which an e2e test reads: "+
				"a docs-only change of it skips the e2e job", path)
		}
	}
}

// TestCIFilterRuleParser proves that the parser follows the first matching arm
// of a case statement, so that the test above cannot pass by accident.
func TestCIFilterRuleParser(t *testing.T) {
	t.Parallel()

	workflow := strings.Join([]string{
		`            case "$f" in`,
		`              .github/*|Makefile) run_all "x" ;;`,
		`            esac`,
		`            case "$f" in`,
		`              docs/guides/*|docs/tfe-api.md) code=true ;;`,
		`              docs/*|openspec/*) ;;`,
		`              */*) code=true ;;`,
		`              *.md) ;;`,
		`              *) code=true ;;`,
		`            esac`,
	}, "\n")

	rules := rulesSetting(t, workflow, classifyCodeTrue)

	for _, tc := range []struct {
		path string
		want bool
	}{
		{"docs/guides/a.md", true},
		{"docs/tfe-api.md", true},
		{"docs/adr/0001-x.md", false},
		{"openspec/changes/x/proposal.md", false},
		{"pkg/a/a.go", true},
		{"README.md", false},
		{"Dockerfile", true},
	} {
		if got := sets(rules, tc.path, classifyCodeTrue); got != tc.want {
			t.Errorf("sets(%q, code=true) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestCIFilterRuleParserE2E proves that the parser reads the arm of the e2e
// output, skips a comment line inside the case statement and follows the
// first matching arm.
func TestCIFilterRuleParserE2E(t *testing.T) {
	t.Parallel()

	workflow := strings.Join([]string{
		`            case "$f" in`,
		`              docs/*) code=true ;;`,
		`            esac`,
		`            case "$f" in`,
		`              e2e/*|cmd/opendustpan/*) e2e=true ;;`,
		`              # A comment line with a pattern (like this one) ;;`,
		`              docs/guides/*|README.md) e2e=true ;;`,
		`            esac`,
	}, "\n")

	rules := rulesSetting(t, workflow, classifyE2ETrue)

	for _, tc := range []struct {
		path string
		want bool
	}{
		{"e2e/a_test.go", true},
		{"docs/guides/a.md", true},
		{"README.md", true},
		{"docs/adr/0001-x.md", false},
		{"pkg/a/a.go", false},
	} {
		if got := sets(rules, tc.path, classifyE2ETrue); got != tc.want {
			t.Errorf("sets(%q, e2e=true) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
