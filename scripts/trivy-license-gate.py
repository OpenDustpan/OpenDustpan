#!/usr/bin/env python3
"""Checks a Trivy license report for the Go modules of this repository.

Trivy reads the license of a Go module from the module cache. When the cache
has no entry for a module, Trivy reports no license for it, and the scan still
passes. This script fails in that case, so the gate cannot pass without
scanning the modules.

Usage:
  trivy-license-gate.py gate <report.json> <go.mod>
      Fails unless every requirement of go.mod has a license result.
  trivy-license-gate.py canary <report.json>
      Fails unless the report flags the GPL fixture in testdata/trivy-canary.
"""

import json
import subprocess
import sys

CANARY_MODULE = "github.com/xen0n/gosmopolitan"
# Trivy gives each license a severity. HIGH is a restricted license (GPL),
# CRITICAL a forbidden one (AGPL), UNKNOWN a license that Trivy cannot classify.
# LOW (MIT, BSD, Apache-2.0) and MEDIUM (MPL-2.0) pass, as docs/research/licenses.md
# accepts. The scan does not filter by severity, because a --severity filter
# also drops the LOW results, and then the scan finds nothing.
FAILING_SEVERITIES = ("UNKNOWN", "HIGH", "CRITICAL")


def load_report(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def license_results(report):
    """Yields (module, license name, severity) for each license result."""
    for result in report.get("Results") or []:
        for found in result.get("Licenses") or []:
            yield found.get("PkgName", ""), found.get("Name", ""), found.get("Severity", "")


def required_modules(go_mod):
    """Returns the module paths that go.mod requires, direct and indirect."""
    out = subprocess.run(
        ["go", "mod", "edit", "-json", go_mod],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    return sorted({req["Path"] for req in json.loads(out).get("Require") or []})


def failing_licenses(report):
    """Returns the (module, name, severity) results that fail the gate."""
    return [
        (module, name, severity)
        for module, name, severity in license_results(report)
        if severity in FAILING_SEVERITIES
    ]


def gate(report_path, go_mod):
    report = load_report(report_path)

    reported = set()
    for module, _, _ in license_results(report):
        if module:
            reported.add(module)

    expected = required_modules(go_mod)
    missing = [module for module in expected if module not in reported]
    if missing:
        print(
            "license gate: %d of %d requirements of %s have no license result: %s"
            % (len(missing), len(expected), go_mod, ", ".join(missing)),
            file=sys.stderr,
        )
        print(
            "license gate: the module cache lacks these modules. Run go mod download and make trivy again.",
            file=sys.stderr,
        )
        return 1

    failing = failing_licenses(report)
    if failing:
        for module, name, severity in failing:
            print(
                "license gate: %s is %s (%s)" % (module, name, severity),
                file=sys.stderr,
            )
        return 1

    print(
        "license gate: %d requirements of %s have a license result, none restricted"
        % (len(expected), go_mod)
    )
    return 0


def canary(report_path):
    for module, name, severity in failing_licenses(load_report(report_path)):
        if module == CANARY_MODULE:
            print("license canary: %s is %s (%s), as expected" % (module, name, severity))
            return 0

    print(
        "license canary: %s was not flagged as a restricted license. The license scan cannot be trusted."
        % CANARY_MODULE,
        file=sys.stderr,
    )
    return 1


def main(argv):
    if len(argv) == 4 and argv[1] == "gate":
        return gate(argv[2], argv[3])
    if len(argv) == 3 and argv[1] == "canary":
        return canary(argv[2])
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
