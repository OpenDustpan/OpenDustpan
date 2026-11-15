//go:build e2e

package e2e_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	docsFileMode = 0o600
	docsDirMode  = 0o750
	docsPage     = "docs/guides/a.md"
)

func docsScope() []string { return []string{"docs/guides", "docs/reference", "README.md"} }

func docsDefault() e2e.DocsConfig { return e2e.NewDocsConfig(0, nil) }

// docsFixture writes pages (path to content) under a new root.
func docsFixture(t *testing.T, pages map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for path, content := range pages {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), docsDirMode); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte(content), docsFileMode); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func fenced(body string) string { return "```sh\n" + body + "```\n" }

func collectOne(t *testing.T, content string) ([]e2e.DocsCommand, error) {
	t.Helper()

	root := docsFixture(t, map[string]string{docsPage: content})

	return e2e.CollectCommands(root, []string{"docs/guides"})
}

func runOne(t *testing.T, content string, cfg e2e.DocsConfig) e2e.DocsResult {
	t.Helper()

	cmds, err := collectOne(t, content)
	if err != nil {
		t.Fatal(err)
	}

	res, err := e2e.RunCommands(t.Context(), cfg, cmds)
	if err != nil {
		t.Fatal(err)
	}

	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}

	return res[0]
}

// TestDocsCommandsCorpusListsBlocks lists each shell block of the real pages.
func TestDocsCommandsCorpusListsBlocks(t *testing.T) {
	t.Parallel()

	cmds, err := e2e.CollectCommands(repoRoot(t), docsScope())
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range cmds {
		t.Logf("%s block %d", c.Page(), c.Index())

		if c.Page() == "" || c.Index() < 1 || strings.TrimSpace(c.Text()) == "" {
			t.Errorf("bad block: page %q index %d", c.Page(), c.Index())
		}
	}
}

func TestDocsCommandsCorpusShape(t *testing.T) {
	t.Parallel()

	cmds, err := collectOne(t, "~~~bash title=x\necho a\n~~~\n\n```console\n$ no\n```\n\n```shell\necho b\n```\n")
	if err != nil {
		t.Fatal(err)
	}

	if len(cmds) != 2 || cmds[0].Text() != "echo a\n" || cmds[1].Index() != 2 {
		t.Fatalf("got %d blocks", len(cmds))
	}
}

func TestDocsCommandsCorpusExactBytes(t *testing.T) {
	t.Parallel()

	body := "echo  'a\tb'  \n  indented\n"

	cmds, err := collectOne(t, fenced(body))
	if err != nil || len(cmds) != 1 || cmds[0].Text() != body {
		t.Fatalf("got %d blocks, err %v", len(cmds), err)
	}

	other, err := collectOne(t, fenced(strings.Replace(body, "a", "x", 1)))
	if err != nil || other[0].Text() == cmds[0].Text() {
		t.Fatal("a changed byte must change the listed text")
	}
}

func TestDocsCommandsCorpusRejects(t *testing.T) {
	t.Parallel()

	marker := "<!-- docs-check-skip: needs-human -->\n"

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"empty corpus", "no blocks\n", "zero blocks"},
		{"unclosed fence", "```sh\ntrue\n", docsPage},
		{"empty block", "```sh\n\n```\n", "empty block"},
		{"oversize block", fenced(strings.Repeat("a", 65537) + "\n"), "limit"},
		{"stale marker before text", marker + "\ntext\n" + fenced("true\n"), "block 1"},
		{"stale marker before other fence", marker + "```console\n$ x\n```\n" + fenced("true\n"), "block 1"},
		{"stale marker at end", fenced("true\n") + marker, "block 2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := collectOne(t, tc.content)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error with %q, got %v", tc.want, err)
			}
		})
	}
}

func TestDocsCommandsCorpusMissingPath(t *testing.T) {
	t.Parallel()

	root := docsFixture(t, map[string]string{docsPage: fenced("true\n")})

	_, err := e2e.CollectCommands(root, []string{"docs/guides", "docs/reference"})
	if err == nil || !strings.Contains(err.Error(), "docs/reference") {
		t.Fatalf("want error with the missing path, got %v", err)
	}
}

func TestDocsCommandRunsAsWritten(t *testing.T) {
	t.Parallel()

	root := docsFixture(t, map[string]string{
		docsPage: fenced("echo one > f.txt\n") + "\n" + fenced("test \"$(cat f.txt)\" = one\n") +
			"\n<!-- docs-check-skip: needs-secret -->\n" + fenced("exit 3\n"),
		"docs/guides/b.md": fenced("test ! -e f.txt\n"),
	})

	cmds, err := e2e.CollectCommands(root, []string{"docs/guides"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := e2e.RunCommands(t.Context(), docsDefault(), cmds)
	if err != nil {
		t.Fatal(err)
	}

	if err = e2e.CheckCovered(cmds, res); err != nil {
		t.Fatal(err)
	}

	want := []e2e.DocsStatus{e2e.DocsPassed, e2e.DocsPassed, e2e.DocsSkipped, e2e.DocsPassed}
	for i, r := range res {
		if r.Status != want[i] {
			t.Errorf("block %d of %s: %s, want %s\n%s", r.Command.Index(), r.Command.Page(), r.Status, want[i],
				r.Report())
		}
	}

	if err = e2e.CheckCovered(cmds, res[:1]); err == nil {
		t.Error("a block without a result must fail the check")
	}
}

// TestDocsCommandsRealPages runs every block of the real pages as written. It
// runs only for make docs-commands-check (E2E_DOCS_COMMANDS=1), because it
// runs the text of the pages.
func TestDocsCommandsRealPages(t *testing.T) {
	t.Parallel()

	if os.Getenv("E2E_DOCS_COMMANDS") != "1" {
		t.Skip("set E2E_DOCS_COMMANDS=1 (make docs-commands-check) to run the blocks of the pages")
	}

	cmds, err := e2e.CollectCommands(repoRoot(t), docsScope())
	if err != nil {
		t.Fatal(err)
	}

	res, err := e2e.RunCommands(t.Context(), docsDefault(), cmds)
	if err != nil {
		t.Fatal(err)
	}

	if err = e2e.CheckCovered(cmds, res); err != nil {
		t.Fatal(err)
	}

	for _, r := range res {
		if r.Status == e2e.DocsFailed {
			t.Error(r.Report())
		}
	}
}

func TestDocsCommandsFailureReport(t *testing.T) {
	t.Parallel()

	r := runOne(t, fenced("echo boom-output\nexit 4\n"), docsDefault())
	if r.Status != e2e.DocsFailed {
		t.Fatalf("status %s", r.Status)
	}

	for _, want := range []string{"docs/guides/a.md block 1", "exit 4", "boom-output", "make docs-commands-check"} {
		if !strings.Contains(r.Report(), want) {
			t.Errorf("report lacks %q:\n%s", want, r.Report())
		}
	}
}

func TestDocsCommandsRefuseSudo(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "ran")
	bodies := []string{
		"sudo touch " + marker + "\n",
		"true && sudo rm -rf /\n",
		"rm -rf /tmp/x-docs-check\n",
		"echo x > /etc/docs-check\n",
	}

	for _, body := range bodies {
		r := runOne(t, fenced(body), docsDefault())
		if r.Status != e2e.DocsFailed || !strings.Contains(r.Reason, "refused") {
			t.Errorf("%q: status %s reason %q, want refused", body, r.Status, r.Reason)
		}
	}

	if _, err := os.Stat(marker); err == nil {
		t.Error("the refused block ran")
	}
}

func TestDocsCommandsTimeoutKillsProcessGroup(t *testing.T) {
	t.Parallel()

	// The block prints the pid of its background child before it waits.
	cmds, err := collectOne(t, fenced("sleep 600 &\necho child-pid=$!\nsleep 600\n"))
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()

	res, err := e2e.RunCommands(t.Context(), e2e.NewDocsConfig(time.Second, nil), cmds)
	if err != nil {
		t.Fatal(err)
	}

	if res[0].Status != e2e.DocsFailed || !strings.Contains(res[0].Reason, "longer than") {
		t.Fatalf("status %s reason %q", res[0].Status, res[0].Reason)
	}

	if time.Since(start) > 30*time.Second {
		t.Fatal("the timeout did not stop the block")
	}

	_, after, ok := strings.Cut(res[0].Output, "child-pid=")
	if !ok {
		t.Fatalf("no child pid in output %q", res[0].Output)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(after))
	if err != nil {
		t.Fatal(err)
	}

	waitProcessGone(t, pid)
}

// waitProcessGone fails the test when the process still runs after five seconds.
func waitProcessGone(t *testing.T, pid int) {
	t.Helper()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for syscall.Kill(pid, 0) == nil {
		select {
		case <-deadline.C:
			t.Fatalf("child process %d still runs", pid)
		case <-t.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func TestDocsCommandsOutputCap(t *testing.T) {
	t.Parallel()

	r := runOne(t, fenced("head -c 3000000 /dev/zero | tr '\\0' a\n"), docsDefault())
	if r.Status != e2e.DocsPassed {
		t.Fatalf("status %s: %s", r.Status, r.Reason)
	}

	if len(r.Output) > e2e.DocsOutputCap || r.Output == "" {
		t.Fatalf("output has %d bytes, the cap is %d", len(r.Output), e2e.DocsOutputCap)
	}
}

func TestDocsCommandsRedactsTokens(t *testing.T) {
	t.Parallel()

	r := runOne(t, fenced("echo 'Authorization: Bearer abcdef0123456789'\n"), docsDefault())
	if strings.Contains(r.Output, "abcdef0123456789") {
		t.Fatalf("token in output: %q", r.Output)
	}
}

func TestDocsCommandsUnknownSkipReason(t *testing.T) {
	t.Parallel()

	_, err := collectOne(t, "<!-- docs-check-skip: because-i-say -->\n"+fenced("true\n"))
	if err == nil || !strings.Contains(err.Error(), "docs/guides/a.md block 1") {
		t.Fatalf("want gate failure with page and block, got %v", err)
	}
}

func TestDocsCommandsDockerGate(t *testing.T) {
	t.Parallel()

	r := runOne(t, fenced("docker ps\n"), docsDefault())
	if r.Status != e2e.DocsFailed {
		t.Errorf("docker without marker: status %s", r.Status)
	}

	marked := "<!-- docs-check-skip: needs-docker -->\n" + fenced("docker ps\n")
	unset := e2e.NewDocsConfig(0, func(string) string { return "" })

	r = runOne(t, marked, unset)
	if r.Status != e2e.DocsSkipped || r.Reason != e2e.DocsSkipNeedsDocker {
		t.Errorf("docker with marker, E2E_COMPOSE unset: status %s reason %q", r.Status, r.Reason)
	}
}
