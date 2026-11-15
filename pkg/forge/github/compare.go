package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/forge"
)

// Limits of the compare call.
const (
	// compareFilesPerPage is the number of files that the adapter asks for in
	// one page. It is the most that GitHub gives.
	compareFilesPerPage = 100
	// maxCompareFiles is the most changed files that Compare lists. More files
	// are forge.ErrTooLarge.
	maxCompareFiles = 1000
)

// The statuses of the answer of the compare call.
const (
	compareIdentical = "identical"
	compareAhead     = "ahead"
	compareBehind    = "behind"
	compareDiverged  = "diverged"
)

// Fields of the answer of the compare call and of its files.
const (
	keyFiles    = "files"
	keyFilename = "filename"
	keyPrevious = "previous_filename"
	keyPatch    = "patch"
)

// compareAnswer is the part of the answer of the compare call that Compare
// reads.
type compareAnswer struct {
	status    string
	mergeBase string
	files     []comparedFile
}

// comparedFile is one file of the answer of the compare call.
type comparedFile struct {
	filename string
	previous string
	status   string
	patch    string
}

// Compare returns the files that changed from base to head, the patch up to
// limit bytes, and whether base is an ancestor of head. Base and head are full
// commit IDs. GitHub compares head with the merge base of the two commits.
//
// It returns forge.ErrInvalid for a commit ID that fails the checks or a limit
// that is not above zero, before any call, forge.ErrNotFound for a missing
// repository or commit, and forge.ErrTooLarge for more than 1000 changed files.
func (f *gitHub) Compare(ctx context.Context, repo, base, head string, limit int64) (forge.Comparison, error) {
	if !f.Capabilities().Has(forge.CapCompare) {
		return forge.Comparison{}, fmt.Errorf(
			"%w: GitHub Compare needs the permission to read the contents", forge.ErrUnsupported,
		)
	}

	r, err := splitRepo(repo)
	if err != nil {
		return forge.Comparison{}, err
	}

	for name, id := range map[string]string{"base": base, "head": head} {
		if idErr := forge.ValidateCommitID(id); idErr != nil {
			return forge.Comparison{}, fmt.Errorf("the %s commit: %w", name, idErr)
		}
	}

	if limit <= 0 {
		return forge.Comparison{}, fmt.Errorf("%w: the size limit of a compare must be above zero", forge.ErrInvalid)
	}

	bearer, err := f.readBearer(ctx, r)
	if err != nil {
		return forge.Comparison{}, f.forget(r, err)
	}

	out, err := f.collectCompare(ctx, compareRead{repo: r, bearer: bearer, base: base, head: head, limit: limit})
	if err != nil {
		return forge.Comparison{}, f.forget(r, err)
	}

	return out, nil
}

// compareRead describes one compare.
type compareRead struct {
	repo   repoName
	bearer string
	base   string
	head   string
	limit  int64
}

// collectCompare reads the pages of the compare call until a page with fewer
// files than the page size.
func (f *gitHub) collectCompare(ctx context.Context, rd compareRead) (forge.Comparison, error) {
	out := forge.Comparison{Files: nil, Patch: nil, Truncated: false, BaseIsAncestor: false}

	var patch bytes.Buffer

	for page := 1; page <= maxCompareFiles/compareFilesPerPage; page++ {
		got, err := f.comparePage(ctx, rd, page)
		if err != nil {
			return forge.Comparison{}, err
		}

		if page == 1 {
			// Defense in depth: the status says that the base is an ancestor.
			// The merge base must be the base too, so that an answer for other
			// commits never counts.
			out.BaseIsAncestor = (got.status == compareIdentical || got.status == compareAhead) &&
				got.mergeBase == rd.base
		}

		if err := addFiles(&out, &patch, got.files, rd.limit); err != nil {
			return forge.Comparison{}, err
		}

		if len(got.files) < compareFilesPerPage {
			out.Patch = patch.Bytes()

			return out, nil
		}
	}

	return forge.Comparison{}, fmt.Errorf(
		"%w: the changes have more than %d files", forge.ErrTooLarge, maxCompareFiles,
	)
}

// addFiles adds the files of one page to out and their patches to patch.
func addFiles(out *forge.Comparison, patch *bytes.Buffer, files []comparedFile, limit int64) error {
	for _, file := range files {
		changed, keep, err := changedFileOf(file)
		if err != nil {
			return err
		}

		if !keep {
			continue
		}

		out.Files = append(out.Files, changed)
		out.Truncated = addPatch(patch, file, changed, limit) || out.Truncated
	}

	return nil
}

// comparePage reads one page of the compare call and checks the answer.
func (f *gitHub) comparePage(ctx context.Context, rd compareRead, page int) (compareAnswer, error) {
	resp, err := f.send(ctx, call{
		method: http.MethodGet,
		path: rd.repo.path() + "/compare/" + rd.base + "..." + rd.head + "?per_page=" +
			strconv.Itoa(compareFilesPerPage) + "&page=" + strconv.Itoa(page),
		bearer: rd.bearer, body: nil, status: f.notFound,
		oversize: fmt.Errorf("%w: the answer for the changes is above the size limit", forge.ErrTooLarge), target: "",
	})
	if err != nil {
		return compareAnswer{}, err
	}

	got, err := parseCompare(resp.body)
	if err != nil {
		return compareAnswer{}, err
	}

	if forge.ValidateCommitID(got.mergeBase) != nil {
		return compareAnswer{}, forge.Unavailable(0, "GitHub gave a compare with no merge base")
	}

	switch got.status {
	case compareIdentical, compareAhead, compareBehind, compareDiverged:
	default:
		return compareAnswer{}, forge.Unavailable(
			0,
			"GitHub gave a compare with a status that the adapter does not know",
		)
	}

	return got, nil
}

// parseCompare reads the fields of the answer that Compare uses. A field that
// is missing or has another type is an empty text, and the checks that follow
// refuse it.
func parseCompare(body []byte) (compareAnswer, error) {
	var fields map[string]json.RawMessage
	if err := decode(body, &fields); err != nil {
		return compareAnswer{}, err
	}

	var (
		base map[string]json.RawMessage
		raw  []map[string]json.RawMessage
	)

	if json.Unmarshal(fields[keyMergeBase], &base) != nil {
		base = nil
	}

	if json.Unmarshal(fields[keyFiles], &raw) != nil {
		raw = nil
	}

	got := compareAnswer{
		status: textField(
			fields,
			keyStatus,
		),
		mergeBase: textField(base, "sha"),
		files:     make([]comparedFile, 0, len(raw)),
	}

	for _, r := range raw {
		got.files = append(got.files, comparedFile{
			filename: textField(r, keyFilename), previous: textField(r, keyPrevious), status: textField(r, keyStatus),
			patch: textField(r, keyPatch),
		})
	}

	return got, nil
}

// textField returns the text at key, or an empty text when the field is
// missing or is not a text.
func textField(fields map[string]json.RawMessage, key string) string {
	var text string
	if json.Unmarshal(fields[key], &text) != nil {
		return ""
	}

	return text
}

// noChange is the file value that goes with an error or a file to skip.
func noChange() forge.ChangedFile {
	return forge.ChangedFile{Path: "", OldPath: "", Status: ""}
}

// changedFileOf maps a file of the answer. keep is false for a file that has
// not changed. It fails closed on a path that validateFilePath refuses and on
// a status that it does not know.
//
// Defense in depth: GitHub gives paths of its own repository, and the caller
// of the port may check paths too. This check stays so that a path that leaves
// the repository never reaches a caller.
func changedFileOf(file comparedFile) (forge.ChangedFile, bool, error) {
	bad := forge.Unavailable(0, "GitHub gave a changed file that the adapter cannot use")

	if validateFilePath(file.filename) != nil {
		return noChange(), false, bad
	}

	changed := forge.ChangedFile{Path: file.filename, OldPath: "", Status: ""}

	switch file.status {
	case "added", "copied":
		changed.Status = forge.FileAdded
	case "removed":
		changed.Status = forge.FileDeleted
	case "modified", "changed":
		changed.Status = forge.FileModified
	case "renamed":
		if validateFilePath(file.previous) != nil {
			return noChange(), false, bad
		}

		changed.Status, changed.OldPath = forge.FileRenamed, file.previous
	case "unchanged":
		return noChange(), false, nil
	default:
		return noChange(), false, bad
	}

	return changed, true, nil
}

// addPatch adds the patch of one file to patch, up to limit bytes in all, and
// reports whether it cut something. The patch has a git header for the file, so
// that the text of many files stays one diff.
func addPatch(patch *bytes.Buffer, file comparedFile, changed forge.ChangedFile, limit int64) bool {
	oldPath := changed.Path
	if changed.OldPath != "" {
		oldPath = changed.OldPath
	}

	var text bytes.Buffer

	text.WriteString("diff --git a/" + oldPath + " b/" + changed.Path + "\n")

	if changed.Status == forge.FileRenamed {
		text.WriteString("rename from " + oldPath + "\nrename to " + changed.Path + "\n")
	}

	if file.patch != "" {
		text.WriteString(patchHeader(changed, oldPath) + file.patch)

		if file.patch[len(file.patch)-1] != '\n' {
			text.WriteByte('\n')
		}
	}

	room := limit - int64(patch.Len())
	if int64(text.Len()) <= room {
		patch.Write(text.Bytes())

		return false
	}

	if room > 0 {
		// The cut stays at a rune boundary, so that the patch stays valid UTF-8.
		cut := room
		for cut > 0 && !utf8.RuneStart(text.Bytes()[cut]) {
			cut--
		}

		patch.Write(text.Bytes()[:cut])
	}

	return true
}

// patchHeader returns the two lines that name the files of a patch.
func patchHeader(changed forge.ChangedFile, oldPath string) string {
	from, to := "a/"+oldPath, "b/"+changed.Path

	if changed.Status == forge.FileAdded {
		from = "/dev/null"
	}

	if changed.Status == forge.FileDeleted {
		to = "/dev/null"
	}

	return "--- " + from + "\n+++ " + to + "\n"
}
