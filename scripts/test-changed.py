#!/usr/bin/env python3
"""Run a quick local check of the Go packages that a branch changes.

The check is fast and cheap. It is not complete: CI runs all the checks. The
script does these steps:

1. `go build ./...` for the full module. When it fails, the script stops.
2. The checks of the full repository that take seconds: go mod tidy, sqlc
   diff, clients-check, nolint-check, files-check, ignored-check and
   subtestcheck.
3. golangci-lint (also of the journal build) and nilaway on the packages
   that hold a changed file.
4. `go test` on those packages and on each package whose tests import one
   of them. The flags are the defaults, so the test cache stays valid: no
   -race and no -count.

Each step runs also when an earlier step fails, so that one run shows all
the failures.

A changed file is a file that differs between the merge base with the base
ref and the working tree, or an untracked file. A file belongs to the
package of its directory, or of the nearest parent directory that is a
package. The test step leaves out the two store packages, because their
suites are slow, unless a changed file is under pkg/store/.

The script fails open. When it cannot find the changed files or the
packages, or a tool is not installed, it writes a note and skips that step.
Only a failed check gives a non-zero exit status.

Usage:
    test-changed.py [--base REF] [--timeout DURATION]
    test-changed.py --selftest

Standard library only.
"""

import json
import os
import shutil
import subprocess
import sys

STORE_PREFIX = "pkg/store/"
STORE_DIRS = ("internal/store/sqlite", "internal/store/postgres")


def note(text):
    print(f"test-changed: {text}", file=sys.stderr, flush=True)


def git_lines(args):
    """Return the output lines of a git command, or None when it fails."""
    try:
        out = subprocess.run(["git", *args], capture_output=True, text=True, check=True).stdout
    except (OSError, subprocess.CalledProcessError):
        return None
    return [line for line in out.splitlines() if line]


def changed_files(base):
    """Return the changed paths, relative to the repository root, or None."""
    merge_base = git_lines(["merge-base", "HEAD", base])
    if not merge_base:
        note(f"found no merge base with {base}; skipped the lint and the tests")
        return None
    diff = git_lines(["diff", "--name-only", "--no-renames", merge_base[0]])
    untracked = git_lines(["ls-files", "--others", "--exclude-standard"])
    if diff is None or untracked is None:
        note("git diff failed; skipped the lint and the tests")
        return None
    return sorted(set(diff) | set(untracked))


def parse_go_list(text, root):
    """Parse the JSON stream of `go list -e -test -json`.

    Return (dirs, graph). dirs maps a package directory, relative to root,
    to its import path. graph maps the import path of each package under
    test to the set of packages that its tests depend on.
    """
    decoder = json.JSONDecoder()
    dirs = {}
    graph = {}
    pos = 0
    while True:
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos >= len(text):
            break
        obj, pos = decoder.raw_decode(text, pos)
        path = obj.get("ImportPath", "")
        if not path or path.endswith(".test"):
            continue
        # A test variant "p [p.test]" or "p_test [p.test]" names p in ForTest.
        owner = obj.get("ForTest") or path
        graph.setdefault(owner, set()).update(obj.get("Deps") or ())
        if obj.get("ForTest"):
            continue
        # Leave out a directory that has no file for this build, for example
        # one whose files all have a build tag.
        if not (obj.get("GoFiles") or obj.get("TestGoFiles") or obj.get("XTestGoFiles")):
            graph.pop(owner, None)
            continue
        directory = obj.get("Dir") or ""
        rel = os.path.relpath(directory, root) if directory else ""
        if rel and not rel.startswith(".."):
            dirs[rel] = path
    graph = {p: deps for p, deps in graph.items() if p in dirs.values()}
    return dirs, graph


def owning_package(file_name, dirs):
    """Return the import path of the package that holds file_name, or None."""
    directory = os.path.dirname(file_name)
    while directory:
        if directory in dirs:
            return dirs[directory]
        directory = os.path.dirname(directory)
    return None


def select(files, dirs, graph):
    """Return (lint_dirs, test_dirs): sorted directories relative to the root."""
    paths = {p for p in (owning_package(f, dirs) for f in files) if p}
    by_path = {p: d for d, p in dirs.items()}
    tested = {p for p, deps in graph.items() if p in paths or paths & deps}
    if not any(f.startswith(STORE_PREFIX) for f in files):
        tested -= {dirs[d] for d in STORE_DIRS if d in dirs}
    lint_dirs = sorted(by_path[p] for p in paths if p in by_path)
    test_dirs = sorted(by_path[p] for p in tested if p in by_path)
    return lint_dirs, test_dirs


def load_packages(root):
    """Return (dirs, graph) from go list, or None when go list fails."""
    try:
        out = subprocess.run(
            ["go", "list", "-e", "-test",
             "-json=ImportPath,Dir,ForTest,Deps,GoFiles,TestGoFiles,XTestGoFiles", "./..."],
            capture_output=True, text=True, check=True, cwd=root,
        ).stdout
        return parse_go_list(out, root)
    except (OSError, subprocess.CalledProcessError, ValueError) as err:
        note(f"go list failed ({err}); skipped the lint and the tests")
        return None


def run(args, root):
    print("+ " + " ".join(args), flush=True)
    return subprocess.run(args, cwd=root, check=False).returncode


def make(target, *variables):
    return ["make", "--no-print-directory", target, *variables]


def repo_checks(base):
    """Return (name, tool, argv) for each check of the full repository.

    Each one takes seconds, so it needs no selection. A check whose tool is
    not installed is skipped.
    """
    return [
        ("go mod tidy", "go", ["go", "mod", "tidy", "-diff"]),
        ("sqlc diff", "sqlc", ["sqlc", "diff"]),
        ("clients-check", "make", make("clients-check")),
        ("nolint-check", "make", make("nolint-check", f"BASE={base}")),
        ("files-check", "make", make("files-check", f"FILES_BASE={base}")),
        ("ignored-check", "make", make("ignored-check")),
        ("subtestcheck", "make", make("subtestcheck")),
    ]


def package_checks(lint_dirs, test_dirs, timeout):
    """Return (name, tool, argv) for each check of the selected packages."""
    lint = ["./" + d for d in lint_dirs]
    test = ["./" + d for d in test_dirs]
    checks = []
    if lint:
        pkgs = " ".join(lint)
        checks += [
            ("golangci-lint", "golangci-lint", ["golangci-lint", "run", *lint]),
            ("golangci-lint of the journal build", "golangci-lint",
             make("lint-journal-build", f"LINT_PKGS={pkgs}")),
            ("nilaway", "make", make("nilaway", f"NILAWAY_PKGS={pkgs}")),
        ]
    if test:
        checks.append(("go test", "go", ["go", "test", "-timeout", timeout, *test]))
    return checks


def main(argv):
    base = "origin/main"
    timeout = "20m"
    args = list(argv)
    while args:
        flag = args.pop(0)
        if flag in ("--base", "--timeout") and args:
            value = args.pop(0)
            if flag == "--base":
                base = value
            else:
                timeout = value
        else:
            print(__doc__, file=sys.stderr)
            return 2

    top = git_lines(["rev-parse", "--show-toplevel"])
    if not top:
        note("not in a git checkout; nothing to check")
        return 0
    root = top[0]

    # The other checks cannot pass when the build fails, so stop here.
    if run(["go", "build", "./..."], root) != 0:
        return 1

    checks = repo_checks(base)
    files = changed_files(base)
    packages = load_packages(root) if files else None
    if files == []:
        note(f"no changed file since the merge base with {base}; skipped the lint and the tests")
    if packages is not None:
        lint_dirs, test_dirs = select(files, *packages)
        if not test_dirs:
            note("no changed Go package; skipped the lint and the tests")
        checks += package_checks(lint_dirs, test_dirs, timeout)

    # Run each check, also after a failure, so that one run shows all failures.
    failed = []
    for name, tool, argv in checks:
        if shutil.which(tool) is None:
            note(f"{tool} is not installed; skipped {name}")
            continue
        if run(argv, root) != 0:
            failed.append(name)
    if failed:
        note("failed: " + ", ".join(failed))
        return 1
    return 0


def selftest():
    mod = "example.org/m"
    root = "/r"
    stream = "\n".join(json.dumps(o) for o in [
        {"ImportPath": f"{mod}/a", "Dir": "/r/a", "GoFiles": ["a.go"]},
        {"ImportPath": f"{mod}/a [{mod}/a.test]", "Dir": "/r/a", "ForTest": f"{mod}/a",
         "Deps": ["fmt"], "GoFiles": ["a.go"]},
        {"ImportPath": f"{mod}/b", "Dir": "/r/b", "GoFiles": ["b.go"], "Deps": [f"{mod}/a"]},
        {"ImportPath": f"{mod}/c", "Dir": "/r/c", "XTestGoFiles": ["c_test.go"]},
        {"ImportPath": f"{mod}/c_test [{mod}/c.test]", "Dir": "/r/c", "ForTest": f"{mod}/c",
         "Deps": [f"{mod}/b", f"{mod}/a"]},
        {"ImportPath": f"{mod}/c.test", "Dir": "/r/c", "Deps": [f"{mod}/a"]},
        {"ImportPath": f"{mod}/tagged", "Dir": "/r/tagged", "Error": {"Err": "build constraints"}},
        {"ImportPath": f"{mod}/internal/store/sqlite", "Dir": "/r/internal/store/sqlite",
         "GoFiles": ["s.go"], "Deps": [f"{mod}/a"]},
        {"ImportPath": f"{mod}/pkg/store/storetest", "Dir": "/r/pkg/store/storetest",
         "GoFiles": ["t.go"]},
    ])
    dirs, graph = parse_go_list(stream, root)
    assert "tagged" not in dirs, dirs
    assert f"{mod}/c.test" not in graph, graph

    cases = [
        # A change in a leaf package selects its reverse dependencies, without the store.
        (["a/a.go"], ["a"], ["a", "b", "c"]),
        # A test-only package is linted and tested.
        (["c/c_test.go"], ["c"], ["c"]),
        # A file in testdata belongs to the package above it.
        (["b/testdata/x.json"], ["b"], ["b", "c"]),
        # A change under pkg/store/ keeps the store suites.
        (["a/a.go", "pkg/store/storetest/t.go"], ["a", "pkg/store/storetest"],
         ["a", "b", "c", "internal/store/sqlite", "pkg/store/storetest"]),
        # Files outside any package, deleted packages and tagged packages select nothing.
        (["README.md", "docs/x.md", "gone/g.go", "tagged/t.go"], [], []),
        ([], [], []),
    ]
    for files, want_lint, want_test in cases:
        got = select(files, dirs, graph)
        assert got == (want_lint, want_test), (files, got)

    # No selected package gives no package check, so no tool gets an empty list.
    assert package_checks([], [], "1m") == []
    names = [name for name, _, _ in package_checks([], ["b"], "1m")]
    assert names == ["go test"], names
    names = [name for name, _, _ in package_checks(["a"], ["a"], "1m")]
    assert names == ["golangci-lint", "golangci-lint of the journal build", "nilaway", "go test"], names

    assert parse_go_list("", root) == ({}, {})
    assert parse_go_list("  \n", root) == ({}, {})
    print("selftest: ok")
    return 0


if __name__ == "__main__":
    if sys.argv[1:] == ["--selftest"]:
        sys.exit(selftest())
    sys.exit(main(sys.argv[1:]))
