#!/usr/bin/env python3
"""Fail when the server module lists a dependency of the runner only.

The Kubernetes client and the OCI image library are for the runner only.
The runner is its own Go module, so the server module must not require
them. depguard checks the direct imports of each file. This check reads
the module list of the server module, so it also finds a module that
another dependency brings in. It checks the path of each module and the
path of its replacement, so a `replace` directive cannot hide a denied
module. It runs go with GOWORK=off, so a go.work file cannot add the
modules of the workspace to the list.

Usage:
    runner-deps-guard.py             list the modules of the current module
    runner-deps-guard.py --selftest

Standard library only.
"""

import contextlib
import io
import os
import subprocess
import sys

# Each line has the module path, a space and the path of the replacement,
# if the module has one.
GO_LIST = [
    "go",
    "list",
    "-m",
    "-f",
    "{{.Path}} {{with .Replace}}{{.Path}}{{end}}",
    "all",
]

# A module path that starts with one of these prefixes is for the runner only.
DENIED = (
    "k8s.io/",
    "sigs.k8s.io/",
    "github.com/google/go-containerregistry",
)


def denied(path):
    """Return True when the module path is for the runner only."""
    for prefix in DENIED:
        if prefix.endswith("/"):
            if path.startswith(prefix):
                return True
        elif path == prefix or path.startswith(prefix + "/"):
            return True
    return False


def check(listing):
    """Return the denied module paths of a GO_LIST output.

    A line can hold a module path and the path of its replacement. The
    check reads both.
    """
    found = []
    for line in listing.splitlines():
        for path in line.split():
            if denied(path):
                found.append(path)
    return found


def go_env(environ):
    """Return the environment for go: a copy of environ with GOWORK=off."""
    env = dict(environ)
    env["GOWORK"] = "off"
    return env


def report(found):
    """Print each finding and return the exit code."""
    for path in found:
        print(
            f"go list -m all: {path}: the server module must not require a "
            "dependency of the runner. Remove the import that brings it in.",
            file=sys.stderr,
        )
    return 1 if found else 0


def selftest():
    listing = "\n".join(
        [
            "opendustpan.org/opendustpan",
            "golang.org/x/net v0.60.0",
            "k8s.io/client-go v0.37.1",
            "sigs.k8s.io/yaml v1.6.0",
            "github.com/google/go-containerregistry v0.22.1",
            "github.com/google/go-containerregistry/pkg/authn/k8schain v0.0.0",
            "github.com/google/go-containerregistryx v1.0.0",
            "example.com/k8s.io/thing",
            "example.com/x k8s.io/client-go",
            "example.com/y example.com/z",
            "example.com/w ../local",
        ]
    )
    want = [
        "k8s.io/client-go",
        "sigs.k8s.io/yaml",
        "github.com/google/go-containerregistry",
        "github.com/google/go-containerregistry/pkg/authn/k8schain",
        "k8s.io/client-go",
    ]
    got = check(listing)
    if got != want:
        print(f"selftest: got {got}, want {want}", file=sys.stderr)
        return 1
    env = go_env({"GOWORK": "/tmp/go.work", "PATH": "/usr/bin"})
    if env.get("GOWORK") != "off" or env.get("PATH") != "/usr/bin":
        print(f"selftest: go_env with a go.work gave {env}", file=sys.stderr)
        return 1
    if go_env({}).get("GOWORK") != "off":
        print("selftest: go_env with no GOWORK must set GOWORK=off", file=sys.stderr)
        return 1
    if report([]) != 0:
        print("selftest: a clean list must pass", file=sys.stderr)
        return 1
    with contextlib.redirect_stderr(io.StringIO()):
        code = report(check("k8s.io/api"))
    if code != 1:
        print("selftest: a denied module must fail the check", file=sys.stderr)
        return 1
    print(
        "selftest: ok (the check fails on the denied modules and replacements, "
        "and go runs with GOWORK=off)"
    )
    return 0


def main(argv):
    if argv[1:] == ["--selftest"]:
        return selftest()
    if len(argv) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    # go list prints its own error, so a failed run only sets the exit code.
    result = subprocess.run(
        GO_LIST,
        stdout=subprocess.PIPE,
        text=True,
        check=False,
        env=go_env(os.environ),
    )
    if result.returncode != 0:
        return 1
    return report(check(result.stdout))


if __name__ == "__main__":
    sys.exit(main(sys.argv))
