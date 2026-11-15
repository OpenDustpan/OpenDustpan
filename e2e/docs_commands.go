//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// docsCommandMaxText is the largest block that the corpus accepts.
	docsCommandMaxText = 65536
	// DocsOutputCap is the most output bytes that the runner keeps for one block.
	DocsOutputCap = 1048576
	// DocsBlockTimeout is the time limit of one block.
	DocsBlockTimeout = 300 * time.Second

	docsDirMode  = 0o750
	docsFileMode = 0o600
	// docsKillGrace is the time that Wait gives to the pipes after the kill of a process group.
	docsKillGrace = 2 * time.Second
	// docsScriptPath is the place of the block text, relative to the work directory of its page.
	docsScriptPath = "tmp/docs-block.sh"
	docsMarkerName = "docs-check-skip"
	// docsFenceIndentMax is the most spaces before a closing fence.
	docsFenceIndentMax = 3
)

// Skip reasons. This list is closed: any other reason fails the check.
const (
	DocsSkipNeedsSecret = "needs-secret"
	DocsSkipNeedsHuman  = "needs-human"
	DocsSkipNeedsDocker = "needs-docker"
	DocsSkipDestructive = "destructive-outside-fixture"
)

var (
	errDocsCorpus = errors.New("docs corpus")

	docsFenceOpen   = regexp.MustCompile("^( {0,3})(`{3,}|~{3,})(.*)$")
	docsSkipMarker  = regexp.MustCompile(`^\s*<!--\s*` + docsMarkerName + `:?\s*([^\s>]*)\s*-->\s*$`)
	docsSudoWord    = regexp.MustCompile("(^|[\\s;&|(`])sudo(\\s|$)")
	docsDockerWord  = regexp.MustCompile("(^|[\\s;&|(`])docker(\\s|$)")
	docsAbsRm       = regexp.MustCompile(`(^|[\s;&|(])rm\s+(-\S+\s+)*/`)
	docsAbsRedirect = regexp.MustCompile(`>>?\s*/([^d]|d[^e]|de[^v])`)
	docsTokenText   = regexp.MustCompile(`(?i)(bearer\s+|token[=: ]+)[A-Za-z0-9._~+/=-]{8,}`)
)

func isDocsSkipReason(reason string) bool {
	switch reason {
	case DocsSkipNeedsSecret, DocsSkipNeedsHuman, DocsSkipNeedsDocker, DocsSkipDestructive:
		return true
	default:
		return false
	}
}

// DocsCommand is one fenced shell block of a page.
type DocsCommand struct {
	page   string
	index  int
	text   string
	reason string
}

func newDocsCommand(page string, index int, text, reason string) (DocsCommand, error) {
	if strings.TrimSpace(text) == "" {
		return DocsCommand{}, fmt.Errorf("%w: page %s block %d: empty block", errDocsCorpus, page, index)
	}

	if len(text) > docsCommandMaxText {
		return DocsCommand{}, fmt.Errorf("%w: page %s block %d: block has %d bytes, the limit is %d",
			errDocsCorpus, page, index, len(text), docsCommandMaxText)
	}

	return DocsCommand{page: page, index: index, text: text, reason: reason}, nil
}

// Page is the path of the page, relative to the root.
func (c DocsCommand) Page() string { return c.page }

// Index is the 1-based place of the block among the shell blocks of its page.
func (c DocsCommand) Index() int { return c.index }

// Text is the exact text of the block.
func (c DocsCommand) Text() string { return c.text }

// SkipReason is the reason of the skip marker, or an empty string.
func (c DocsCommand) SkipReason() string { return c.reason }

// DocsStatus is the outcome of one block.
type DocsStatus string

// The outcomes of a block.
const (
	DocsPassed  DocsStatus = "passed"
	DocsFailed  DocsStatus = "failed"
	DocsSkipped DocsStatus = "skipped"
)

// DocsResult is the outcome of one block of RunCommands.
type DocsResult struct {
	Command DocsCommand
	Status  DocsStatus
	// Reason is the skip reason of a skipped block, or the refusal text of a failed block.
	Reason string
	Output string
}

// Report is the failure text of a block.
func (r DocsResult) Report() string {
	var msg strings.Builder

	fmt.Fprintf(&msg, "The docs check failed on page %s block %d: %s\n", r.Command.page, r.Command.index, r.Reason)
	fmt.Fprintf(&msg, "Run `make docs-commands-check` to see it again.\nCommand:\n%s\nOutput:\n%s\n",
		r.Command.text, r.Output)

	return msg.String()
}

// CollectCommands lists each fenced block tagged sh, bash or shell in the
// scope. A scope entry is a directory (all .md files below it) or a file, both
// relative to root. It fails on a missing entry, an unclosed fence, a skip
// marker with an unknown reason, a stale skip marker and an empty corpus.
func CollectCommands(root string, scope []string) ([]DocsCommand, error) {
	var pages []string

	for _, entry := range scope {
		found, err := scopePages(root, entry)
		if err != nil {
			return nil, err
		}

		pages = append(pages, found...)
	}

	slices.Sort(pages)

	docsRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("%w: root %s: %w", errDocsCorpus, root, err)
	}

	all, readErr := readPages(docsRoot, pages)
	if closeErr := docsRoot.Close(); closeErr != nil && readErr == nil {
		readErr = fmt.Errorf("%w: close root: %w", errDocsCorpus, closeErr)
	}

	if readErr != nil {
		return nil, readErr
	}

	if len(all) == 0 {
		return nil, fmt.Errorf("%w: zero blocks found", errDocsCorpus)
	}

	return all, nil
}

func readPages(root *os.Root, pages []string) ([]DocsCommand, error) {
	var all []DocsCommand

	for _, page := range pages {
		data, err := root.ReadFile(page)
		if err != nil {
			return nil, fmt.Errorf("%w: page %s: %w", errDocsCorpus, page, err)
		}

		cmds, err := collectPage(page, string(data))
		if err != nil {
			return nil, err
		}

		all = append(all, cmds...)
	}

	return all, nil
}

// scopePages lists the pages of one scope entry, relative to root.
func scopePages(root, entry string) ([]string, error) {
	full := filepath.Join(root, entry)

	info, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("%w: path %s: %w", errDocsCorpus, entry, err)
	}

	if !info.IsDir() {
		return []string{entry}, nil
	}

	var pages []string

	walkErr := filepath.WalkDir(full, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("relative path of %s: %w", path, relErr)
		}

		pages = append(pages, rel)

		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("%w: path %s: %w", errDocsCorpus, entry, walkErr)
	}

	return pages, nil
}

// pageParser reads the fences of one page.
type pageParser struct {
	page     string
	lines    []string
	pos      int
	next     int
	marker   string
	hasMark  bool
	markLine int
	cmds     []DocsCommand
}

func collectPage(page, content string) ([]DocsCommand, error) {
	p := &pageParser{
		page: page, lines: strings.Split(content, "\n"), pos: 0, next: 1,
		marker: "", hasMark: false, markLine: 0, cmds: nil,
	}

	for p.pos < len(p.lines) {
		if err := p.step(); err != nil {
			return nil, err
		}
	}

	if p.hasMark {
		return nil, p.stale()
	}

	return p.cmds, nil
}

func (p *pageParser) stale() error {
	return fmt.Errorf("%w: page %s block %d: skip marker at line %d has no shell block after it",
		errDocsCorpus, p.page, p.next, p.markLine)
}

// plain handles a line outside a fence: it can set a marker, and it ends one.
func (p *pageParser) plain(line string) error {
	if p.hasMark {
		return p.stale()
	}

	if sm := docsSkipMarker.FindStringSubmatch(line); sm != nil {
		p.marker, p.hasMark, p.markLine = sm[1], true, p.pos
	}

	return nil
}

func (p *pageParser) step() error {
	line := p.lines[p.pos]
	p.pos++

	m := docsFenceOpen.FindStringSubmatch(line)
	if m == nil {
		return p.plain(line)
	}

	indent, fence, info := len(m[1]), m[2], strings.TrimSpace(m[3])
	if fence[0] == '`' && strings.Contains(info, "`") {
		return p.plain(line)
	}

	openLine := p.pos

	var body []string

	closed := false

	for ; p.pos < len(p.lines); p.pos++ {
		if isFenceClose(p.lines[p.pos], fence) {
			closed = true
			p.pos++

			break
		}

		body = append(body, trimIndent(p.lines[p.pos], indent))
	}

	if !closed {
		return fmt.Errorf("%w: page %s: fence at line %d is not closed", errDocsCorpus, p.page, openLine)
	}

	return p.block(info, body)
}

func (p *pageParser) block(info string, body []string) error {
	words := strings.Fields(info)
	if len(words) == 0 || (words[0] != "sh" && words[0] != "bash" && words[0] != "shell") {
		if p.hasMark {
			return p.stale()
		}

		return nil
	}

	reason := ""

	if p.hasMark {
		if !isDocsSkipReason(p.marker) {
			return fmt.Errorf("%w: page %s block %d: skip reason %q is not in the allowlist",
				errDocsCorpus, p.page, p.next, p.marker)
		}

		reason = p.marker
	}

	cmd, err := newDocsCommand(p.page, p.next, strings.Join(body, "\n")+"\n", reason)
	if err != nil {
		return err
	}

	p.cmds = append(p.cmds, cmd)
	p.next++
	p.hasMark = false

	return nil
}

func isFenceClose(line, fence string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > docsFenceIndentMax {
		return false
	}

	trimmed = strings.TrimRight(trimmed, " \t")

	return len(trimmed) >= len(fence) && strings.Trim(trimmed, fence[:1]) == ""
}

func trimIndent(line string, n int) string {
	i := 0
	for i < n && i < len(line) && line[i] == ' ' {
		i++
	}

	return line[i:]
}

// DocsConfig sets how RunCommands runs blocks.
type DocsConfig struct {
	timeout time.Duration
	getenv  func(string) string
}

// NewDocsConfig makes a config. A timeout of zero or less means
// DocsBlockTimeout. A nil getenv means os.Getenv. The getenv function reads
// the variables of the closed environment allowlist: PATH, OPENDUSTPAN_URL
// and E2E_COMPOSE.
func NewDocsConfig(timeout time.Duration, getenv func(string) string) DocsConfig {
	if timeout <= 0 {
		timeout = DocsBlockTimeout
	}

	if getenv == nil {
		getenv = os.Getenv
	}

	return DocsConfig{timeout: timeout, getenv: getenv}
}

// RunCommands runs each listed block, as written, with bash in a fresh work
// directory for its page. Blocks of one page run in page order. It returns one
// result for each block. A block that fails is a result with status failed; the
// error is for a failure to make or remove a work directory.
func RunCommands(ctx context.Context, cfg DocsConfig, cmds []DocsCommand) ([]DocsResult, error) {
	if cfg.getenv == nil || cfg.timeout <= 0 {
		cfg = NewDocsConfig(cfg.timeout, cfg.getenv)
	}

	parent, err := os.MkdirTemp("", "docs-commands-")
	if err != nil {
		return nil, fmt.Errorf("make the work directory: %w", err)
	}

	results, runErr := runAll(ctx, cfg, cmds, parent)

	if rmErr := os.RemoveAll(parent); rmErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("remove the work directory: %w", rmErr))
	}

	return results, runErr
}

func runAll(ctx context.Context, cfg DocsConfig, cmds []DocsCommand, parent string) ([]DocsResult, error) {
	var results []DocsResult

	dirs := map[string]string{}

	for _, cmd := range cmds {
		if res, done := docsPrecheck(cfg, cmd); done {
			results = append(results, res)

			continue
		}

		dir, ok := dirs[cmd.page]
		if !ok {
			dir = filepath.Join(parent, strconv.Itoa(len(dirs)))
			if err := os.MkdirAll(filepath.Join(dir, "tmp"), docsDirMode); err != nil {
				return results, fmt.Errorf("work directory of page %s: %w", cmd.page, err)
			}

			dirs[cmd.page] = dir
		}

		results = append(results, runDocsBlock(ctx, cfg, cmd, dir))
	}

	return results, nil
}

// docsPrecheck decides the blocks that RunCommands does not run.
func docsPrecheck(cfg DocsConfig, cmd DocsCommand) (DocsResult, bool) {
	res := DocsResult{Command: cmd, Status: DocsFailed, Reason: "", Output: ""}

	if cmd.reason != "" && cmd.reason != DocsSkipNeedsDocker {
		res.Status, res.Reason = DocsSkipped, cmd.reason

		return res, true
	}

	// Defense in depth: the runner refuses these texts here, and the process
	// also gets a closed environment and a fresh work directory.
	if docsSudoWord.MatchString(cmd.text) {
		res.Reason = "refused: the block uses sudo"

		return res, true
	}

	if docsAbsRm.MatchString(cmd.text) || docsAbsRedirect.MatchString(cmd.text) {
		res.Reason = "refused: the block writes outside its work directory"

		return res, true
	}

	if docsDockerWord.MatchString(cmd.text) && cmd.reason != DocsSkipNeedsDocker {
		res.Reason = "the block uses docker and has no needs-docker marker"

		return res, true
	}

	if cmd.reason == DocsSkipNeedsDocker && cfg.getenv("E2E_COMPOSE") != "1" {
		res.Status, res.Reason = DocsSkipped, DocsSkipNeedsDocker

		return res, true
	}

	return res, false
}

func runDocsBlock(ctx context.Context, cfg DocsConfig, block DocsCommand, dir string) DocsResult {
	res := DocsResult{Command: block, Status: DocsFailed, Reason: "", Output: ""}

	if err := writeDocsScript(dir, block.text); err != nil {
		res.Reason = err.Error()

		return res
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	// The script path is a constant, so the block text is never an argument.
	cmd := exec.CommandContext(ctx, "bash", docsScriptPath)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + cfg.getenv("PATH"),
		"HOME=" + dir,
		"TMPDIR=" + filepath.Join(dir, "tmp"),
		"OPENDUSTPAN_URL=" + cfg.getenv("OPENDUSTPAN_URL"),
		"E2E_COMPOSE=" + cfg.getenv("E2E_COMPOSE"),
	}
	cmd.SysProcAttr = new(syscall.SysProcAttr)
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		// Kill the whole process group, so no child outlives the block.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = docsKillGrace

	out := new(cappedBuffer)
	cmd.Stdout, cmd.Stderr = out, out

	runErr := cmd.Run()

	// A background child can outlive a block that exits.
	if cmd.Process != nil {
		killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
			runErr = errors.Join(runErr, killErr)
		}
	}

	res.Output = docsTokenText.ReplaceAllString(out.String(), "${1}[redacted]")

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.Reason = fmt.Sprintf("the block ran longer than %s", cfg.timeout)
	case runErr != nil:
		res.Reason = runErr.Error()
	default:
		res.Status = DocsPassed
	}

	return res
}

// writeDocsScript writes the block text, unchanged, into the work directory.
func writeDocsScript(dir, text string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open the work directory: %w", err)
	}

	writeErr := root.WriteFile(docsScriptPath, []byte(text), docsFileMode)
	closeErr := root.Close()

	if writeErr != nil {
		return fmt.Errorf("write the block text: %w", writeErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close the work directory: %w", closeErr)
	}

	return nil
}

// cappedBuffer keeps at most DocsOutputCap bytes and drops the rest.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if room := DocsOutputCap - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}

	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// PagesWithBlocks lists, sorted, each page of the scope that has an in-scope
// shell block. Unlike CollectCommands, it accepts a scope with no block.
func PagesWithBlocks(root string, scope []string) ([]string, error) {
	var withBlocks []string

	for _, entry := range scope {
		pages, err := scopePages(root, entry)
		if err != nil {
			return nil, err
		}

		for _, page := range pages {
			data, readErr := readRooted(root, page)
			if readErr != nil {
				return nil, readErr
			}

			cmds, collectErr := collectPage(page, string(data))
			if collectErr != nil {
				return nil, collectErr
			}

			if len(cmds) > 0 {
				withBlocks = append(withBlocks, page)
			}
		}
	}

	slices.Sort(withBlocks)

	return slices.Compact(withBlocks), nil
}

// readRooted reads a file below root; a path that leaves root fails.
func readRooted(root, path string) ([]byte, error) {
	docsRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("%w: root %s: %w", errDocsCorpus, root, err)
	}

	data, readErr := docsRoot.ReadFile(path)
	closeErr := docsRoot.Close()

	if readErr != nil {
		return nil, fmt.Errorf("%w: file %s: %w", errDocsCorpus, path, readErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("%w: close root: %w", errDocsCorpus, closeErr)
	}

	return data, nil
}

// ReadPageList reads a page list: one page path on each line. It fails on an
// empty line. An empty file is an empty list.
func ReadPageList(root, path string) ([]string, error) {
	data, err := readRooted(root, path)
	if err != nil {
		return nil, err
	}

	var list []string

	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" && len(data) == 0 {
			break
		}

		if strings.TrimSpace(line) == "" || line != strings.TrimSpace(line) {
			return nil, fmt.Errorf("%w: page list %s line %d: empty line or space around the path",
				errDocsCorpus, path, i+1)
		}

		list = append(list, line)
	}

	return list, nil
}

// CheckPageLists checks the checked list and the pending list against the
// pages of the scope. It fails when a page with an in-scope block is in
// neither list, when a page is in both lists, when a listed page does not exist
// or has no in-scope block, and when a list has a duplicate, an unsorted line
// or a path outside the scope. An empty list is valid.
func CheckPageLists(root string, scope, checked, pending []string) error {
	withBlocks, err := PagesWithBlocks(root, scope)
	if err != nil {
		return err
	}

	var problems []string

	for name, list := range map[string][]string{"checked": checked, "pending": pending} {
		problems = append(problems, checkOneList(name, list, scope, withBlocks)...)
	}

	for _, page := range checked {
		if slices.Contains(pending, page) {
			problems = append(problems, fmt.Sprintf("page %s is in both lists", page))
		}
	}

	for _, page := range withBlocks {
		if !slices.Contains(checked, page) && !slices.Contains(pending, page) {
			problems = append(problems, fmt.Sprintf("page %s has a shell block and is in neither list", page))
		}
	}

	if len(problems) > 0 {
		slices.Sort(problems)

		return fmt.Errorf("%w: page lists: %s", errDocsCorpus, strings.Join(problems, "; "))
	}

	return nil
}

func checkOneList(name string, list, scope, withBlocks []string) []string {
	var problems []string

	for i, page := range list {
		if !inDocsScope(page, scope) {
			problems = append(problems, fmt.Sprintf("%s list: path %s is outside the scope", name, page))
		} else if !slices.Contains(withBlocks, page) {
			problems = append(problems,
				fmt.Sprintf("%s list: page %s does not exist or has no shell block", name, page))
		}

		if i > 0 && list[i-1] == page {
			problems = append(problems, fmt.Sprintf("%s list: duplicate line %s", name, page))
		} else if i > 0 && list[i-1] > page {
			problems = append(problems, fmt.Sprintf("%s list: line %s is not in sorted order", name, page))
		}
	}

	return problems
}

func inDocsScope(page string, scope []string) bool {
	if page != filepath.Clean(page) || filepath.IsAbs(page) || !strings.HasSuffix(page, ".md") {
		return false
	}

	for _, entry := range scope {
		if page == entry || strings.HasPrefix(page, strings.TrimSuffix(entry, "/")+"/") {
			return true
		}
	}

	return false
}

// CheckCovered fails when a listed block has no result.
func CheckCovered(cmds []DocsCommand, results []DocsResult) error {
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.Command.page+"#"+strconv.Itoa(r.Command.index)] = true
	}

	var missing []string

	for _, c := range cmds {
		if !seen[c.page+"#"+strconv.Itoa(c.index)] {
			missing = append(missing, fmt.Sprintf("page %s block %d", c.page, c.index))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: blocks without a result: %s", errDocsCorpus, strings.Join(missing, ", "))
	}

	return nil
}
