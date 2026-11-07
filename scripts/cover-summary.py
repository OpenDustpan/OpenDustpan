#!/usr/bin/env python3
"""Write a coverage summary from a Go cover profile.

The script reads a profile that `go test -coverprofile` wrote. It prints the
total and one line for each package to the output. When GITHUB_STEP_SUMMARY
names a file, it also appends a Markdown table to that file.

A block that appears more than once (several test binaries, -coverpkg) counts
once. It is covered when any record of it has a count above zero. The totals
are statement percentages, the same as `go tool cover -func` gives.

Usage:
    cover-summary.py coverage.out
    cover-summary.py --selftest

Standard library only.
"""

import os
import sys
import tempfile

# The module path is the prefix of each package path in a profile. The script
# removes it from the table, so that the table shows short paths. It reads the
# module path from go.mod in the current directory, and uses this value when
# go.mod is not there.
MODULE_PREFIX = "opendustpan.org/opendustpan/"


def check_path(file_name, where):
    """Refuse a path that holds a character which can break a Markdown table."""
    if not file_name.isprintable() or any(c in file_name for c in "|`"):
        raise ValueError(f"{where}: refused a path with a pipe, a backtick or a control character")


def parse(path):
    """Return {(file, block): (statements, covered)} for a profile."""
    blocks = {}
    mode_seen = False
    with open(path, encoding="utf-8") as profile:
        for number, raw in enumerate(profile, start=1):
            line = raw.rstrip("\n")
            if not line:
                continue
            if line.startswith("mode:"):
                mode_seen = True
                continue
            if not mode_seen:
                raise ValueError(f"{path}:{number}: no mode line before the first block")
            # The path can hold spaces and colons, so split from the right.
            fields = line.rsplit(" ", 2)
            if len(fields) != 3:
                raise ValueError(f"{path}:{number}: not a block record")
            where, statements, count = fields
            file_name, sep, block = where.rpartition(":")
            if not sep or not file_name:
                raise ValueError(f"{path}:{number}: no file name in the block record")
            try:
                statements = int(statements)
                count = int(count)
            except ValueError as err:
                raise ValueError(f"{path}:{number}: number expected: {err}") from err
            check_path(file_name, f"{path}:{number}")
            key = (file_name, block)
            previous = blocks.get(key)
            covered = count > 0 or (previous is not None and previous[1])
            blocks[key] = (statements, covered)
    if not mode_seen:
        raise ValueError(f"{path}: no mode line")
    return blocks


def by_package(blocks):
    """Return {package: [statements, covered statements]}."""
    packages = {}
    for (file_name, _), (statements, covered) in blocks.items():
        package = file_name.rpartition("/")[0]
        total = packages.setdefault(package, [0, 0])
        total[0] += statements
        if covered:
            total[1] += statements
    return packages


def percent(covered, statements):
    if statements == 0:
        return 0.0
    return 100.0 * covered / statements


def module_prefix():
    """Return the module path of go.mod in the current directory, with a slash."""
    try:
        with open("go.mod", encoding="utf-8") as handle:
            first = handle.readline().split()
    except OSError:
        return MODULE_PREFIX
    if len(first) == 2 and first[0] == "module":
        return first[1] + "/"
    return MODULE_PREFIX


def short(package):
    return package.removeprefix(module_prefix())


def render_text(packages):
    total = [sum(v[0] for v in packages.values()), sum(v[1] for v in packages.values())]
    lines = []
    for package in sorted(packages):
        statements, covered = packages[package]
        lines.append(f"{percent(covered, statements):6.1f}%  {covered:7d}/{statements:<7d}  {short(package)}")
    lines.append(f"{percent(total[1], total[0]):6.1f}%  {total[1]:7d}/{total[0]:<7d}  total")
    return "\n".join(lines) + "\n"


def render_markdown(packages):
    total = [sum(v[0] for v in packages.values()), sum(v[1] for v in packages.values())]
    lines = [
        "## Go test coverage",
        "",
        f"Total: **{percent(total[1], total[0]):.1f}%** ({total[1]} of {total[0]} statements)",
        "",
        "| Package | Coverage | Statements |",
        "| --- | ---: | ---: |",
    ]
    for package in sorted(packages):
        check_path(package, "package")
        statements, covered = packages[package]
        lines.append(f"| `{short(package)}` | {percent(covered, statements):.1f}% | {covered}/{statements} |")
    return "\n".join(lines) + "\n"


def selftest():
    profile = (
        "mode: set\n"
        "m/a/x.go:1.1,2.2 2 0\n"
        "m/a/x.go:1.1,2.2 2 1\n"
        "m/a/x.go:3.1,4.2 1 0\n"
        "m/b/y.go:1.1,2.2 4 3\n"
        "m/b/with space.go:1.1,2.2 1 0\n"
    )
    with tempfile.TemporaryDirectory() as directory:
        path = os.path.join(directory, "c.out")
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(profile)
        packages = by_package(parse(path))
        assert packages == {"m/a": [3, 2], "m/b": [5, 4]}, packages
        text = render_text(packages)
        assert text.splitlines()[-1].endswith("total"), text
        assert text.splitlines()[-1] == "  75.0%        6/8        total", repr(text)
        assert "| `m/a` | 66.7% | 2/3 |" in render_markdown(packages)
        # Defense in depth: parse rejects a path that could break a Markdown
        # table; render_markdown also refuses it, so a new caller cannot skip
        # the check in parse.
        for hostile in ("m/a|b/x.go", "m/a`b/x.go", "m/a\nb/x.go"):
            try:
                render_markdown({hostile.rpartition("/")[0]: [1, 1]})
            except ValueError:
                pass
            else:
                raise AssertionError(f"render_markdown must refuse {hostile!r}")
        hostile_profile = os.path.join(directory, "hostile.out")
        with open(hostile_profile, "w", encoding="utf-8") as handle:
            handle.write("mode: set\nm/a|b/x.go:1.1,2.2 1 1\n")
        try:
            parse(hostile_profile)
        except ValueError:
            pass
        else:
            raise AssertionError("parse must refuse a path with a pipe")
        bad = os.path.join(directory, "bad.out")
        with open(bad, "w", encoding="utf-8") as handle:
            handle.write("m/a/x.go:1.1,2.2 2 0\n")
        try:
            parse(bad)
        except ValueError:
            pass
        else:
            raise AssertionError("a profile without a mode line must fail")
    print("cover-summary selftest ok")


def main(argv):
    if argv[1:] == ["--selftest"]:
        selftest()
        return 0
    if len(argv) != 2:
        sys.stderr.write("usage: cover-summary.py coverage.out\n")
        return 2
    try:
        packages = by_package(parse(argv[1]))
    except (OSError, ValueError) as err:
        sys.stderr.write(f"cover-summary: {err}\n")
        return 1
    if not packages:
        sys.stderr.write(f"cover-summary: {argv[1]} has no block\n")
        return 1
    sys.stdout.write(render_text(packages))
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as handle:
            handle.write(render_markdown(packages))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
