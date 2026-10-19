#!/usr/bin/env python3
"""Fail when a Go module version in the build list is younger than 14 days.

A version that the module proxy published a short time ago can be a
compromised release. This check reads the publish time of each module version
from go list, so it needs no extra tool. It runs go with -mod=readonly and
GOWORK=off, so it never changes go.mod or go.sum.

Usage:
    module-age-gate.py             check the modules of the current module
    module-age-gate.py --selftest

Exit code 0: every version is old enough. 1: a version is too young.
2: go failed or printed a line that this script cannot read.

Standard library only.
"""

import os
import subprocess
import sys
import time

MIN_AGE_DAYS = 14

# Each line has the module path, the version, the publish time (Unix seconds,
# empty when the proxy gave none) and the main-module flag.
GO_LIST = [
    "go",
    "list",
    "-mod=readonly",
    "-m",
    "-f",
    "{{.Path}}|{{.Version}}|{{with .Time}}{{.Unix}}{{end}}|{{.Main}}",
    "all",
]


def parse(listing):
    """Return (path, version, published) for each module except the main one.

    published is None when the line has no time. Raise ValueError for a line
    that has the wrong shape.
    """
    modules = []
    for line in listing.splitlines():
        if not line.strip():
            continue
        fields = line.split("|")
        if len(fields) != 4:
            raise ValueError("unexpected line: %r" % line)
        path, version, published, main = fields
        if main == "true":
            continue
        modules.append((path, version, int(published) if published else None))
    return modules


def too_young(modules, now, min_age_days=MIN_AGE_DAYS):
    """Return the (path, version, age in days) of each version below the limit."""
    limit = min_age_days * 86400
    return [
        (path, version, (now - published) / 86400)
        for path, version, published in modules
        if published is not None and now - published < limit
    ]


def untimed(modules):
    """Return the modules that have no publish time (for example a replace)."""
    return [(path, version) for path, version, published in modules if published is None]


def go_env(environ):
    """Return the environment for go: a copy of environ with GOWORK=off."""
    env = dict(environ)
    env["GOWORK"] = "off"
    return env


def report(young, missing):
    """Print each finding and return the exit code."""
    for path, version, age in young:
        print(
            "module age: %s %s was published %.1f days ago. The limit is %d days. "
            "Pin an older version." % (path, version, age, MIN_AGE_DAYS),
            file=sys.stderr,
        )
    for path, version in missing:
        print(
            "module age: %s %s has no publish time, so the check cannot clear it. "
            "Run go mod download and try again." % (path, version),
            file=sys.stderr,
        )
    return 1 if young or missing else 0


def selftest():
    now = 1_000_000_000
    day = 86400
    listing = "\n".join(
        [
            "example.com/main||0|true",
            "example.com/old|v1.0.0|%d|false" % (now - 15 * day),
            "example.com/edge|v1.0.0|%d|false" % (now - 14 * day),
            "example.com/young|v1.2.0|%d|false" % (now - 2 * day),
            "example.com/none|v0.1.0||false",
        ]
    )
    modules = parse(listing)
    if [m[0] for m in modules] != [
        "example.com/old",
        "example.com/edge",
        "example.com/young",
        "example.com/none",
    ]:
        print("selftest: parse gave %s" % modules, file=sys.stderr)
        return 1
    young = too_young(modules, now)
    if [y[0] for y in young] != ["example.com/young"]:
        print("selftest: too_young gave %s" % young, file=sys.stderr)
        return 1
    if untimed(modules) != [("example.com/none", "v0.1.0")]:
        print("selftest: untimed gave %s" % untimed(modules), file=sys.stderr)
        return 1
    try:
        parse("broken line")
    except ValueError:
        pass
    else:
        print("selftest: a broken line must raise", file=sys.stderr)
        return 1
    if go_env({"GOWORK": "/tmp/go.work"}).get("GOWORK") != "off":
        print("selftest: go_env must set GOWORK=off", file=sys.stderr)
        return 1
    if report([], []) != 0:
        print("selftest: a clean list must pass", file=sys.stderr)
        return 1
    # report prints to stderr, so run it with the output closed.
    stderr, sys.stderr = sys.stderr, open(os.devnull, "w", encoding="utf-8")
    try:
        code = report(young, [])
    finally:
        sys.stderr.close()
        sys.stderr = stderr
    if code != 1:
        print("selftest: a young module must fail the check", file=sys.stderr)
        return 1
    print("selftest: ok (the check fails on a version younger than 14 days)")
    return 0


def main(argv):
    if argv[1:] == ["--selftest"]:
        return selftest()
    if len(argv) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    result = subprocess.run(
        GO_LIST,
        stdout=subprocess.PIPE,
        text=True,
        check=False,
        env=go_env(os.environ),
    )
    if result.returncode != 0:
        return 2
    try:
        modules = parse(result.stdout)
    except ValueError as err:
        print("module age: %s" % err, file=sys.stderr)
        return 2
    code = report(too_young(modules, time.time()), untimed(modules))
    if code == 0:
        print("module age: %d modules, none younger than %d days" % (len(modules), MIN_AGE_DAYS))
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv))
