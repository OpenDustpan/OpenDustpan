// Package source defines the source filter of a namespace: which
// submodules and which Git LFS objects a snapshot of a job may hold. This
// package holds the filter, its port and its checks. The service that
// fetches the code of a job is internal.
//
// The package is tool-neutral. It never imports a transport, a store
// adapter or a forge adapter.
package source

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	// ErrInvalid reports a request or a filter with an empty or bad field.
	ErrInvalid = errors.New("source request is not valid")
	// ErrTooLarge reports a filter text above MaxFilterJSON bytes.
	ErrTooLarge = errors.New("source is too large")
)

// Filter is the source filter of a namespace: which submodules and which
// Git LFS objects a snapshot may hold. By default a snapshot holds
// neither.
type Filter struct {
	// Submodules lists the URL patterns of the submodules that the server
	// gets. A submodule whose URL matches no pattern is not in the
	// snapshot.
	Submodules []URLPattern
	// LargeFiles lists the URL patterns of the repositories whose Git LFS
	// objects the server gets.
	LargeFiles []URLPattern
	// FailOnDenied fails the fetch when the commit has a submodule or a Git
	// LFS pointer that the filter does not allow. With false, the snapshot
	// holds the gitlink or the pointer file as it is.
	FailOnDenied bool
	// ProtectedBranches lists the exact names of the branches whose head a
	// job may start from with the fact source.protected. A name is not a
	// pattern. It is empty by default, so no branch is protected. It does
	// not change the content of a snapshot, so Hash leaves it out.
	ProtectedBranches []string
}

// MaxPatterns bounds each list of a filter.
const MaxPatterns = 64

// MaxBranchName bounds the length of a protected branch name, in bytes.
const MaxBranchName = 255

// Validate returns ErrInvalid for more than MaxPatterns patterns in a list,
// a pattern that URLPattern.Validate refuses, more than MaxPatterns
// protected branches, a protected branch that ValidBranchName refuses, or a
// protected branch that the list holds twice.
func (p *Filter) Validate() error {
	for _, list := range [][]URLPattern{p.Submodules, p.LargeFiles} {
		if len(list) > MaxPatterns {
			return fmt.Errorf("%w: a filter list has at most %d patterns", ErrInvalid, MaxPatterns)
		}

		for _, u := range list {
			if err := u.Validate(); err != nil {
				return err
			}
		}
	}

	return validateBranches(p.ProtectedBranches)
}

// validateBranches returns ErrInvalid for a list of protected branches that
// is too long, that holds a name that ValidBranchName refuses, or that holds
// a name twice.
func validateBranches(list []string) error {
	if len(list) > MaxPatterns {
		return fmt.Errorf("%w: the protected branches have at most %d names", ErrInvalid, MaxPatterns)
	}

	seen := make(map[string]struct{}, len(list))

	for _, b := range list {
		if !ValidBranchName(b) {
			return fmt.Errorf("%w: %q is not a valid branch name", ErrInvalid, b)
		}

		if _, dup := seen[b]; dup {
			return fmt.Errorf("%w: branch %q is in the protected branches twice", ErrInvalid, b)
		}

		seen[b] = struct{}{}
	}

	return nil
}

// ValidBranchName reports whether b is an exact branch name that a filter
// may list. It is an allowlist: 1 to MaxBranchName bytes of ASCII letters,
// digits, '.', '_', '-' and '/'. The first byte is not '-'. No part between
// '/' is empty, starts with '.', or ends with ".lock", and ".." does not
// occur. A name that git allows and this check refuses cannot be listed.
func ValidBranchName(b string) bool {
	if b == "" || len(b) > MaxBranchName || b[0] == '-' || strings.Contains(b, "..") {
		return false
	}

	for _, c := range []byte(b) {
		if !branchByte(c) {
			return false
		}
	}

	for part := range strings.SplitSeq(b, "/") {
		if part == "" || part[0] == '.' || strings.HasSuffix(part, ".lock") {
			return false
		}
	}

	return true
}

// branchByte reports whether c is a byte that a branch name may hold.
func branchByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return strings.IndexByte("._-/", c) >= 0
	}
}

// Hash returns a text that changes when the filter changes. It is the
// patterns of each list in order, with the switch. It leaves out the
// protected branches, because they do not change the content of a snapshot.
func (p *Filter) Hash() string {
	parts := make([]string, 0, len(p.Submodules)+len(p.LargeFiles))
	parts = append(parts, "s")

	for _, u := range p.Submodules {
		parts = append(parts, string(u))
	}

	parts = append(parts, "l")

	for _, u := range p.LargeFiles {
		parts = append(parts, string(u))
	}

	parts = append(parts, strconv.FormatBool(p.FailOnDenied))

	return strconv.Quote(strings.Join(parts, " "))
}

// AllowsSubmodule reports whether the filter allows the submodule URL u.
func (p *Filter) AllowsSubmodule(u string) bool {
	return matchAny(p.Submodules, u)
}

// AllowsLargeFiles reports whether the filter allows the Git LFS objects
// of the repository URL u.
func (p *Filter) AllowsLargeFiles(u string) bool {
	return matchAny(p.LargeFiles, u)
}

// matchAny reports whether a pattern of list matches u.
func matchAny(list []URLPattern, u string) bool {
	for _, p := range list {
		if p.Match(u) {
			return true
		}
	}

	return false
}

// URLPattern is an https URL of a repository, with an optional "*" as the
// last path part, which matches one path part. For example
// "https://git.example.com/platform/*" matches
// "https://git.example.com/platform/modules" and
// "https://git.example.com/platform/modules.git", not
// "https://git.example.com/platform/a/b".
type URLPattern string

// Validate returns ErrInvalid for a pattern that is not an https URL with a
// host and a path, that has a user, a query, a fragment, a port or a '%',
// whose host has a character other than an ASCII letter, a digit, '.' or
// '-', that has an empty path part, or that has a "*" that is not the
// whole last path part.
func (u URLPattern) Validate() error {
	h, ok := SplitHTTPS(string(u))
	if !ok || h.Host == "" || len(h.Parts) == 0 {
		return fmt.Errorf("%w: pattern %q must be an https URL of a repository", ErrInvalid, u)
	}

	for i, p := range h.Parts {
		if p == "" || (strings.Contains(p, "*") && (p != "*" || i != len(h.Parts)-1)) {
			return fmt.Errorf("%w: in pattern %q, '*' can only be the whole last path part", ErrInvalid, u)
		}
	}

	return nil
}

// Match reports whether the repository URL target matches u. The host
// matches with no regard to case, the path parts exactly, and a ".git" at
// the end of the target does not count. A target with a '%' never
// matches, so an encoded '/' or '.' cannot pass a pattern. A target with
// a "." or ".." part never matches. The caller resolves a relative URL
// with ResolveURL first.
func (u URLPattern) Match(target string) bool {
	p, ok := SplitHTTPS(string(u))
	t, ok2 := SplitHTTPS(target)

	if !ok || !ok2 || !strings.EqualFold(p.Host, t.Host) || len(p.Parts) != len(t.Parts) {
		return false
	}

	for i, want := range p.Parts {
		got := t.Parts[i]
		if i == len(p.Parts)-1 {
			got = strings.TrimSuffix(got, ".git")
			want = strings.TrimSuffix(want, ".git")
		}

		if !matchPart(want, got) {
			return false
		}
	}

	return true
}

// matchPart reports whether the path part got matches the pattern part
// want: "*" matches one part that is not empty, "." or "..".
func matchPart(want, got string) bool {
	if want == "*" {
		return got != "" && got != "." && got != ".."
	}

	return want == got
}

// HTTPSURL is an https URL split into its host and its path parts. Parts
// is empty for a URL with no path.
type HTTPSURL struct {
	Host  string
	Parts []string
}

// String returns the URL with no '/' at its end.
func (u HTTPSURL) String() string {
	if len(u.Parts) == 0 {
		return "https://" + u.Host
	}

	return "https://" + u.Host + "/" + strings.Join(u.Parts, "/")
}

// SplitHTTPS is the one parser of the https URLs of repositories. It splits
// an https URL with no user, port, query, fragment or '%' into its host and
// its path parts. It takes one '/' off the end of the path. It refuses a
// host with a character other than an ASCII letter, a digit, '.' or '-', or
// with an empty label, and a path part that is empty, "." or "..". So
// "https://h/a/b/" is "https://h/a/b", and "https://h/a//b" and
// "https://h/a/b//" are refused.
func SplitHTTPS(s string) (HTTPSURL, bool) {
	none := HTTPSURL{Host: "", Parts: nil}

	rest, found := strings.CutPrefix(s, "https://")
	if !found {
		return none, false
	}

	host, p, _ := strings.Cut(rest, "/")
	if !validHost(host) {
		return none, false
	}

	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return HTTPSURL{Host: host, Parts: nil}, true
	}

	if strings.ContainsAny(p, "@?#:%\\ \t\n\r") || strings.ContainsRune(p, 0) {
		return none, false
	}

	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return none, false
		}
	}

	return HTTPSURL{Host: host, Parts: parts}, true
}

// validHost reports whether h is empty, or is labels of ASCII letters,
// digits and '-', with '.' between them. An empty host is for the caller
// to refuse. A host with any other character, for example a non-ASCII
// letter that looks like an ASCII one, a '_' or a port, never matches a
// pattern.
func validHost(h string) bool {
	if h == "" {
		return true
	}

	for label := range strings.SplitSeq(h, ".") {
		if label == "" {
			return false
		}

		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}

	return true
}
