#!/usr/bin/env python3
"""Run the TruffleHog scan and retry a result that stays unknown.

TruffleHog sends each candidate to its provider for verification. An
"unknown" result is a candidate whose verification request did not finish,
for example because the provider did not answer. A second run can verify
such a candidate. This gate runs the scan again, at most two more times,
when every result is unknown. A verified result fails at once. A command
that exits with an error and gives no result fails at once.

The gate never prints the fields that can hold the secret: Raw, RawV2,
Redacted and ExtraData.

Usage:
  trufflehog-gate.py scan -- <trufflehog command and arguments>
      Runs the command with --json added and without --fail. Fails when a
      result is verified, or when it stays unknown after the retries.
  trufflehog-gate.py --selftest
      Checks the decision table of this script.

Standard library only.
"""

import json
import subprocess
import sys

# The scan runs at most three times: the first run and two retries.
MAX_RUNS = 3


class ScanError(Exception):
    """The scan command failed and gave no result."""

    def __init__(self, returncode):
        super().__init__("the scan command failed with exit code %d" % returncode)
        self.returncode = returncode


def with_json(command):
    """Return the command with --json added and --fail removed."""
    args = [arg for arg in command if arg != "--fail" and not arg.startswith("--fail=")]
    if "--json" not in args:
        args.append("--json")
    return args


def parse_results(text):
    """Return each line of text that is a JSON object. Ignore the other lines."""
    results = []
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(value, dict):
            results.append(value)
    return results


def run_command(args):
    """Run one scan and return its results.

    Raise ScanError when the command exits with an error and gives no result.
    """
    result = subprocess.run(args, stdout=subprocess.PIPE, text=True, check=False)
    results = parse_results(result.stdout)
    if result.returncode != 0 and not results:
        raise ScanError(result.returncode)
    return results


def is_verified(result):
    """Return True when the result carries "Verified": true."""
    return result.get("Verified") is True


def nested(value, *keys):
    """Return the value at the key path, or an empty string when a key is absent.

    A missing key, or a value that is not a mapping, gives an empty string.
    """
    for key in keys:
        if not isinstance(value, dict):
            return ""
        value = value.get(key)
    return "" if value is None else value


def result_line(state, result):
    """Return the message line of one result. Never print the secret fields."""
    return (
        "trufflehog-gate: %s result: detector %s, file %s, line %s, commit %s"
        % (
            state,
            nested(result, "DetectorName"),
            nested(result, "SourceMetadata", "Data", "Git", "file"),
            nested(result, "SourceMetadata", "Data", "Git", "line"),
            nested(result, "SourceMetadata", "Data", "Git", "commit"),
        )
    )


def failure_lines(runs, verified, unknown):
    """Return the message lines of a failure."""
    lines = [
        "trufflehog-gate: %d run%s made: %d verified, %d unknown"
        % (runs, "" if runs == 1 else "s", len(verified), len(unknown))
    ]
    for result in verified:
        lines.append(result_line("verified", result))
    for result in unknown:
        lines.append(result_line("unknown", result))
    return lines


def decide(run):
    """Return (exit code, message lines) for the scan.

    run() returns the list of results of one run. It raises ScanError when
    the scan command fails and gives no result. A verified result fails at
    once. A run with no result passes. Only unknown results give a retry,
    at most two, and a third unknown result fails.
    """
    runs = 0
    unknown = []
    while runs < MAX_RUNS:
        runs += 1
        try:
            results = run()
        except ScanError as err:
            return 1, [
                "trufflehog-gate: run %d failed with exit code %d and no result. "
                "Run the trufflehog command by hand to see the error."
                % (runs, err.returncode)
            ]
        verified = [result for result in results if is_verified(result)]
        unknown = [result for result in results if not is_verified(result)]
        if verified:
            return 1, failure_lines(runs, verified, unknown)
        if not results:
            return 0, []
    return 1, failure_lines(runs, [], unknown)


def scan(command):
    """Run the gate over the given command and return its exit code."""
    args = with_json(command)
    code, lines = decide(lambda: run_command(args))
    for line in lines:
        print(line, file=sys.stderr)
    if code == 0:
        print("trufflehog-gate: no verified secret found")
    return code


def fake_runner(runs):
    """Return a run() over the given run outcomes, and the list of the runs made.

    An outcome is a list of results, or an exception for run() to raise.
    """
    made = []

    def run():
        if len(made) >= len(runs):
            raise AssertionError("the decision made more runs than the case has")
        outcome = runs[len(made)]
        made.append(outcome)
        if isinstance(outcome, Exception):
            raise outcome
        return outcome

    return run, made


def check_case(case):
    """Return an error string when the case fails, or None when it passes."""
    run, made = fake_runner(case["runs"])
    try:
        code, lines = decide(run)
    except Exception as err:  # The fake run raises AssertionError for an extra run.
        return "raised %s: %s" % (type(err).__name__, err)
    if code != case["code"]:
        return "exit code %d, want %d" % (code, case["code"])
    if len(made) != case["made"]:
        return "%d runs made, want %d" % (len(made), case["made"])
    text = "\n".join(lines)
    for fragment in case.get("shows", []):
        if fragment not in text:
            return "the message does not name %r" % fragment
    for fragment in case.get("hides", []):
        if fragment in text:
            return "the message prints %r" % fragment
    if code == 0 and lines:
        return "a passing case printed %r" % lines
    return None


def make_result(verified, detector="DetectorA", file="config/app.tf", line=7, commit="abc123"):
    """Return a scan result in the shape of the TruffleHog JSON output."""
    return {
        "DetectorName": detector,
        "Verified": verified,
        "SourceMetadata": {
            "Data": {"Git": {"file": file, "line": line, "commit": commit}}
        },
    }


RAW_VALUE = "raw-value-that-the-selftest-keeps"
RAW_V2_VALUE = "raw-v2-value-that-the-selftest-keeps"
REDACTED_VALUE = "redacted-value-that-the-selftest-keeps"
EXTRA_VALUE = "extra-value-that-the-selftest-keeps"


def secret_result():
    """Return a verified result that carries the secret fields."""
    result = make_result(True)
    result["Raw"] = RAW_VALUE
    result["RawV2"] = RAW_V2_VALUE
    result["Redacted"] = REDACTED_VALUE
    result["ExtraData"] = {"note": EXTRA_VALUE}
    return result


CASES = [
    {
        "name": "verified on the first run",
        "runs": [[make_result(True)]],
        "code": 1,
        "made": 1,
        "shows": ["1 run made", "verified", "DetectorA", "config/app.tf", "line 7", "abc123"],
    },
    {
        "name": "verified and unknown on the first run",
        "runs": [
            [make_result(True, detector="DetectorA"), make_result(False, detector="DetectorB")]
        ],
        "code": 1,
        "made": 1,
        "shows": ["1 run made", "DetectorA", "DetectorB", "verified", "unknown"],
    },
    {
        "name": "no result",
        "runs": [[]],
        "code": 0,
        "made": 1,
    },
    {
        "name": "unknown, then no result",
        "runs": [[make_result(False)], []],
        "code": 0,
        "made": 2,
    },
    {
        "name": "unknown, unknown, then no result",
        "runs": [[make_result(False)], [make_result(False)], []],
        "code": 0,
        "made": 3,
    },
    {
        "name": "unknown three times",
        "runs": [[make_result(False)], [make_result(False)], [make_result(False)]],
        "code": 1,
        "made": 3,
        "shows": ["3 runs made", "unknown", "DetectorA", "config/app.tf"],
    },
    {
        "name": "unknown, then verified",
        "runs": [[make_result(False)], [make_result(True)]],
        "code": 1,
        "made": 2,
        "shows": ["2 runs made", "verified", "DetectorA"],
    },
    {
        "name": "a result without SourceMetadata",
        "runs": [[{"DetectorName": "DetectorA", "Verified": True}]],
        "code": 1,
        "made": 1,
        "shows": ["1 run made", "verified", "DetectorA"],
    },
    {
        "name": "the failure message hides Raw, RawV2, Redacted and ExtraData",
        "runs": [[secret_result()]],
        "code": 1,
        "made": 1,
        "hides": [RAW_VALUE, RAW_V2_VALUE, REDACTED_VALUE, EXTRA_VALUE],
    },
    {
        "name": "the scan command fails and gives no result",
        "runs": [ScanError(2)],
        "code": 1,
        "made": 1,
        "shows": ["exit code 2"],
    },
]


def selftest():
    for number, case in enumerate(CASES, 1):
        error = check_case(case)
        if error is not None:
            print(
                "trufflehog-gate selftest: case %d (%s) failed: %s"
                % (number, case["name"], error),
                file=sys.stderr,
            )
            return 1
    parsed = parse_results(
        '{"Verified": false}\na log line\n[1, 2]\n\n{"Verified": true}\n'
    )
    if parsed != [{"Verified": False}, {"Verified": True}]:
        print("trufflehog-gate selftest: parse_results gave %r" % (parsed,), file=sys.stderr)
        return 1
    args = with_json(["trufflehog", "git", "file://.", "--fail", "--results=verified,unknown"])
    if args != ["trufflehog", "git", "file://.", "--results=verified,unknown", "--json"]:
        print("trufflehog-gate selftest: with_json gave %r" % (args,), file=sys.stderr)
        return 1
    if nested({"SourceMetadata": []}, "SourceMetadata", "Data", "Git", "file") != "":
        print("trufflehog-gate selftest: nested did not keep a non-mapping safe", file=sys.stderr)
        return 1
    print("trufflehog-gate selftest: %d cases passed" % len(CASES))
    return 0


def main(argv):
    if argv[1:] == ["--selftest"]:
        return selftest()
    if len(argv) >= 4 and argv[1] == "scan" and argv[2] == "--":
        return scan(argv[3:])
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
