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

// runCheckInRoot runs scripts/check-ai-pr-spec.sh with dir as the repo
// root. The caller writes body.md and commits.txt in dir first. This
// variant lets a test build a repo root that a proposal map cannot hold,
// such as one with a symlink.
func runCheckInRoot(t *testing.T, dir string) (string, error) {
	t.Helper()

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

// aiCase is one row of the TestCheckAIPRSpec table.
type aiCase struct {
	name      string
	proposals map[string]string
	archived  map[string]string
	body      string
	commits   string
	wantPass  bool
	want      string
}

// runCase runs the script on one row of the table and checks the result.
func runCase(t *testing.T, tt aiCase) {
	t.Helper()

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
}

func TestCheckAIPRSpec(t *testing.T) {
	t.Parallel()

	ready := map[string]string{"demo-ready": readyProposal()}
	notReady := map[string]string{"demo-open": notReadyProposal()}

	tests := []aiCase{
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
			name:      "tilde line inside a backtick fence does not close it",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\n```\n~~~\nChange: openspec/changes/demo-ready\n```\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "add a line",
		},
		{
			name:      "short backtick line inside a long backtick fence does not close it",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\n````\n```\nChange: openspec/changes/demo-ready\n````\n",
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
			body:      "Tool: test harness\n\nChange: openspec/changes/a/b\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "refused id",
		},
		{
			name:      "id with a double dot is refused",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/demo-ready..x\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "refused id",
		},
		{
			name:      "id with a dot-dot and a slash is refused",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/../x\n",
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
			name:      "id with a space is refused",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/bad id\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "refused id",
		},
		{
			name:      "id of 81 bytes is refused at the length cap",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/" + strings.Repeat("a", 81) + "\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "an id holds at most 80 bytes",
		},
		{
			// The want names the log form of the id: the check removed the
			// CR and the ESC. TestCheckAIPRSpecRefusedIDHoldsNoControlByte
			// proves that the whole refusal line stays one plain line.
			name:      "refused id with CR and an ANSI escape writes one plain line",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/de\rmo\x1b[2jdemo\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      `refused id "demo[2jdemo"`,
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
		{
			name:      "id with a trailing slash is refused",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nChange: openspec/changes/demo-ready/\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "refused id",
		},
		{
			name:      "sentence punctuation after the id is refused",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\nSee openspec/changes/demo-ready.\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "refused id",
		},
		{
			name:      "link inside a single-line HTML comment fails",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\n<!-- Change: openspec/changes/demo-ready -->\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "add a line",
		},
		{
			name:      "link inside a multi-line HTML comment fails",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\n<!--\nChange: openspec/changes/demo-ready\n-->\n",
			commits:   "someone@example.com\n",
			wantPass:  false,
			want:      "add a line",
		},
		{
			name:      "link outside a closed HTML comment passes",
			proposals: ready,
			archived:  nil,
			body:      "Tool: test harness\n\n<!-- an old link: openspec/changes/demo-missing -->\nChange: openspec/changes/demo-ready\n",
			commits:   "someone@example.com\n",
			wantPass:  true,
			want:      "demo-ready is spec-ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runCase(t, tt)
		})
	}
}

// TestCheckAIPRSpecUnreadableFileFailsClosed proves that the check fails
// closed when it cannot read the body or the commit list. grep reports 2 on a
// read error, and the check must not treat that as "no AI signal" and pass the
// pull request as a person's pull request. The commit list holds no agent
// email, so a fail-open run on the body case exits 0 as a person's pull
// request.
func TestCheckAIPRSpecUnreadableFileFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
	}{
		{name: "unreadable body file", file: "body.md"},
		{name: "unreadable commits file", file: "commits.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFile(t, dir, filepath.Join("openspec", "changes", "demo-ready", "proposal.md"), readyProposal())
			writeFile(t, dir, "body.md", "Tool: test harness\n\nChange: openspec/changes/demo-ready\n")
			writeFile(t, dir, "commits.txt", "someone@example.com\n")

			unreadable := filepath.Join(dir, tt.file)
			if err := os.Chmod(unreadable, 0); err != nil {
				t.Fatal(err)
			}
			// The test process owns the file, so mode 000 denies its read.
			// A process with CAP_DAC_OVERRIDE (root) reads it anyway, so this
			// case needs a user that the mode denies.
			if os.Geteuid() == 0 {
				t.Skipf(
					"this user reads a mode-000 file; the %s case needs a user that the mode denies",
					tt.file,
				)
			}

			out, err := runCheckInRoot(t, dir)
			if err == nil {
				t.Fatalf("the check passed on %s, want a failure:\n%s", tt.file, out)
			}
			if !strings.Contains(out, "cannot read") {
				t.Errorf("the check said:\n%s\nwant a cannot-read message", out)
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

	out, err := runCheckInRoot(t, dir)
	if err == nil {
		t.Fatalf("the check passed, want a failure:\n%s", out)
	}
	if !strings.Contains(out, "resolved path leaves") {
		t.Errorf("the check said:\n%s\nwant %q", out, "resolved path leaves")
	}
}

// TestCheckAIPRSpecSymlinkProposalFileLeavesChanges puts a symlink named
// proposal.md inside a change directory in openspec/changes, and points it
// at a spec-ready file outside the tree. The directory of the link stays
// inside openspec/changes, so the file itself must be refused.
func TestCheckAIPRSpecSymlinkProposalFileLeavesChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "elsewhere.md", readyProposal())

	changeDir := filepath.Join(dir, "openspec", "changes", "demo-file-link")
	if err := os.MkdirAll(changeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "elsewhere.md"), filepath.Join(changeDir, "proposal.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "body.md", "Tool: test harness\n\nChange: openspec/changes/demo-file-link\n")
	writeFile(t, dir, "commits.txt", "someone@example.com\n")

	out, err := runCheckInRoot(t, dir)
	if err == nil {
		t.Fatalf("the check passed, want a failure:\n%s", out)
	}
	if !strings.Contains(out, "resolved path leaves") {
		t.Errorf("the check said:\n%s\nwant %q", out, "resolved path leaves")
	}
}

// TestCheckAIPRSpecSymlinkArchivedProposalFileLeavesChanges puts a symlink
// named proposal.md inside an archived change directory. The find lists the
// link, and the file itself must be refused.
func TestCheckAIPRSpecSymlinkArchivedProposalFileLeavesChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "elsewhere.md", readyProposal())

	archiveDir := filepath.Join(dir, "openspec", "changes", "archive", "2026-01-01-demo-archive-link")
	if err := os.MkdirAll(archiveDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "elsewhere.md"), filepath.Join(archiveDir, "proposal.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "body.md", "Tool: test harness\n\nChange: openspec/changes/demo-archive-link\n")
	writeFile(t, dir, "commits.txt", "someone@example.com\n")

	out, err := runCheckInRoot(t, dir)
	if err == nil {
		t.Fatalf("the check passed, want a failure:\n%s", out)
	}
	if !strings.Contains(out, "resolved path leaves") {
		t.Errorf("the check said:\n%s\nwant %q", out, "resolved path leaves")
	}
}

// TestCheckAIPRSpecQuestionsLineHoldsNoControlByte writes an escape byte
// into the "Open questions" section. The failure line must not carry the
// byte into the log.
func TestCheckAIPRSpecQuestionsLineHoldsNoControlByte(t *testing.T) {
	t.Parallel()

	proposal := "# Title\n\n## Open questions\n\nopen \x1b[31mred\n"
	out, err := runCheck(t, map[string]string{"demo-esc": proposal}, nil,
		"Tool: test harness\n\nChange: openspec/changes/demo-esc\n",
		"someone@example.com\n")
	if err == nil {
		t.Fatalf("the check passed, want a failure:\n%s", out)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("the check output holds an escape byte:\n%q", out)
	}
	if !strings.Contains(out, "open ?[31mred") {
		t.Errorf("the check output does not encode the line:\n%q", out)
	}
}

// TestCheckAIPRSpecRefusedIDHoldsNoControlByte sends an id that holds a
// carriage return and an escape byte. The allowlist refuses it, and the
// refusal line must be one plain line: the log form removes CR, LF and ESC,
// so a hostile id cannot add a line or a terminal sequence to the log.
func TestCheckAIPRSpecRefusedIDHoldsNoControlByte(t *testing.T) {
	t.Parallel()

	id := "de\rmo-\x1bready"
	out, err := runCheck(t, map[string]string{"demo-ready": readyProposal()}, nil,
		"Tool: test harness\n\nChange: openspec/changes/"+id+"\n",
		"someone@example.com\n")
	if err == nil {
		t.Fatalf("the check passed, want a failure:\n%s", out)
	}
	if strings.ContainsAny(out, "\r\x1b") {
		t.Errorf("the check output holds a control byte:\n%q", out)
	}
	trimmed := strings.TrimRight(out, "\n")
	if strings.ContainsRune(trimmed, '\n') {
		t.Errorf("the check output holds more than one plain line:\n%q", out)
	}
	if !strings.Contains(out, `refused id "demo-ready"`) {
		t.Errorf("the check output does not refuse the id in its log form:\n%q", out)
	}
}

// TestCheckAIPRSpecRefusedIDIsBounded sends a 500-character id. The length
// cap refuses it, and the refusal line must hold at most 80 bytes of it.
func TestCheckAIPRSpecRefusedIDIsBounded(t *testing.T) {
	t.Parallel()

	id := strings.Repeat("A", 500)
	out, err := runCheck(t, map[string]string{"demo-ready": readyProposal()}, nil,
		"Tool: test harness\n\nChange: openspec/changes/"+id+"\n",
		"someone@example.com\n")
	if err == nil {
		t.Fatalf("the check passed, want a failure:\n%s", out)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("the check output holds an escape byte:\n%q", out)
	}
	_, after, ok := strings.Cut(out, `refused id "`)
	if !ok {
		t.Fatalf("the check output does not refuse the id:\n%q", out)
	}
	quoted, _, ok := strings.Cut(after, `"`)
	if !ok {
		t.Fatalf("the check output has no closing quote:\n%q", out)
	}
	if len(quoted) > 80 {
		t.Errorf("the refused id holds %d bytes, want at most 80:\n%q", len(quoted), out)
	}
}

// TestMakeAIPRSpecCheckQuoteInValue proves that the Makefile target hands the
// two paths to the script as environment variables, so a quote in a path
// cannot escape into the command line. The body and the commit file live in a
// directory whose name holds a single quote. A recipe that put the value in
// single quotes would let that quote escape and break the command.
func TestMakeAIPRSpecCheckQuoteInValue(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("make"); err != nil {
		t.Skipf("make is not on PATH: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "body's dir")
	writeFile(t, dir, filepath.Join("openspec", "changes", "demo-ready", "proposal.md"), readyProposal())
	writeFile(t, dir, "body.md", "Tool: test harness\n\nChange: openspec/changes/demo-ready\n")
	writeFile(t, dir, "commits.txt", "someone@example.com\n")

	cmd := exec.CommandContext(
		t.Context(),
		"make",
		"--no-print-directory",
		"-C", "../..",
		"ai-pr-spec-check",
	)
	// The two paths and the change tree go through the environment, so a
	// single quote in a path cannot escape into the command line. The rest of
	// the environment (PATH) comes from the test process.
	cmd.Env = append(os.Environ(),
		"PR_BODY_FILE="+filepath.Join(dir, "body.md"),
		"PR_COMMITS_FILE="+filepath.Join(dir, "commits.txt"),
		"AI_PR_ROOT="+dir,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the check failed, want a pass: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "demo-ready is spec-ready") {
		t.Errorf("the check said:\n%s\nwant a spec-ready pass", out)
	}
}

// TestMakeAIPRSpecCheckUsageWithoutVariables proves that the target refuses
// to run when one of the two variables is unset, and prints its usage line.
// The environment holds neither variable.
func TestMakeAIPRSpecCheckUsageWithoutVariables(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("make"); err != nil {
		t.Skipf("make is not on PATH: %v", err)
	}

	cmd := exec.CommandContext(
		t.Context(),
		"make",
		"--no-print-directory",
		"-C", "../..",
		"ai-pr-spec-check",
	)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the target passed with no variables, want a failure:\n%s", out)
	}
	if !strings.Contains(string(out), "usage: make ai-pr-spec-check PR_BODY_FILE=") {
		t.Errorf("the target said:\n%s\nwant the usage line", out)
	}
}
