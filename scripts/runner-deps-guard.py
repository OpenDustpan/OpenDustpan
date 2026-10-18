#!/usr/bin/env python3
"""Fail when the server module lists a dependency of the runner only.

The Kubernetes client and the OCI image library are for the runner only.
The runner is its own Go module, so the server module must not require
them. depguard checks the direct imports of each file. This check reads
the module list of the server module, so it also finds a module that
another dependency brings in.

Usage:
    runner-deps-guard.py             run `go list -m all` in the current module
    runner-deps-guard.py --selftest

Standard library only.
"""

import contextlib
import io
import subprocess
import sys

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
    """Return the denied module paths of a `go list -m all` output."""
    found = []
    for line in listing.splitlines():
        fields = line.split()
        if fields and denied(fields[0]):
            found.append(fields[0])
    return found


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
            "example.com/k8s.io/thing v1.0.0",
        ]
    )
    want = [
        "k8s.io/client-go",
        "sigs.k8s.io/yaml",
        "github.com/google/go-containerregistry",
        "github.com/google/go-containerregistry/pkg/authn/k8schain",
    ]
    got = check(listing)
    if got != want:
        print(f"selftest: got {got}, want {want}", file=sys.stderr)
        return 1
    if report([]) != 0:
        print("selftest: a clean list must pass", file=sys.stderr)
        return 1
    with contextlib.redirect_stderr(io.StringIO()):
        code = report(check("k8s.io/api v0.37.1"))
    if code != 1:
        print("selftest: a denied module must fail the check", file=sys.stderr)
        return 1
    print("selftest: ok (the check fails on the denied modules)")
    return 0


def main(argv):
    if argv[1:] == ["--selftest"]:
        return selftest()
    if len(argv) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    # go list prints its own error, so a failed run only sets the exit code.
    result = subprocess.run(
        ["go", "list", "-m", "all"],
        stdout=subprocess.PIPE,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        return 1
    return report(check(result.stdout))


if __name__ == "__main__":
    sys.exit(main(sys.argv))
