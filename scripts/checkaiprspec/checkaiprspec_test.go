package checkaiprspec_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes data to dir/rel, making parent directories.
func writeFile(t *testing.T, dir, rel, data string) {
	t.Helper()

	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readScript returns the text of scripts/check-ai-pr-spec.sh.
func readScript(t *testing.T) string {
	t.Helper()

	root, err := os.OpenRoot("../")
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()

	data, err := root.ReadFile("check-ai-pr-spec.sh")
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func readyProposal() string {
	return "# Title\n\n## Open questions\n\nnone\n"
}

func notReadyProposal() string {
	return "# Title\n\n## Open questions\n\nShould the check read archived changes?\n"
}

// askedProposal is a spec-ready proposal whose "none" is followed by
// answered questions, like the proposal of this change.
func askedProposal() string {
	return "# Title\n\n## Open questions\n\nnone\n\nQuestions considered and answered:\n\n- Is it checked? Yes.\n"
}

// runCheck runs scripts/check-ai-pr-spec.sh on a temporary repo root that
// holds the given change proposals, body and commits. It returns the
// combined output and the error of the run.
func runCheck(
	t *testing.T,
	proposals, archived map[string]string,
	body, commits string,
) (string, error) {
	t.Helper()

	dir := t.TempDir()
	for id, proposal := range proposals {
		writeFile(t, dir, filepath.Join("openspec", "changes", id, "proposal.md"), proposal)
	}
	for id, proposal := range archived {
		writeFile(t, dir, filepath.Join("openspec", "changes", "archive", "2026-01-01-"+id, "proposal.md"), proposal)
	}
	writeFile(t, dir, "body.md", body)
	writeFile(t, dir, "commits.txt", commits)

	// The script goes in on standard input, with the files in the
	// environment, so that the command line holds no variable.
	cmd := exec.CommandContext(t.Context(), "bash", "-s")
	cmd.Stdin = strings.NewReader(readScript(t))
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"AI_PR_BODY_FILE=" + filepath.Join(dir, "body.md"),
		"AI_PR_COMMITS_FILE=" + filepath.Join(dir, "commits.txt"),
		"AI_PR_ROOT=" + dir,
	}

	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestCheckAIPRSpec(t *testing.T) {
	t.Parallel()

	ready := map[string]string{"demo-ready": readyProposal()}
	notReady := map[string]string{"demo-open": notReadyProposal()}

	tests := []struct {
		name      string
		proposals map[string]string
		archived  map[string]string
		body      string
		commits   string
		wantPass  bool
		want      string
	}{
		{
			name:      "agent pull request with a spec-ready link",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/demo-ready\n",
			commits:   "someone@example.com\n",
			wantPass:  true,
			want:      "demo-ready is spec-ready",
		},
		{
			name:      "agent pull request with no link fails and prints the fix line",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nNo change link here.\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "add a line",
		},
		{
			name:      "link to a change whose open questions are not none fails and names the change",
			proposals: notReady,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/demo-open\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "demo-open is not spec-ready: open questions: Should the check read archived changes?",
		},
		{
			name:      "link only inside a fenced code block fails",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\n```\nChange: openspec/changes/demo-ready\n```\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "add a line",
		},
		{
			name:      "archived change passes",
			proposals: nil,
			archived:  map[string]string{"demo-old": readyProposal()},
			body:      "Tool: test harness\n\nChange: openspec/changes/demo-old\n",
			commits:   "someone@example.com\n",
			wantPass:  true,
			want:      "demo-old is spec-ready",
		},
		{
			name:      "person pull request with no link passes",
			proposals: ready,
			archived:  nil,
			body:      "A plain change with no AI text.\n",
			commits:   "someone@example.com\n",
			wantPass:  true,
			want:      "person's pull request",
		},
		{
			name:      "commit signal alone makes the pull request AI-authored",
			proposals: ready,
			archived:  nil,
			body:      "A plain change with no AI text.\n",
			commits:   "noreply@anthropic.com\n",
			wantPass:  false,
			want:      "add a line",
		},
		{
			name:      "commit signal alone with a spec-ready link passes",
			proposals: ready,
			archived:  nil,
			body:      "Change: openspec/changes/demo-ready\n",
			commits:   "noreply@anthropic.com\n",
			wantPass:  true,
			want:      "demo-ready is spec-ready",
		},
		{
			name:      "none followed by answered questions is spec-ready",
			proposals: map[string]string{"demo-asked": askedProposal()},
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/demo-asked\n",
			commits:   "someone@example.com\n",
			wantPass:  true,
			want:      "demo-asked is spec-ready",
		},
		{
			name:      "archive match without the hyphen boundary does not count",
			proposals: nil,
			archived:  map[string]string{"specx": readyProposal()},
			body:      "Tool: test harness\n\nChange: openspec/changes/spec\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "no change openspec/changes/spec",
		},
		{
			name:      "archive id that is a suffix of the link id does not count",
			proposals: nil,
			archived:  map[string]string{"ai-pr-spec": readyProposal()},
			body:      "Tool: test harness\n\nChange: openspec/changes/spec\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "no change openspec/changes/spec",
		},
		{
			name:      "valid link plus a refused id passes",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/demo-ready\n\nSee openspec/changes/../demo-ready for context.\n",
			commits:   "someone@example.com\n",
			wantPass:  true,
			want:      "demo-ready is spec-ready",
		},
		{
			name:      "valid link plus a missing change passes",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/demo-missing\n\nChange: openspec/changes/demo-ready\n",
			commits:   "someone@example.com\n",
			wantPass:  true,
			want:      "demo-ready is spec-ready",
		},
		{
			name:      "link only inside a tilde fenced block fails",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\n~~~\nChange: openspec/changes/demo-ready\n~~~\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "add a line",
		},
		{
			name:      "AI tool signal alone makes the pull request AI-authored",
			proposals: notReady,
			archived:  nil,
			body:      "AI tool: test harness\n\nChange: openspec/changes/demo-open\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "demo-open is not spec-ready: open questions: Should the check read archived changes?",
		},
		{
			name:      "id with a slash is refused",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/../demo-ready\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "refused id",
		},
		{
			name:      "id with capital letters is refused",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/Demo-Ready\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "refused id",
		},
		{
			name:      "id of only dots is refused",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/..\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "refused id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := runCheck(t, tt.proposals, tt.archived, tt.body, tt.commits)
			if tt.wantPass && err != nil {
				t.Fatalf("the check failed, want a pass:\n%s", out)
			}
			if !tt.wantPass && err == nil {
				t.Fatalf("the check passed, want a failure:\n%s", out)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("the check said:\n%s\nwant %q", out, tt.want)
			}
		})
	}
}

func TestCheckAIPRSpecLongQuestionsLineIsBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("q", 500)
	proposal := "# Title\n\n## Open questions\n\n" + long + "\n"
	out, err := runCheck(t, map[string]string{"demo-long": proposal}, nil,
		"Tool: test harness\n\nChange: openspec/changes/demo-long\n",
		"someone@example.com\n")
	if err == nil {
		t.Fatalf("the check passed, want a failure:\n%s", out)
	}
	quoted := out
	if _, after, ok := strings.Cut(out, "open questions: "); ok {
		quoted = strings.TrimRight(after, "\n")
	}
	if len(quoted) > 200 {
		t.Errorf("the quoted open-questions line holds %d characters, want at most 200:\n%s", len(quoted), out)
	}
	if !strings.Contains(out, long[:200]) {
		t.Errorf("the check output does not hold the first 200 characters:\n%s", out)
	}
}

func TestCheckAIPRSpecNoCRInOutput(t *testing.T) {
	t.Parallel()

	crlf := "# Title\r\n\r\n## Open questions\r\n\r\nSomething open\r\n"
	out, err := runCheck(t, map[string]string{"demo-crlf": crlf}, nil,
		"Tool: test harness\n\nChange: openspec/changes/demo-crlf\n",
		"someone@example.com\n")
	if err == nil {
		t.Fatalf("the check passed, want a failure:\n%s", out)
	}
	if strings.ContainsRune(out, '\r') {
		t.Errorf("the check output holds a carriage return:\n%q", out)
	}
}

func TestCheckAIPRSpecSymlinkLeavesChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "proposal.md", notReadyProposal())
	changes := filepath.Join(dir, "openspec", "changes")
	if err := os.MkdirAll(changes, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(changes, "demo-link")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "body.md", "Tool: test harness\n\nChange: openspec/changes/demo-link\n")
	writeFile(t, dir, "commits.txt", "someone@example.com\n")

	cmd := exec.CommandContext(t.Context(), "bash", "-s")
	cmd.Stdin = strings.NewReader(readScript(t))
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"AI_PR_BODY_FILE=" + filepath.Join(dir, "body.md"),
		"AI_PR_COMMITS_FILE=" + filepath.Join(dir, "commits.txt"),
		"AI_PR_ROOT=" + dir,
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the check passed, want a failure:\n%s", out)
	}
	if !strings.Contains(string(out), "resolved path leaves") {
		t.Errorf("the check said:\n%s\nwant %q", out, "resolved path leaves")
	}
}
