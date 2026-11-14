// Package adrnumbers checks the numbers of the ADRs and the RFCs of a
// repository, and the ADR numbers that its docs cite.
package adrnumbers

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	adrDir  = "docs/adr"
	rfcDir  = "docs/rfc"
	docsDir = "docs"

	// minDuplicates is the number of files that make a number a duplicate.
	minDuplicates = 2

	// maxDocBytes is the largest doc that Check reads. A larger doc is an
	// error, so that Check never reads an unbounded file.
	maxDocBytes = 4 << 20
)

// errDocTooLarge is returned when a doc is larger than maxDocBytes.
var errDocTooLarge = errors.New("doc is larger than the limit")

// fileName matches the name of a numbered ADR or RFC: four digits, a dash, a
// slug and the .md extension. Other files, such as INDEX.md, are not numbered
// and are skipped.
var fileName = regexp.MustCompile(`^(\d{4})-[a-z0-9-]+\.md$`)

// citation matches "ADR" and a number, for example "ADR 214".
var citation = regexp.MustCompile(`\bADR (\d+)\b`)

// Check returns the problems in the numbering of the docs under root, sorted.
// It reports a number that two files of docs/adr share, a number that two
// files of docs/rfc share, and an ADR number that a .md file under docs/
// cites when docs/adr has no file with that number. The exempt paths are
// slash-separated paths from root of files whose citations are not checked;
// the number checks still apply to them. An empty result means that the
// numbering is clean. Check returns an error when it cannot read the tree, so
// a read error never looks like a clean result.
func Check(root string, exempt ...string) ([]string, error) {
	repo, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", root, err)
	}
	findings, checkErr := checkFS(repo.FS(), exempt)
	closeErr := repo.Close()
	if checkErr != nil {
		return nil, checkErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close %s: %w", root, closeErr)
	}
	return findings, nil
}

// checkFS runs the checks of Check on the tree of fsys, which holds the docs.
func checkFS(fsys fs.FS, exempt []string) ([]string, error) {
	adrs, err := numbered(fsys, adrDir)
	if err != nil {
		return nil, err
	}
	rfcs, err := numbered(fsys, rfcDir)
	if err != nil {
		return nil, err
	}
	findings := duplicates(adrDir, adrs)
	findings = append(findings, duplicates(rfcDir, rfcs)...)

	cites, err := citedWithoutFile(fsys, adrs, exempt)
	if err != nil {
		return nil, err
	}
	findings = append(findings, cites...)
	slices.Sort(findings)
	return findings, nil
}

// numbered returns the names of the numbered regular files of dir, grouped by
// their number. The names of each group are in the order of the directory.
// The dir is a path from the root of fsys.
func numbered(fsys fs.FS, dir string) (map[int][]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	byNumber := make(map[int][]string)
	for _, entry := range entries {
		m := fileName.FindStringSubmatch(entry.Name())
		if m == nil || !entry.Type().IsRegular() {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("parse the number of %s/%s: %w", dir, entry.Name(), err)
		}
		byNumber[n] = append(byNumber[n], entry.Name())
	}
	return byNumber, nil
}

// duplicates returns one finding for each number of dir that more than one
// file has.
func duplicates(dir string, byNumber map[int][]string) []string {
	var findings []string
	for n, names := range byNumber {
		if len(names) < minDuplicates {
			continue
		}
		findings = append(findings, fmt.Sprintf("%s: number %04d is used by %s",
			dir, n, strings.Join(names, ", ")))
	}
	return findings
}

// citedWithoutFile returns one finding for each ADR citation in a .md file
// under docs/ whose number has no file in adrs. It skips the files in exempt.
func citedWithoutFile(fsys fs.FS, adrs map[int][]string, exempt []string) ([]string, error) {
	var findings []string
	err := fs.WalkDir(fsys, docsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || filepath.Ext(path) != ".md" || slices.Contains(exempt, path) {
			return nil
		}
		found, err := citationsIn(fsys, path, adrs)
		if err != nil {
			return err
		}
		findings = append(findings, found...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", docsDir, err)
	}
	return findings, nil
}

// citationsIn returns one finding for each ADR citation in the doc at path
// whose number has no file in adrs. The path is from the root of fsys.
func citationsIn(fsys fs.FS, path string, adrs map[int][]string) ([]string, error) {
	info, err := fs.Stat(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() > maxDocBytes {
		return nil, fmt.Errorf("%s is %d bytes: %w", path, info.Size(), errDocTooLarge)
	}
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var findings []string
	for i, line := range strings.Split(string(data), "\n") {
		for _, m := range citation.FindAllStringSubmatch(line, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				return nil, fmt.Errorf("parse the ADR number in %s:%d: %w", path, i+1, err)
			}
			if _, ok := adrs[n]; !ok {
				findings = append(findings, fmt.Sprintf("%s:%d: cites ADR %d, which has no file in %s",
					path, i+1, n, adrDir))
			}
		}
	}
	return findings, nil
}
