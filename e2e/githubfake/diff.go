package githubfake

import (
	"bytes"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// keySHA is the field of the object ID in an answer.
const keySHA = "sha"

// changedFile is a file in the answer of the compare call.
type changedFile struct {
	sha      string
	filename string
	previous string
	status   string
	patch    string
}

// wire returns the file as the answer shows it. A file with no old path or no
// patch has neither field, as in the answer of GitHub.
func (c changedFile) wire() map[string]string {
	out := map[string]string{keySHA: c.sha, "filename": c.filename, "status": c.status}

	if c.previous != "" {
		out["previous_filename"] = c.previous
	}

	if c.patch != "" {
		out["patch"] = c.patch
	}

	return out
}

// diffFiles lists the files that differ between the trees of two commits, in
// the order of the paths. A deleted path and an added path with the same blob
// make a rename.
func diffFiles(rp *repo, from, to *commit) []changedFile {
	var added, removed []string

	var files []changedFile

	for _, p := range slices.Sorted(maps.Keys(to.tree)) {
		old, existed := from.tree[p]

		switch {
		case !existed:
			added = append(added, p)
		case old != to.tree[p]:
			files = append(files, changedFile{
				sha: to.tree[p], filename: p, previous: "", status: "modified",
				patch: modifiedPatch(rp.blobs[old], rp.blobs[to.tree[p]]),
			})
		case from.modeOf(p) != to.modeOf(p):
			// A change of the mode alone has no patch.
			files = append(files, changedFile{sha: to.tree[p], filename: p, previous: "", status: "changed", patch: ""})
		default:
			// The same blob and the same mode: the file did not change.
		}
	}

	for p := range from.tree {
		if _, kept := to.tree[p]; !kept {
			removed = append(removed, p)
		}
	}

	slices.Sort(removed)

	files = append(files, pairRenames(rp, from, to, added, removed)...)

	slices.SortFunc(files, func(a, b changedFile) int { return strings.Compare(a.filename, b.filename) })

	return files
}

// pairRenames turns the added and removed paths into files. A removed path and
// an added path with the same blob ID are one rename.
func pairRenames(rp *repo, from, to *commit, added, removed []string) []changedFile {
	var files []changedFile

	taken := map[string]bool{}

	for _, a := range added {
		renamedFrom := ""

		for _, d := range removed {
			if !taken[d] && from.tree[d] == to.tree[a] {
				renamedFrom = d

				break
			}
		}

		if renamedFrom != "" {
			taken[renamedFrom] = true

			files = append(files, changedFile{
				sha: to.tree[a], filename: a, previous: renamedFrom, status: "renamed", patch: "",
			})

			continue
		}

		files = append(files, changedFile{
			sha: to.tree[a], filename: a, previous: "", status: "added", patch: wholePatch(rp.blobs[to.tree[a]], "+"),
		})
	}

	for _, d := range removed {
		if !taken[d] {
			files = append(files, changedFile{
				sha: from.tree[d], filename: d, previous: "", status: "removed",
				patch: wholePatch(rp.blobs[from.tree[d]], "-"),
			})
		}
	}

	return files
}

// linesOf splits content into lines. It returns nil for empty or binary content,
// and for a blob that the fake does not have, for example a submodule.
func linesOf(content []byte) []string {
	if len(content) == 0 || bytes.IndexByte(content, 0) >= 0 {
		return nil
	}

	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

// wholePatch is the patch of a file that is new or gone: one hunk, in which
// each line has the sign.
func wholePatch(content []byte, sign string) string {
	lines := linesOf(content)
	if lines == nil {
		return ""
	}

	header := "@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@\n"
	if sign == "-" {
		header = "@@ -1," + strconv.Itoa(len(lines)) + " +0,0 @@\n"
	}

	return header + signed(lines, sign)
}

// modifiedPatch is the patch of a file whose content changed: one hunk that
// removes each old line and adds each new line.
func modifiedPatch(before, after []byte) string {
	old, now := linesOf(before), linesOf(after)
	if old == nil && now == nil {
		return ""
	}

	return "@@ -1," + strconv.Itoa(len(old)) + " +1," + strconv.Itoa(len(now)) + " @@\n" +
		signed(old, "-") + signed(now, "+")
}

func signed(lines []string, sign string) string {
	var b strings.Builder

	for _, l := range lines {
		b.WriteString(sign + l + "\n")
	}

	return b.String()
}

// pagedFiles returns the page of the files that the query asks for, with the
// default of 30 files in a page and at most 100.
func pagedFiles(files []changedFile, r *http.Request) []map[string]string {
	const (
		defaultFiles = 30
		mostFiles    = 100
	)

	q := r.URL.Query()
	perPage, page := min(pageOf(q.Get("per_page"), defaultFiles), mostFiles), pageOf(q.Get("page"), 1)

	start := min((page-1)*perPage, len(files))
	listed := make([]map[string]string, 0, perPage)

	for _, c := range files[start:min(start+perPage, len(files))] {
		listed = append(listed, c.wire())
	}

	return listed
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
